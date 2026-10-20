package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// repairTestKB serves a KB with one nonstandard_field finding (`updated` for
// `timestamp`) through an in-process server, and returns a caller for it.
func repairTestKB(t *testing.T) (toolCaller, string) {
	t.Helper()
	k := kbtest.New(t)
	if err := k.CreateMapWithContract("ops", "Ops", "map", nil, kb.MapContract{}); err != nil {
		t.Fatal(err)
	}
	fm, _ := okf.ParseFrontmatter("")
	fm.Set("type", "Note")
	fm.Set("title", "First")
	fm.Set("updated", "2026-01-02")
	if _, err := k.WriteConcept("ops/first", fm, "# First\n", ""); err != nil {
		t.Fatal(err)
	}
	concept := filepath.Join(k.DataRoot(), "ops", "first.md")
	s := mcpserver.New("test")
	mcpserver.RegisterKBTools(s, k, mcpserver.Deps{})
	h := s.HTTPHandler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.ContextWithPrincipal(r.Context(), auth.Principal{ID: "test", Policy: auth.Policy{Admin: true}})
		h.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(srv.Close)
	return targetCaller{c: client.New(srv.URL+"/mcp", "")}, concept
}

func runRepairJSON(t *testing.T, c toolCaller, apply bool) (kbRepairReport, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runKBRepair(c, apply, true, &out, &errOut)
	var rep kbRepairReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("report: %v (stdout %q, stderr %q)", err, out.String(), errOut.String())
	}
	return rep, code
}

func checkLine(rep kbRepairReport, check string) kbRepairCheck {
	for _, dc := range rep.Checks {
		if dc.Check == check {
			return dc
		}
	}
	return kbRepairCheck{}
}

func TestKBRepairDryRunWritesNothing(t *testing.T) {
	c, concept := repairTestKB(t)
	before, err := os.ReadFile(concept)
	if err != nil || !bytes.Contains(before, []byte("updated:")) {
		t.Fatalf("fixture: %v %q", err, before)
	}
	rep, code := runRepairJSON(t, c, false)
	if code != kbRepairExitJudgement {
		t.Fatalf("exit = %d, want %d (debt remains)", code, kbRepairExitJudgement)
	}
	if dc := checkLine(rep, "nonstandard_field"); dc.Planned != 1 || dc.Applied != 0 || !dc.Auto {
		t.Fatalf("nonstandard_field = %+v", dc)
	}
	if after, _ := os.ReadFile(concept); !bytes.Equal(before, after) {
		t.Fatal("a dry run changed the concept")
	}
}

func TestKBRepairApplyRunsOnlyTheSafeChecks(t *testing.T) {
	c, concept := repairTestKB(t)
	rep, code := runRepairJSON(t, c, true)
	if dc := checkLine(rep, "nonstandard_field"); dc.Applied != 1 {
		t.Fatalf("nonstandard_field = %+v, want 1 applied", dc)
	}
	for _, dc := range rep.Checks {
		if !lint.AutoRepairSafe(dc.Check) && dc.Applied != 0 {
			t.Fatalf("%s applied but is not safe to run unattended: %+v", dc.Check, dc)
		}
	}
	after, _ := os.ReadFile(concept)
	if !strings.Contains(string(after), "timestamp:") || strings.Contains(string(after), "updated:") {
		t.Fatalf("concept not repaired:\n%s", after)
	}
	if code != kbRepairExitClean || rep.Remaining != 0 {
		t.Fatalf("exit = %d, remaining = %d, want a clean KB", code, rep.Remaining)
	}
	// A mechanical pass is not a doctor session (D290): it must not move
	// last_doctor, or it would silence the doctor proposal for an interval.
	log, _ := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(concept)), "log.md"))
	if strings.Contains(string(log), "kb-doctor") {
		t.Fatalf("kb doctor wrote the kb-doctor marker:\n%s", log)
	}
}

// The server nudges a writer once a day toward a kb-doctor session (D299),
// but never the CLI: client.Call would hand every command a JSON array.
func TestKBRepairCLIIsNeverNudged(t *testing.T) {
	c, _ := repairTestKB(t)
	for i := 0; i < 2; i++ {
		raw, err := c.Invoke("search", map[string]any{"query": "First"})
		if err != nil {
			t.Fatal(err)
		}
		var blocks []string
		if json.Unmarshal(raw, &blocks) == nil {
			t.Fatalf("the CLI received %d blocks: %q", len(blocks), blocks)
		}
	}
}

