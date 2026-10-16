package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

const amSkillMD = "---\nname: query-rete\ndescription: Query the network\n---\n# Query\nbody\n"

// artifactMoveFixture is the two-KB fixture with artifact writes enabled on
// both sides, which is what registers the tool.
func newArtifactMoveFixture(t *testing.T) *transferFixture {
	t.Helper()
	f := newTransferFixture(t)
	for _, k := range []*kb.KB{f.a, f.b} {
		setPolicy(k, func(p *kb.Policy) { p.AllowArtifactWrite = true })
	}
	deps := Deps{KBByName: func(n string) (*kb.KB, bool) { k, ok := f.byName[n]; return k, ok }}
	f.srvA = New("test")
	RegisterKBTools(f.srvA, f.a, deps)
	return f
}

func (f *transferFixture) move(t *testing.T, args string) ToolResult {
	t.Helper()
	return f.srvA.callTool(authLocalContext(), "artifact_move", json.RawMessage(args))
}

func amWriteBytes(t *testing.T, k *kb.KB, rel string, data []byte, mode os.FileMode) {
	t.Helper()
	abs := filepath.Join(k.Root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(abs, mode); err != nil {
		t.Fatal(err)
	}
}

func amExists(k *kb.KB, rel string) bool {
	_, err := os.Lstat(filepath.Join(k.Root, filepath.FromSlash(rel)))
	return err == nil
}

func TestArtifactMove_SkillMovesVerbatimInOneCommitPerKB(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	script := []byte("#!/bin/sh\necho hi\n")
	bin := []byte{0, 1, 2, 0xff, 0xfe, 0, 'x'}
	amWriteBytes(t, f.a, "skills/query-rete/SKILL.md", []byte(amSkillMD), 0o644)
	amWriteBytes(t, f.a, "skills/query-rete/scripts/run.sh", script, 0o755)
	amWriteBytes(t, f.a, "skills/query-rete/res/blob.bin", bin, 0o644)
	amWriteBytes(t, f.a, "skills/other/SKILL.md", []byte("---\nname: other\ndescription: Stays\n---\nx\n"), 0o644)
	xferCommit(t, f.a)
	headA, headB := xferHead(t, f.a), xferHead(t, f.b)

	res := f.move(t, `{"target_kb":"kb-b","reason":"query-rete belongs to the network team","paths":["skills/query-rete"]}`)
	if res.IsError {
		t.Fatalf("artifact_move: %s", xferText(res))
	}
	for rel, want := range map[string][]byte{
		"skills/query-rete/SKILL.md":       []byte(amSkillMD),
		"skills/query-rete/scripts/run.sh": script,
		"skills/query-rete/res/blob.bin":   bin,
	} {
		got, err := os.ReadFile(filepath.Join(f.b.Root, filepath.FromSlash(rel)))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s not copied byte-identical: %v %q", rel, err, got)
		}
	}
	// Windows has no exec bit: Stat reports 0o666 and the check would be meaningless.
	if st, err := os.Stat(filepath.Join(f.b.Root, "skills/query-rete/scripts/run.sh")); err != nil || (runtime.GOOS != "windows" && st.Mode()&0o111 == 0) {
		t.Errorf("exec bit lost: %v", err)
	}
	if amExists(f.a, "skills/query-rete") {
		t.Error("source skill still exists")
	}
	if !amExists(f.a, "skills/other/SKILL.md") || !amExists(f.a, "skills") {
		t.Error("a sibling skill or the shared skills/ directory was removed")
	}
	if xferHead(t, f.a) == headA || xferHead(t, f.b) == headB {
		t.Error("each KB must get its own commit")
	}

	var out struct {
		Source struct{ KB, Commit string } `json:"source"`
		Target struct{ KB, Commit string } `json:"target"`
		Moved  []struct {
			Path  string
			Kind  string
			Files int
		} `json:"moved"`
	}
	if err := json.Unmarshal([]byte(xferText(res)), &out); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, xferText(res))
	}
	if out.Source.Commit != xferHead(t, f.a) || out.Target.Commit != xferHead(t, f.b) || len(out.Moved) != 1 || out.Moved[0].Files != 3 || out.Moved[0].Kind != "skill" {
		t.Errorf("result wrong: %+v", out)
	}
	commits, err := gitx.LogNameStatus(f.a.Root, time.Now().Add(-time.Hour))
	if err != nil || commits[0].MovedTo != "kb-b" || commits[0].Reason != "query-rete belongs to the network team" {
		t.Errorf("source commit trailers = %+v (%v)", commits[0], err)
	}
	commits, err = gitx.LogNameStatus(f.b.Root, time.Now().Add(-time.Hour))
	if err != nil || commits[0].MovedFrom != "kb-a" {
		t.Errorf("target commit trailers = %+v (%v)", commits[0], err)
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

func TestArtifactMove_SingleFileKindsAndHookDirectory(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	amWriteBytes(t, f.a, "agents/helper.md", []byte("---\nname: helper\ndescription: Helps\n---\nbody\n"), 0o644)
	amWriteBytes(t, f.a, "templates/note.md", []byte("---\ntype: Note\ntitle: \"{{title}}\"\nvariables: [title]\n---\n{{title}}\n"), 0o644)
	amWriteBytes(t, f.a, "hooks/guard/hook.json", []byte(`{"event":"SessionStart","command":"./run.sh"}`), 0o644)
	amWriteBytes(t, f.a, "hooks/guard/run.sh", []byte("#!/bin/sh\n"), 0o755)
	xferCommit(t, f.a)

	res := f.move(t, `{"target_kb":"kb-b","paths":["agents/helper.md","hooks/guard/","templates/note.md"]}`)
	if res.IsError {
		t.Fatalf("artifact_move: %s", xferText(res))
	}
	for _, rel := range []string{"agents/helper.md", "hooks/guard/hook.json", "hooks/guard/run.sh", "templates/note.md"} {
		if !amExists(f.b, rel) || amExists(f.a, rel) {
			t.Errorf("%s did not move", rel)
		}
	}
	if amExists(f.a, "hooks/guard") {
		t.Error("the emptied hook directory remains in the source")
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

func TestArtifactMove_RefusalsChangeNothing(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	amWriteBytes(t, f.a, "skills/query-rete/SKILL.md", []byte(amSkillMD), 0o644)
	amWriteBytes(t, f.a, "agents/helper.md", []byte("---\nname: helper\ndescription: Helps\n---\nbody\n"), 0o644)
	amWriteBytes(t, f.a, "instructions.md", []byte("# i\n"), 0o644)
	amWriteBytes(t, f.a, "skills/broken/SKILL.md", []byte("---\nname: other-name\ndescription: x\n---\nx\n"), 0o644)
	amWriteBytes(t, f.a, "skills/big/SKILL.md", []byte("---\nname: big\ndescription: x\n---\nx\n"), 0o644)
	amWriteBytes(t, f.a, "skills/big/blob.bin", make([]byte, artifactMaxFileSize+1), 0o644)
	amWriteBytes(t, f.a, "skills/linked/SKILL.md", []byte("---\nname: linked\ndescription: x\n---\nx\n"), 0o644)
	if err := os.Symlink("SKILL.md", filepath.Join(f.a.Root, "skills/linked/alias.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	amWriteBytes(t, f.b, "agents/helper.md", []byte("---\nname: helper\ndescription: Taken\n---\nb\n"), 0o644)
	xferCommit(t, f.a)
	xferCommit(t, f.b)
	headA, headB := xferHead(t, f.a), xferHead(t, f.b)

	cases := map[string]string{
		`{"target_kb":"kb-a","paths":["skills/query-rete"]}`:                      "artifacts are already here",
		`{"target_kb":"nope","paths":["skills/query-rete"]}`:                      "unknown target_kb",
		`{"target_kb":"kb-b","paths":[]}`:                                         "needs 1 to 20",
		`{"target_kb":"kb-b","paths":["skills/query-rete","skills/query-rete/"]}`: "listed twice",
		`{"target_kb":"kb-b","paths":["skills/query-rete/SKILL.md"]}`:             "name the artifact itself",
		`{"target_kb":"kb-b","paths":["instructions.md"]}`:                        "describes the KB it is in",
		`{"target_kb":"kb-b","paths":["paths.yaml"]}`:                             "describes the KB it is in",
		`{"target_kb":"kb-b","paths":["../etc/passwd"]}`:                          "not",
		`{"target_kb":"kb-b","paths":["skills/Not_A_Slug"]}`:                      "slug",
		`{"target_kb":"kb-b","paths":["skills/absent"]}`:                          "does not exist in",
		`{"target_kb":"kb-b","paths":["skills/query-rete","agents/helper.md"]}`:   "already exists in",
		`{"target_kb":"kb-b","paths":["skills/query-rete","skills/broken"]}`:      "does not validate",
		`{"target_kb":"kb-b","paths":["skills/big"]}`:                             "over the",
		`{"target_kb":"kb-b","paths":["skills/linked"]}`:                          "symlink",
		`{"target_kb":"kb-b","paths":["skills/query-rete\nReason: forged"]}`:      "control character",
	}
	for args, want := range cases {
		res := f.move(t, args)
		if !res.IsError || !strings.Contains(xferText(res), want) {
			t.Errorf("%s: want %q, got %q", args, want, xferText(res))
		}
	}
	if xferHead(t, f.a) != headA || xferHead(t, f.b) != headB {
		t.Error("a refused move must not commit")
	}
	if amExists(f.b, "skills/query-rete") {
		t.Error("a unit of a refused batch was written to the target")
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

func TestArtifactMove_RefusesIgnoredFiles(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	if err := os.WriteFile(filepath.Join(f.a.Root, ".gitignore"), []byte("*.dec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	amWriteBytes(t, f.a, "skills/query-rete/SKILL.md", []byte(amSkillMD), 0o644)
	xferCommit(t, f.a)
	amWriteBytes(t, f.a, "skills/query-rete/secret.dec", []byte("plain"), 0o644)
	headB := xferHead(t, f.b)

	res := f.move(t, `{"target_kb":"kb-b","paths":["skills/query-rete"]}`)
	if !res.IsError || !strings.Contains(xferText(res), "secret.dec") {
		t.Fatalf("want a refusal naming the ignored file, got: %s", xferText(res))
	}
	if xferHead(t, f.b) != headB || !amExists(f.a, "skills/query-rete/secret.dec") {
		t.Error("a refusal must leave both KBs as they were")
	}
}

// The target must opt in to artifact writes exactly as artifact_write requires,
// and a template-only KB takes templates/ and nothing else.
func TestArtifactMove_TargetNeedsTheArtifactWriteRight(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	amWriteBytes(t, f.a, "skills/query-rete/SKILL.md", []byte(amSkillMD), 0o644)
	amWriteBytes(t, f.a, "templates/note.md", []byte("---\ntype: Note\ntitle: \"{{title}}\"\nvariables: [title]\n---\n{{title}}\n"), 0o644)
	xferCommit(t, f.a)

	setPolicy(f.b, func(p *kb.Policy) { p.AllowArtifactWrite, p.AllowTemplateWrite = false, false })
	res := f.move(t, `{"target_kb":"kb-b","paths":["skills/query-rete"]}`)
	if !res.IsError || !strings.Contains(xferText(res), "does not accept artifact writes") {
		t.Errorf("target without the right: got %q", xferText(res))
	}

	setPolicy(f.b, func(p *kb.Policy) { p.AllowTemplateWrite = true })
	res = f.move(t, `{"target_kb":"kb-b","paths":["skills/query-rete"]}`)
	if !res.IsError || !strings.Contains(xferText(res), "only templates/ is writable on kb-b") {
		t.Errorf("template-only target, skill: got %q", xferText(res))
	}
	if res = f.move(t, `{"target_kb":"kb-b","paths":["templates/note.md"]}`); res.IsError {
		t.Errorf("template-only target, template: %s", xferText(res))
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

func TestArtifactMove_SourceFailureRevertsTarget(t *testing.T) {
	f := newArtifactMoveFixture(t)
	amWriteBytes(t, f.a, "skills/query-rete/SKILL.md", []byte(amSkillMD), 0o644)
	xferCommit(t, f.a)
	headA, headB := xferHead(t, f.a), xferHead(t, f.b)

	transferSourceHook = func() error { return errors.New("injected") }
	t.Cleanup(func() { transferSourceHook = nil })
	res := f.move(t, `{"target_kb":"kb-b","paths":["skills/query-rete"]}`)
	if !res.IsError || !strings.Contains(xferText(res), "artifact_move: source step failed") {
		t.Fatalf("want a source-step failure, got %q", xferText(res))
	}
	if xferHead(t, f.a) != headA || !amExists(f.a, "skills/query-rete/SKILL.md") {
		t.Error("the source must be unchanged")
	}
	if amExists(f.b, "skills/query-rete") {
		t.Error("target still holds the skill after the compensation")
	}
	out, err := gitx.LogNameStatus(f.b.Root, time.Now().Add(-time.Hour))
	if err != nil || len(out) < 2 || !strings.Contains(out[0].Subject, "revert") || xferHead(t, f.b) == headB {
		t.Fatalf("want a compensating revert commit on top, got %+v (%v)", out, err)
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

func TestArtifactMove_Policy(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	amWriteBytes(t, f.a, "skills/query-rete/SKILL.md", []byte(amSkillMD), 0o644)
	xferCommit(t, f.a)
	args := json.RawMessage(`{"target_kb":"kb-b","paths":["skills/query-rete"]}`)

	for name, tc := range map[string]struct {
		perms []auth.Permission
		want  string
	}{
		"read-only target":  {[]auth.Permission{{KB: "kb-a", Write: true}, {KB: "kb-b"}}, "read_only_token"},
		"scoped target":     {[]auth.Permission{{KB: "kb-a", Write: true}, {KB: "kb-b", Maps: []string{"notes"}, Write: true}}, "needs_whole_kb"},
		"scoped source":     {[]auth.Permission{{KB: "kb-a", Maps: []string{"notes"}, Write: true}, {KB: "kb-b", Write: true}}, "needs_whole_kb"},
		"read-only source":  {[]auth.Permission{{KB: "kb-a"}, {KB: "kb-b", Write: true}}, "read_only_token"},
		"target not in set": {[]auth.Permission{{KB: "kb-a", Write: true}}, "no_kb_access"},
	} {
		res := f.srvA.callTool(restrictedContext(auth.Policy{Permissions: tc.perms}), "artifact_move", args)
		if !res.IsError || !strings.Contains(xferText(res), tc.want) {
			t.Errorf("%s: want %q, got %q", name, tc.want, xferText(res))
		}
	}
	if !amExists(f.a, "skills/query-rete/SKILL.md") || amExists(f.b, "skills/query-rete") {
		t.Error("a denied call moved the skill")
	}

	// Positive control: whole-KB write on both sides passes.
	ok := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true}, {KB: "kb-b", Write: true}}})
	if res := f.srvA.callTool(ok, "artifact_move", args); res.IsError {
		t.Errorf("whole-KB write on both KBs was denied: %s", xferText(res))
	}
}

func TestArtifactMove_RoutedSetMustHoldTheTarget(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	amWriteBytes(t, f.a, "skills/query-rete/SKILL.md", []byte(amSkillMD), 0o644)
	xferCommit(t, f.a)
	ctx := withRoutedSet(authLocalContext(), []string{"kb-a"})
	res := f.srvA.callTool(ctx, "artifact_move", json.RawMessage(`{"target_kb":"kb-b","paths":["skills/query-rete"]}`))
	if !res.IsError || !strings.Contains(xferText(res), "not bound to this connection") {
		t.Errorf("got %q", xferText(res))
	}
}

func TestArtifactMove_NeedsAServerMountingBothKBs(t *testing.T) {
	t.Parallel()
	a, _ := setupGitKB(t)
	setPolicy(a, func(p *kb.Policy) { p.AllowArtifactWrite = true })
	s := New("test")
	RegisterKBTools(s, a, Deps{})
	res := s.callTool(authLocalContext(), "artifact_move", json.RawMessage(`{"target_kb":"kb-b","paths":["skills/x"]}`))
	if !res.IsError || !strings.Contains(xferText(res), "needs a server mounting both KBs") {
		t.Errorf("got %q", xferText(res))
	}
}

// The tool is gated like artifact_write: a KB without the right never registers it.
func TestArtifactMove_NotRegisteredWithoutTheArtifactWriteRight(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	res := f.srvA.callTool(authLocalContext(), "artifact_move", json.RawMessage(`{"target_kb":"kb-b","paths":["skills/x"]}`))
	if !res.IsError || !strings.Contains(xferText(res), "kbs[].allow_artifact_write") {
		t.Errorf("got %q", xferText(res))
	}
}

func TestArtifactMove_OppositeDirectionsDoNotDeadlock(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	srvB := New("test")
	RegisterKBTools(srvB, f.b, Deps{KBByName: func(n string) (*kb.KB, bool) { k, ok := f.byName[n]; return k, ok }})
	amWriteBytes(t, f.a, "skills/query-rete/SKILL.md", []byte(amSkillMD), 0o644)
	amWriteBytes(t, f.b, "skills/other/SKILL.md", []byte("---\nname: other\ndescription: x\n---\nx\n"), 0o644)
	xferCommit(t, f.a)
	xferCommit(t, f.b)

	done := make(chan ToolResult, 2)
	go func() {
		done <- f.srvA.callTool(authLocalContext(), "artifact_move", json.RawMessage(`{"target_kb":"kb-b","paths":["skills/query-rete"]}`))
	}()
	go func() {
		done <- srvB.callTool(authLocalContext(), "artifact_move", json.RawMessage(`{"target_kb":"kb-a","paths":["skills/other"]}`))
	}()
	for i := 0; i < 2; i++ {
		select {
		case res := <-done:
			if res.IsError {
				t.Errorf("move failed: %s", xferText(res))
			}
		case <-time.After(30 * time.Second):
			t.Fatal("deadlock: opposite moves did not finish")
		}
	}
}

// What the move cannot decide is reported and never refused: the skill is
// moved, and the caller learns what to fix on the other side.
func TestArtifactMove_ReportsWhatTravelsBadly(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	skill := "---\nname: query-rete\ndescription: Query the network\n---\nUse {{repo:declared}} and {{repo:missing}}.\n"
	amWriteBytes(t, f.a, "skills/query-rete/SKILL.md", []byte(skill), 0o644)
	amWriteBytes(t, f.a, "skills/query-rete/creds.sops.yaml", []byte("sops: {mac: ENC[AES256_GCM,data:abc]}\n"), 0o644)
	amWriteBytes(t, f.a, "skills/query-rete-2/SKILL.md", []byte("---\nname: query-rete-2\ndescription: Stays\n---\nx\n"), 0o644)
	amWriteBytes(t, f.a, "instructions.md", []byte("---\nperimeter: ops\n---\nKB a.\n"), 0o644)
	amWriteBytes(t, f.a, "data/notes/runbook.md", []byte("---\ntype: Note\ntitle: Runbook\n---\nRun skills/query-rete first, or skills/query-rete/SKILL.md.\n"), 0o644)
	amWriteBytes(t, f.a, "data/notes/other.md", []byte("---\ntype: Note\ntitle: Other\n---\nSee skills/query-rete-2 only.\n"), 0o644)
	xferCommit(t, f.a)
	amWriteBytes(t, f.b, "paths.yaml", []byte("repos:\n  declared:\n    description: a repo\n"), 0o644)
	amWriteBytes(t, f.b, "instructions.md", []byte("---\nperimeter: ops\n---\nKB b.\n"), 0o644)
	xferCommit(t, f.b)

	res := f.move(t, `{"target_kb":"kb-b","paths":["skills/query-rete"]}`)
	if res.IsError {
		t.Fatalf("artifact_move: %s", xferText(res))
	}
	var out struct {
		Placeholders []string                `json:"undeclared_placeholders"`
		Sops         []string                `json:"sops_warnings"`
		Refs         []string                `json:"references_in_source"`
		Findings     map[string][]findingOut `json:"findings_by_kb"`
	}
	if err := json.Unmarshal([]byte(xferText(res)), &out); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, xferText(res))
	}
	if len(out.Placeholders) != 1 || out.Placeholders[0] != "repo:missing" {
		t.Errorf("undeclared_placeholders = %v", out.Placeholders)
	}
	if len(out.Sops) != 1 || !strings.Contains(out.Sops[0], "skills/query-rete/creds.sops.yaml") {
		t.Errorf("sops_warnings = %v", out.Sops)
	}
	if len(out.Refs) != 1 || out.Refs[0] != "data/notes/runbook.md → skills/query-rete" {
		t.Errorf("references_in_source = %v (skills/query-rete-2 must not match)", out.Refs)
	}
	var perimeter bool
	for _, fd := range out.Findings["kb-b"] {
		if fd.Check == "skill_missing_perimeter" && fd.Path == "skills/query-rete/SKILL.md" {
			perimeter = true
		}
	}
	if !perimeter {
		t.Errorf("findings_by_kb lacks skill_missing_perimeter on the moved skill: %+v", out.Findings)
	}
	if amExists(f.a, "skills/query-rete") || !amExists(f.b, "skills/query-rete/SKILL.md") {
		t.Error("a report is not a refusal: the skill must have moved")
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

// The target has its own ignore rules: a file they exclude would stay out of the
// target commit while the source removal went ahead, so the move is refused.
func TestArtifactMove_RefusesWhatTheTargetIgnores(t *testing.T) {
	t.Parallel()
	f := newArtifactMoveFixture(t)
	amWriteBytes(t, f.a, "hooks/guard/hook.json", []byte(`{"event":"SessionStart","command":"./run.sh"}`), 0o644)
	amWriteBytes(t, f.a, "hooks/guard/run.sh", []byte("#!/bin/sh\n"), 0o755)
	xferCommit(t, f.a)
	if err := os.WriteFile(filepath.Join(f.b.Root, ".gitignore"), []byte("*.sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	xferCommit(t, f.b)
	headA, headB := xferHead(t, f.a), xferHead(t, f.b)

	res := f.move(t, `{"target_kb":"kb-b","paths":["hooks/guard"]}`)
	if !res.IsError || !strings.Contains(xferText(res), "run.sh") || !strings.Contains(xferText(res), "kb-b's ignore rules") {
		t.Fatalf("want a refusal naming the file the target ignores, got: %s", xferText(res))
	}
	if xferHead(t, f.a) != headA || xferHead(t, f.b) != headB || !amExists(f.a, "hooks/guard/run.sh") || amExists(f.b, "hooks/guard") {
		t.Error("a refusal must leave both KBs as they were")
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}
