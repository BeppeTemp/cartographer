package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// cliCommandRE is what a generic connection must never be told to run (D1101):
// `cartographer ` followed by one of the CLI's top-level commands (the case
// labels of run in cmd/cartographer/main.go, kept as a literal list: a new
// command is added here), or one of the bare forms the messages used to carry.
// Capital-C "Cartographer" in prose and the hyphenated skill names do not match.
var cliCommandRE = regexp.MustCompile("cartographer (serve|setup|kb|version|update|hook|help|agents|connect|disconnect|reconnect|status|sync|approve|service|upgrade-repair|import|resolve|paths|audit|doctor|reindex|config|client|workspace|mcp)\\b" +
	"|kb reauthor|kb repair|workspace bind|workspace resolve|client bind|service restart")

func assertNoCLICommand(t *testing.T, what, text string) {
	t.Helper()
	if m := cliCommandRE.FindString(text); m != "" {
		t.Errorf("%s tells a generic connection to run %q: %.600s", what, m, text)
	}
}

type operatorTextRig struct {
	handler http.Handler
	kbA     *kb.KB
}

// newOperatorTextRig mounts kb-a, kb-b and kb-c; kb-old is an alias of kb-a, so
// ?kbs=kb-old,kb-b,gone yields unknown (gone), renamed (kb-old) and unbound (kb-c).
func newOperatorTextRig(t *testing.T) operatorTextRig {
	t.Helper()
	multi := NewMultiKBServer("test")
	var rig operatorTextRig
	for _, name := range []string{"kb-a", "kb-b", "kb-c"} {
		k := setupNamedTestKB(t, name)
		if name == "kb-a" {
			rig.kbA = k
		}
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	multi.SetKBAliases([]config.KBAlias{{From: "kb-old", To: "kb-a"}})
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatalf("EnableRoutedMount: %v", err)
	}
	rig.handler = routedTokenStore("kb-a", "kb-b", "kb-c").Middleware(multi.Handler())
	return rig
}

// everyText gathers the texts a connection on path can be shown by the
// messages D1101 reworded; the kind of connection is the path's marker.
func (r operatorTextRig) everyText(t *testing.T, path string) map[string]string {
	t.Helper()
	r.kbA.SetSettingsInfo(kb.SettingsInfo{Present: true, Findings: []string{"doctor_mode: removed setting - " + kb.DoctorKeysRemoved}})
	r.kbA.SetGitStatus("failed", &kb.ErrIdentityRejected{Branch: "main", Git: "remote: rejected", Identities: []string{"tok <t@example.test>"}})
	out := map[string]string{}
	out["tools/list"] = routedPost(t, r.handler, path, toolsListBody).Body.String()
	status := aliasCall(t, r.handler, path, "kb_status", map[string]any{"kb": "kb-a"})
	out["kb_status"] = status.Content[0].Text
	for name, kbName := range map[string]string{"mounted-not-bound": "kb-c", "unknown": "gone", "not-available": "nope"} {
		out["refusal/"+name] = aliasCall(t, r.handler, path, "concept_read", map[string]any{"kb": kbName, "id": "x"}).Content[0].Text
	}
	out["prefixed-name"] = aliasCall(t, r.handler, path, "kb_a__search", map[string]any{"kb": "kb-a"}).Content[0].Text
	return out
}