func TestCallIntoTakesTheFirstBlock(t *testing.T) {
	var v struct{ A int }
	two := fakeCaller(`["{\"A\":1}","cartographer: notice"]`)
	if err := callInto(two, "x", nil, &v); err != nil || v.A != 1 {
		t.Fatalf("v = %+v, err = %v", v, err)
	}
}

type fakeCaller string

func (f fakeCaller) Invoke(string, any) (json.RawMessage, error) { return json.RawMessage(f), nil }

// --apply runs every safe check (D355, D907): no setting narrows the list.
func TestKBRepairApplyRunsEverySafeCheck(t *testing.T) {
	c, concept := repairTestKB(t)
	rep, code := runRepairJSON(t, c, true)
	if dc := checkLine(rep, "nonstandard_field"); dc.Applied != 1 {
		t.Fatalf("nonstandard_field = %+v, want 1 applied", dc)
	}
	if code != kbRepairExitClean {
		t.Fatalf("exit = %d, want a clean KB", code)
	}
	for _, check := range lint.AutoRepairChecks() {
		if !checkLine(rep, check).Auto {
			t.Errorf("%s is safe to run unattended but not marked auto", check)
		}
	}
	for _, check := range []string{"broken_link", "reciprocal_link_item"} {
		if checkLine(rep, check).Auto {
			t.Errorf("%s drops or rewrites links and must not be safe to run unattended", check)
		}
	}
	if after, _ := os.ReadFile(concept); strings.Contains(string(after), "updated:") {
		t.Fatalf("concept not repaired:\n%s", after)
	}
}

func TestKBRepairReportsTheSafeList(t *testing.T) {
	var out bytes.Buffer
	printKBRepairReport(&out, kbRepairReport{KB: "kb-a", AutoRepair: lint.AutoRepairChecks()})
	if !strings.Contains(out.String(), fmt.Sprintf("--apply applies the %d checks that are safe to run unattended", len(lint.AutoRepairChecks()))) {
		t.Fatalf("report:\n%s", out.String())
	}
}

// A repair is a write, so its commit is authored as whoever runs it (D904),
// not as the token.
func TestKBRepairCommitIsAuthoredAsTheWriter(t *testing.T) {
	c, concept := repairTestKB(t)
	cfg := clientconfig.Default()
	cfg.ServerURL = c.(targetCaller).c.ServerURL
	cfg.Identities = map[string]string{clientconfig.IdentityDefault: "Ada Lovelace <ada@example.com>"}
	var errOut bytes.Buffer
	if _, code := runRepairJSON(t, repairCaller(cfg, "", kbTarget{}, "", &errOut), true); code != kbRepairExitClean {
		t.Fatalf("exit = %d; stderr %s", code, errOut.String())
	}
	if got := gitOut(t, filepath.Dir(concept), "log", "-1", "--format=%an <%ae>"); got != "Ada Lovelace <ada@example.com>" {
		t.Fatalf("repair commit author = %q", got)
	}
}

// The CLI follows the bridge's rule: an identities entry decides, even an
// empty or invalid one, and only without one is git asked, in the folder the
// command runs in.
func TestCLIAuthorIdentitiesDecideBeforeGit(t *testing.T) {
	if !hasGit() {
		t.Skip("git not in PATH")
	}
	repo := t.TempDir()
	gitOut(t, repo, "init", "-q")
	gitOut(t, repo, "config", "user.name", "Repo Writer")
	gitOut(t, repo, "config", "user.email", "repo@example.com")
	for _, tc := range []struct {
		name, dir  string
		identities map[string]string
		want       string
		warns      bool
	}{
		{name: "git in the folder", dir: repo, want: "Repo Writer <repo@example.com>"},
		{name: "no work tree", dir: t.TempDir()},
		{name: "the KB's entry", dir: repo, identities: map[string]string{"kb-a": "Ada <ada@example.com>"}, want: "Ada <ada@example.com>"},
		{name: "another KB's entry", dir: repo, identities: map[string]string{"kb-b": "Ada <ada@example.com>"}, want: "Repo Writer <repo@example.com>"},
		{name: "an empty entry opts out", dir: repo, identities: map[string]string{"kb-a": ""}},
		{name: "an invalid entry", dir: repo, identities: map[string]string{clientconfig.IdentityDefault: "Ada"}, warns: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := clientconfig.Default()
			cfg.Identities = tc.identities
			var errOut bytes.Buffer
			if got := cliAuthor(cfg, "kb-a", tc.dir, &errOut); got != tc.want {
				t.Fatalf("author = %q, want %q", got, tc.want)
			}
			if warned := strings.Contains(errOut.String(), "identities entry"); warned != tc.warns {
				t.Fatalf("warning = %q", errOut.String())
			}
		})
	}
}
