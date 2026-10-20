package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// transferFixture is two git-backed KBs mounted on one server, as in serve.go.
type transferFixture struct {
	a, b   *kb.KB
	srvA   *Server
	byName map[string]*kb.KB
}

func newTransferFixture(t *testing.T) *transferFixture {
	t.Helper()
	a, _ := setupGitKB(t)
	b, _ := setupGitKB(t)
	a.AuthName, b.AuthName = "kb-a", "kb-b"
	f := &transferFixture{a: a, b: b, byName: map[string]*kb.KB{"kb-a": a, "kb-b": b}}
	deps := Deps{KBByName: func(n string) (*kb.KB, bool) { k, ok := f.byName[n]; return k, ok }}
	f.srvA = New("test")
	RegisterKBTools(f.srvA, a, deps)
	return f
}

func xferWrite(t *testing.T, k *kb.KB, rel, content string) {
	t.Helper()
	abs := filepath.Join(k.DataRoot(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func xferCommit(t *testing.T, k *kb.KB) {
	t.Helper()
	if _, err := k.CommitOp("fixture"); err != nil {
		t.Fatal(err)
	}
}

func xferHead(t *testing.T, k *kb.KB) string {
	t.Helper()
	sha, err := gitx.HeadSHA(k.Root)
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

// xferText is the tool's answer: its first block. A later one is the doctor's
// nudge, which a KB with debt gets since the interval is fixed (D907).
func xferText(res ToolResult) string {
	if len(res.Content) == 0 {
		return ""
	}
	return res.Content[0].Text
}

func xferClean(t *testing.T, k *kb.KB) {
	t.Helper()
	if st, _ := gitx.Status(k.Root); strings.TrimSpace(st) != "" {
		t.Errorf("working tree of %s not clean:\n%s", k.AuthName, st)
	}
}

func (f *transferFixture) transfer(t *testing.T, args string) ToolResult {
	t.Helper()
	return f.srvA.callTool(authLocalContext(), "concept_transfer", json.RawMessage(args))
}

const flatMoves = `{"target_kb":"kb-b","reason":"split the notes","moves":[{"source_id":"notes/a","target_id":"wiki/a"}]}`

func TestConceptTransfer_FlatPage(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/a.md", "---\ntype: Note\ntitle: A\n---\n# A\nbody of a\n")
	xferWrite(t, f.a, "notes/linker.md", "---\ntype: Note\ntitle: Linker\n---\nSee [A page](a.md) and [[notes/a|the alias]] and `[code](a.md)`.\n")
	xferCommit(t, f.a)
	xferWrite(t, f.b, "wiki/keep.md", "---\ntype: Note\ntitle: Keep\n---\nkeep\n")
	xferCommit(t, f.b)
	headA, headB := xferHead(t, f.a), xferHead(t, f.b)

	res := f.transfer(t, flatMoves)
	if res.IsError {
		t.Fatalf("concept_transfer: %s", xferText(res))
	}
	if _, err := os.Stat(filepath.Join(f.a.DataRoot(), "notes", "a.md")); !os.IsNotExist(err) {
		t.Error("source page still exists")
	}
	data, err := os.ReadFile(filepath.Join(f.b.DataRoot(), "wiki", "a.md"))
	if err != nil || !strings.Contains(string(data), "body of a") {
		t.Fatalf("target page missing or wrong: %v %q", err, data)
	}
	linker, _ := os.ReadFile(filepath.Join(f.a.DataRoot(), "notes", "linker.md"))
	got := string(linker)
	if !strings.Contains(got, "See A page and the alias and `[code](a.md)`.") {
		t.Errorf("inbound links not turned into plain text (code span kept):\n%s", got)
	}

	var out struct {
		Source     struct{ KB, Commit string } `json:"source"`
		Target     struct{ KB, Commit string } `json:"target"`
		Moved      []map[string]string         `json:"moved"`
		UnlinkedIn []rewrittenConcept          `json:"unlinked_in"`
	}
	if err := json.Unmarshal([]byte(xferText(res)), &out); err != nil {
		t.Fatalf("result is not JSON: %v\n%s", err, xferText(res))
	}
	if out.Source.KB != "kb-a" || out.Target.KB != "kb-b" || out.Source.Commit != xferHead(t, f.a) || out.Target.Commit != xferHead(t, f.b) {
		t.Errorf("result commits/kbs wrong: %+v (heads %s %s)", out, xferHead(t, f.a), xferHead(t, f.b))
	}
	if len(out.UnlinkedIn) != 1 || out.UnlinkedIn[0].ID != "notes/linker" || out.UnlinkedIn[0].Replacements != 2 {
		t.Errorf("unlinked_in = %+v", out.UnlinkedIn)
	}
	if xferHead(t, f.a) == headA || xferHead(t, f.b) == headB {
		t.Error("each KB must get its own commit")
	}
	xferClean(t, f.a)
	xferClean(t, f.b)

	// The trailers are read back by changes_since and concept_history.
	commits, err := gitx.LogNameStatus(f.a.Root, time.Now().Add(-time.Hour))
	if err != nil || commits[0].MovedTo != "kb-b" || commits[0].Reason != "split the notes" {
		t.Errorf("source commit trailers = %+v (%v)", commits[0], err)
	}
	srvB := New("test")
	RegisterKBTools(srvB, f.b, Deps{})
	hist := srvB.callTool(authLocalContext(), "concept_history", json.RawMessage(`{"id":"wiki/a"}`))
	if !strings.Contains(xferText(hist), `"moved_from": "kb-a"`) {
		t.Errorf("concept_history lacks moved_from:\n%s", xferText(hist))
	}
	since := srvB.callTool(authLocalContext(), "changes_since", json.RawMessage(`{"since":"`+time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)+`"}`))
	if !strings.Contains(xferText(since), "from kb-a") {
		t.Errorf("changes_since lacks the transfer:\n%s", xferText(since))
	}
}

func TestConceptTransfer_ExpandedConceptWithAssets(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/dossier/index.md", "---\ntype: Note\ntitle: Dossier\n---\n![d](diagram.png) and [sat](part.md) and [data](data.sops.yaml)\n")
	xferWrite(t, f.a, "notes/dossier/part.md", "---\ntype: Note\ntitle: Part\n---\nsatellite\n")
	bin := []byte{0, 1, 2, 0xff, 0xfe, 0, 'x', '\n'}
	if err := os.WriteFile(filepath.Join(f.a.DataRoot(), "notes", "dossier", "diagram.png"), bin, 0o644); err != nil {
		t.Fatal(err)
	}
	enc := []byte("sops: {mac: ENC[AES256_GCM,data:abc]}\n")
	xferWrite(t, f.a, "notes/dossier/data.sops.yaml", string(enc))
	xferWrite(t, f.a, "notes/dossier/.hidden", "h")
	xferCommit(t, f.a)

	res := f.transfer(t, `{"target_kb":"kb-b","moves":[{"source_id":"notes/dossier","target_id":"wiki/dossier"}]}`)
	if res.IsError {
		t.Fatalf("concept_transfer: %s", xferText(res))
	}
	for name, want := range map[string][]byte{"diagram.png": bin, "data.sops.yaml": enc, ".hidden": []byte("h")} {
		got, err := os.ReadFile(filepath.Join(f.b.DataRoot(), "wiki", "dossier", name))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s not copied byte-identical: %v %q", name, err, got)
		}
	}
	if _, err := os.Stat(filepath.Join(f.b.DataRoot(), "wiki", "dossier", "part.md")); err != nil {
		t.Errorf("satellite missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.a.DataRoot(), "notes", "dossier")); !os.IsNotExist(err) {
		t.Error("source directory still exists")
	}
	idx, _ := os.ReadFile(filepath.Join(f.b.DataRoot(), "wiki", "dossier", "index.md"))
	if !strings.Contains(string(idx), "(diagram.png)") || !strings.Contains(string(idx), "(part.md)") {
		t.Errorf("links inside the dossier were rebased to the old location:\n%s", idx)
	}
	if !strings.Contains(xferText(res), "sops-encrypted") {
		t.Errorf("no sops warning:\n%s", xferText(res))
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

func TestConceptTransfer_TargetOccupiedChangesNothing(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/a.md", "---\ntype: Note\ntitle: A\n---\na\n")
	xferWrite(t, f.a, "notes/b.md", "---\ntype: Note\ntitle: B\n---\nb\n")
	xferCommit(t, f.a)
	xferWrite(t, f.b, "wiki/b.md", "---\ntype: Note\ntitle: Taken\n---\ntaken\n")
	xferCommit(t, f.b)
	headA, headB := xferHead(t, f.a), xferHead(t, f.b)

	res := f.transfer(t, `{"target_kb":"kb-b","moves":[{"source_id":"notes/a","target_id":"wiki/a"},{"source_id":"notes/b","target_id":"wiki/b"}]}`)
	if !res.IsError || !strings.Contains(xferText(res), "target already exists") {
		t.Fatalf("want an occupied-target refusal, got %q", xferText(res))
	}
	if xferHead(t, f.a) != headA || xferHead(t, f.b) != headB {
		t.Error("a refused transfer must not commit")
	}
	if _, err := os.Stat(filepath.Join(f.b.DataRoot(), "wiki", "a.md")); !os.IsNotExist(err) {
		t.Error("the free slot was written although the batch was refused")
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

func TestConceptTransfer_SourceFailureRevertsTarget(t *testing.T) {
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/a.md", "---\ntype: Note\ntitle: A\n---\na\n")
	xferCommit(t, f.a)
	headA, headB := xferHead(t, f.a), xferHead(t, f.b)

	transferSourceHook = func() error { return errors.New("injected") }
	t.Cleanup(func() { transferSourceHook = nil })
	res := f.transfer(t, flatMoves)
	if !res.IsError || !strings.Contains(xferText(res), "source step failed") {
		t.Fatalf("want a source-step failure, got %q", xferText(res))
	}
	if xferHead(t, f.a) != headA {
		t.Error("the source must be unchanged")
	}
	if _, err := os.Stat(filepath.Join(f.a.DataRoot(), "notes", "a.md")); err != nil {
		t.Errorf("source page lost: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.b.DataRoot(), "wiki", "a.md")); !os.IsNotExist(err) {
		t.Error("target page still there after the compensation")
	}
	// Append-only history: the target commit is undone by a new commit, not reset.
	out, err := gitx.LogNameStatus(f.b.Root, time.Now().Add(-time.Hour))
	if err != nil || len(out) < 2 || !strings.Contains(out[0].Subject, "revert") {
		t.Fatalf("want a compensating revert commit on top, got %+v (%v)", out, err)
	}
	if xferHead(t, f.b) == headB {
		t.Error("the target HEAD must have moved forward (copy + revert)")
	}
	xferClean(t, f.a)
	xferClean(t, f.b)
}

func TestConceptTransfer_ReportsUndeclaredPlaceholders(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/a.md", "---\ntype: Note\ntitle: A\n---\n{{repo:declared}} {{repo:missing}} {{path:also-missing}}\n")
	xferCommit(t, f.a)
	if err := os.WriteFile(filepath.Join(f.b.Root, "paths.yaml"), []byte("repos:\n  declared:\n    description: a repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	xferCommit(t, f.b)

	res := f.transfer(t, flatMoves)
	if res.IsError {
		t.Fatalf("a placeholder must be reported, never refused: %s", xferText(res))
	}
	var out struct {
		Undeclared []string `json:"undeclared_placeholders"`
	}
	_ = json.Unmarshal([]byte(xferText(res)), &out)
	if strings.Join(out.Undeclared, ",") != "path:also-missing,repo:missing" {
		t.Errorf("undeclared_placeholders = %v", out.Undeclared)
	}
}

func TestConceptTransfer_Refusals(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/a.md", "---\ntype: Note\ntitle: A\n---\na\n")
	xferCommit(t, f.a)

	cases := map[string]string{
		`{"target_kb":"kb-a","moves":[{"source_id":"notes/a","target_id":"wiki/a"}]}`:   "target_kb is this KB",
		`{"target_kb":"nope","moves":[{"source_id":"notes/a","target_id":"wiki/a"}]}`:   "unknown target_kb",
		`{"target_kb":"kb-b","moves":[{"source_id":"notes/zzz","target_id":"wiki/a"}]}`: "read source",
	}
	for args, want := range cases {
		if res := f.transfer(t, args); !res.IsError || !strings.Contains(xferText(res), want) {
			t.Errorf("%s: want %q, got %q", args, want, xferText(res))
		}
	}

	// A symlink inside an expanded concept is refused before anything is written.
	xferWrite(t, f.a, "notes/d/index.md", "---\ntype: Note\ntitle: D\n---\nd\n")
	if err := os.Symlink("index.md", filepath.Join(f.a.DataRoot(), "notes", "d", "link.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	xferCommit(t, f.a)
	res := f.transfer(t, `{"target_kb":"kb-b","moves":[{"source_id":"notes/d","target_id":"wiki/d"}]}`)
	if !res.IsError || !strings.Contains(xferText(res), "symlink") {
		t.Errorf("symlink: got %q", xferText(res))
	}
	xferClean(t, f.b)

	// Oversized asset.
	xferWrite(t, f.a, "notes/big/index.md", "---\ntype: Note\ntitle: Big\n---\nbig\n")
	if err := os.WriteFile(filepath.Join(f.a.DataRoot(), "notes", "big", "blob.bin"), make([]byte, kb.AssetMaxFileSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	xferCommit(t, f.a)
	res = f.transfer(t, `{"target_kb":"kb-b","moves":[{"source_id":"notes/big","target_id":"wiki/big"}]}`)
	if !res.IsError || !strings.Contains(xferText(res), "cap") {
		t.Errorf("oversized asset: got %q", xferText(res))
	}
}

func TestConceptTransfer_NeedsAServerMountingBothKBs(t *testing.T) {
	t.Parallel()
	a, _ := setupGitKB(t)
	s := New("test")
	RegisterKBTools(s, a, Deps{})
	res := s.callTool(authLocalContext(), "concept_transfer", json.RawMessage(flatMoves))
	if !res.IsError || !strings.Contains(xferText(res), "needs a server mounting both KBs") {
		t.Errorf("got %q", xferText(res))
	}
}

func TestConceptTransfer_Policy(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/a.md", "---\ntype: Note\ntitle: A\n---\na\n")
	xferCommit(t, f.a)

	// Whole-KB write on the source but only read on the target.
	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true}, {KB: "kb-b"}}})
	res := f.srvA.callTool(ctx, "concept_transfer", json.RawMessage(flatMoves))
	if !res.IsError || !strings.Contains(xferText(res), "read_only_token") {
		t.Errorf("read-only target: got %q", xferText(res))
	}
	// A token scoped to a map cannot rewrite the source's inbound links.
	ctx = restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Maps: []string{"notes"}, Write: true}, {KB: "kb-b", Write: true}}})
	res = f.srvA.callTool(ctx, "concept_transfer", json.RawMessage(flatMoves))
	if !res.IsError || !strings.Contains(xferText(res), "needs_whole_kb") {
		t.Errorf("scoped source: got %q", xferText(res))
	}
	if _, err := os.Stat(filepath.Join(f.a.DataRoot(), "notes", "a.md")); err != nil {
		t.Errorf("a denied call moved the page: %v", err)
	}
}

func TestConceptTransfer_RoutedSetMustHoldTheTarget(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/a.md", "---\ntype: Note\ntitle: A\n---\na\n")
	xferCommit(t, f.a)
	ctx := withRoutedSet(authLocalContext(), []string{"kb-a"})
	res := f.srvA.callTool(ctx, "concept_transfer", json.RawMessage(flatMoves))
	if !res.IsError || !strings.Contains(xferText(res), "not bound to this connection") {
		t.Errorf("got %q", xferText(res))
	}
}

// TestConceptTransfer_OppositeDirectionsDoNotDeadlock: A→B and B→A at the same
// time take the same two locks; a global order is what keeps them from waiting
// on each other forever.
func TestConceptTransfer_OppositeDirectionsDoNotDeadlock(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	srvB := New("test")
	RegisterKBTools(srvB, f.b, Deps{KBByName: func(n string) (*kb.KB, bool) { k, ok := f.byName[n]; return k, ok }})
	xferWrite(t, f.a, "notes/a.md", "---\ntype: Note\ntitle: A\n---\na\n")
	xferWrite(t, f.b, "wiki/b.md", "---\ntype: Note\ntitle: B\n---\nb\n")
	xferCommit(t, f.a)
	xferCommit(t, f.b)

	done := make(chan ToolResult, 2)
	go func() {
		done <- f.srvA.callTool(authLocalContext(), "concept_transfer", json.RawMessage(`{"target_kb":"kb-b","moves":[{"source_id":"notes/a","target_id":"wiki/a"}]}`))
	}()
	go func() {
		done <- srvB.callTool(authLocalContext(), "concept_transfer", json.RawMessage(`{"target_kb":"kb-a","moves":[{"source_id":"wiki/b","target_id":"notes/b"}]}`))
	}()
	for i := 0; i < 2; i++ {
		select {
		case res := <-done:
			if res.IsError {
				t.Errorf("transfer failed: %s", xferText(res))
			}
		case <-time.After(30 * time.Second):
			t.Fatal("deadlock: opposite transfers did not finish")
		}
	}
}

// A wiki-link between two moved pages follows them; a page that stays and
// embeds a moved asset is reported (D760 review).
func TestConceptTransfer_LinksBetweenMovedPagesAndToMovedAssets(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/a.md", "---\ntype: Note\ntitle: A\n---\nsee [[notes/b]]\n")
	xferWrite(t, f.a, "notes/b.md", "---\ntype: Note\ntitle: B\n---\nb\n")
	xferWrite(t, f.a, "notes/dossier/index.md", "---\ntype: Note\ntitle: D\n---\n![d](diagram.png)\n")
	xferWrite(t, f.a, "notes/dossier/diagram.png", "png")
	xferWrite(t, f.a, "notes/stays.md", "---\ntype: Note\ntitle: Stays\n---\n![d](dossier/diagram.png)\n")
	xferCommit(t, f.a)

	res := f.transfer(t, `{"target_kb":"kb-b","moves":[{"source_id":"notes/a","target_id":"wiki/a"},{"source_id":"notes/b","target_id":"wiki/b"},{"source_id":"notes/dossier","target_id":"wiki/dossier"}]}`)
	if res.IsError {
		t.Fatalf("concept_transfer: %s", xferText(res))
	}
	a, _ := os.ReadFile(filepath.Join(f.b.DataRoot(), "wiki", "a.md"))
	if !strings.Contains(string(a), "[[wiki/b]]") {
		t.Errorf("wiki-link between moved pages not renamed:\n%s", a)
	}
	if !strings.Contains(xferText(res), "notes/stays links to the moved asset notes/dossier/diagram.png") {
		t.Errorf("dangling asset link not reported:\n%s", xferText(res))
	}
}

// A gitignored file inside an expanded concept refuses the transfer: the target
// would commit it, and the source would lose it beyond git's reach.
func TestConceptTransfer_RefusesIgnoredFiles(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	if err := os.WriteFile(filepath.Join(f.a.Root, ".gitignore"), []byte("*.dec\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	xferWrite(t, f.a, "notes/dossier/index.md", "---\ntype: Note\ntitle: D\n---\nd\n")
	xferCommit(t, f.a)
	xferWrite(t, f.a, "notes/dossier/secret.dec", "plain")
	headB := xferHead(t, f.b)

	res := f.transfer(t, `{"target_kb":"kb-b","moves":[{"source_id":"notes/dossier","target_id":"wiki/dossier"}]}`)
	if !res.IsError || !strings.Contains(xferText(res), "secret.dec") {
		t.Fatalf("want a refusal naming the ignored file, got: %s", xferText(res))
	}
	if xferHead(t, f.b) != headB {
		t.Error("target changed on refusal")
	}
	if _, err := os.Stat(filepath.Join(f.a.DataRoot(), "notes", "dossier", "secret.dec")); err != nil {
		t.Error("ignored file must stay in the source")
	}
}

// TestConceptTransfer_RefusesFilesTheTargetIgnores: a file the target's
// .gitignore excludes would miss the target commit and be deleted from the
// source, so the transfer is refused before either KB changes (#873).
func TestConceptTransfer_RefusesFilesTheTargetIgnores(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	if err := os.WriteFile(filepath.Join(f.b.Root, ".gitignore"), []byte("*.csv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	xferCommit(t, f.b)
	xferWrite(t, f.a, "notes/report/index.md", "---\ntype: Note\ntitle: R\n---\nr\n")
	xferWrite(t, f.a, "notes/report/assets/data.csv", "a,b\n")
	xferCommit(t, f.a)
	headA, headB := xferHead(t, f.a), xferHead(t, f.b)

	res := f.transfer(t, `{"target_kb":"kb-b","moves":[{"source_id":"notes/report","target_id":"wiki/report"}]}`)
	if !res.IsError || !strings.Contains(xferText(res), "assets/data.csv") {
		t.Fatalf("want a refusal naming the file the target ignores, got: %s", xferText(res))
	}
	if xferHead(t, f.a) != headA || xferHead(t, f.b) != headB {
		t.Error("a KB changed on refusal")
	}
	if _, err := os.Stat(filepath.Join(f.b.DataRoot(), "wiki", "report")); !os.IsNotExist(err) {
		t.Errorf("nothing may be written in the target, stat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.a.DataRoot(), "notes", "report", "assets", "data.csv")); err != nil {
		t.Error("the file must stay in the source")
	}
}

// TestConceptTransfer_TargetAuthorizesEverySatellite: the destination slot of
// each satellite is checked with that satellite's own type (#783).
func TestConceptTransfer_TargetAuthorizesEverySatellite(t *testing.T) {
	t.Parallel()
	f := newTransferFixture(t)
	xferWrite(t, f.a, "notes/dossier/index.md", "---\ntype: Entity\ntitle: Dossier\n---\nbody\n")
	xferWrite(t, f.a, "notes/dossier/part.md", "---\ntype: Topic\ntitle: Part\n---\nsat\n")
	xferWrite(t, f.a, "notes/flat.md", "---\ntype: Entity\ntitle: Flat\n---\nflat\n")
	expanded := []conceptMoveEntry{{SourceID: "notes/dossier", TargetID: "wiki/dossier"}}
	flat := []conceptMoveEntry{{SourceID: "notes/flat", TargetID: "wiki/flat"}}

	ctx := restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true}, {KB: "kb-b", Write: true, Types: []string{"Entity"}}}})
	if err := authorizeTransferTarget(ctx, f.a, f.b, "kb-b", expanded); err == nil {
		t.Error("a satellite of a type the token may not write on the target was transferred")
	}
	if err := authorizeTransferTarget(ctx, f.a, f.b, "kb-b", flat); err != nil {
		t.Errorf("flat concept denied: %v", err)
	}
	ctx = restrictedContext(auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true}, {KB: "kb-b", Write: true, Types: []string{"Entity", "Topic"}}}})
	if err := authorizeTransferTarget(ctx, f.a, f.b, "kb-b", expanded); err != nil {
		t.Errorf("every type allowed, still denied: %v", err)
	}
}