// TestGenericConnectionIsNeverToldToRunACLICommand is the D1101 guard: no text
// a connection without ?provisioned=1 receives names a `cartographer` command,
// so a new message with one fails here. The provisioned twin proves the same
// requests still carry them.
func TestGenericConnectionIsNeverToldToRunACLICommand(t *testing.T) {
	t.Parallel()
	rig := newOperatorTextRig(t)
	generic := RoutedMountPath + "?kbs=kb-old,kb-b,gone"
	for what, text := range rig.everyText(t, generic) {
		assertNoCLICommand(t, what, text)
	}
	if !strings.Contains(rig.everyText(t, generic)["kb_status"], "re-synchronizes") {
		t.Error("the generic connection hint is missing")
	}

	// Static texts: conflicts_list and the write's conflict error.
	k, sha := setupGitKB(t)
	handleConflictError(k, &gitx.RebaseConflictError{Files: []string{"data/a.md"}, LocalSHA: sha, RemoteSHA: sha, Branch: "main"})
	res, _ := toolConflictsList(k).Handler(authLocalContext(), nil)
	assertNoCLICommand(t, "conflicts_list", res.Content[0].Text)
	// Only git_conflict_resolve clears the registry; a concept_write leaves the conflict open.
	if !strings.Contains(res.Content[0].Text, "kb_guide") || !strings.Contains(res.Content[0].Text, "git_conflict_resolve") ||
		strings.Contains(res.Content[0].Text, "cartographer-kb-conflict-resolve") {
		t.Errorf("conflicts_list guidance = %s", res.Content[0].Text)
	}
	for _, msg := range []string{conflictMessage(1, nil), conflictMessage(0, []string{"data/log.md"}), conflictMessage(1, []string{"data/log.md"})} {
		assertNoCLICommand(t, "conflict error", msg)
		if strings.Contains(msg, "-kb-conflict-resolve") {
			t.Errorf("conflict error names the skill: %q", msg)
		}
	}

	// Identity rejection: the write warning and kb_status.push_last_error.
	k.SetGitStatus("failed", &kb.ErrIdentityRejected{Branch: "main", Git: "remote: rejected"})
	var warned ToolResult
	appendSyncWarning(authLocalContext(), &warned, k)
	generic1 := warned.Content[0].Text
	assertNoCLICommand(t, "write sync warning", generic1)
	var warnedOp ToolResult
	appendSyncWarning(withProvisioned(authLocalContext()), &warnedOp, k)
	if !strings.Contains(warnedOp.Content[0].Text, "cartographer kb reauthor") {
		t.Errorf("a provisioned write warning lost the command: %s", warnedOp.Content[0].Text)
	}
	// sync_status is hidden from tools/list but callable by name: same rule.
	st, _ := toolSyncStatus(k).Handler(authLocalContext(), nil)
	assertNoCLICommand(t, "sync_status", st.Content[0].Text)
	stOp, _ := toolSyncStatus(k).Handler(withProvisioned(authLocalContext()), nil)
	if !strings.Contains(stOp.Content[0].Text, "cartographer kb reauthor") {
		t.Errorf("a provisioned sync_status lost the command: %s", stOp.Content[0].Text)
	}
	if !strings.Contains(statusError(withOperator(authLocalContext()), k.GitStatusSnapshot()), "cartographer kb reauthor") {
		t.Error("the operator surface lost the full identity text")
	}

	// The removed-key note reaches every connection.
	assertNoCLICommand(t, "DoctorKeysRemoved", kb.DoctorKeysRemoved)
}

func TestProvisionedConnectionKeepsTheCLITexts(t *testing.T) {
	t.Parallel()
	rig := newOperatorTextRig(t)
	got := rig.everyText(t, provisionedPath(RoutedMountPath+"?kbs=kb-old,kb-b,gone"))
	for what, want := range map[string]string{
		"refusal/mounted-not-bound": "cartographer workspace resolve",
		"refusal/unknown":           "run cartographer sync on this client",
		"refusal/not-available":     "cartographer service restart",
		"prefixed-name":             "`cartographer doctor` flags the old names",
		"kb_status":                 "cartographer kb reauthor",
	} {
		if !strings.Contains(got[what], want) {
			t.Errorf("%s lost %q for a provisioned connection: %.400s", what, want, got[what])
		}
	}
	var s struct {
		Connection struct{ Hint string } `json:"connection"`
	}
	if err := json.Unmarshal([]byte(got["kb_status"]), &s); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{routedConnectionHint, routedUnknownHint, routedRenamedHint} {
		if !strings.Contains(s.Connection.Hint, want) {
			t.Errorf("provisioned hint %q lacks %q", s.Connection.Hint, want)
		}
	}
	// Two requests, one handler: the wording follows the request only.
	generic := rig.everyText(t, RoutedMountPath+"?kbs=kb-old,kb-b,gone")
	if strings.Contains(generic["refusal/unknown"], "cartographer sync") {
		t.Errorf("a generic request inherited a provisioned wording: %s", generic["refusal/unknown"])
	}
}

func TestGenericRefusalsKeepTheirFacts(t *testing.T) {
	t.Parallel()
	rig := newOperatorTextRig(t)
	got := rig.everyText(t, RoutedMountPath+"?kbs=kb-old,kb-b,gone")
	for what, want := range map[string]string{
		"refusal/mounted-not-bound": `kb "kb-c" is mounted on this server but is not part of this connection`,
		"refusal/unknown":           `kb "gone" is named by this connection's URL`,
		"refusal/not-available":     fmt.Sprintf("kb %q is not available on this connection", "nope"),
		"prefixed-name":             "Restart your agent session to load the new tool list",
	} {
		if !strings.Contains(got[what], want) {
			t.Errorf("%s = %.300s, want %q", what, got[what], want)
		}
	}
}
