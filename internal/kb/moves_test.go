package kb

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

func TestConceptIDOfGitPath(t *testing.T) {
	t.Parallel()
	for p, want := range map[string]okf.ConceptID{
		"data/ops/net.md":            "ops/net",
		"data/ops/dossier/index.md":  "ops/dossier",
		"data/ops/dossier/child.md":  "ops/dossier/child",
		"data/root-concept.md":       "root-concept",
		"data/ops/index.md":          "",
		"data/ops/_map.md":           "",
		"data/log.md":                "",
		"data/ops/asset.png":         "",
		"skills/x/SKILL.md":          "",
		"data/ops/Not_Kebab Case.md": "",
	} {
		got, ok := conceptIDOfGitPath(p)
		if got != want || ok != (want != "") {
			t.Errorf("%s = %q, %v; want %q", p, got, ok, want)
		}
	}
}

func TestRecordedMoves_CachedUntilHeadChanges(t *testing.T) {
	k := newTestKB(t)
	write := func(rel string) {
		p := filepath.Join(k.DataRoot(), rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("---\ntype: Note\ntitle: T\n---\nbody long enough for git to detect the rename of "+rel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("ops/a.md")
	write("ops/dossier/index.md")
	if err := gitx.Commit(k.Root, "setup", "user", "user@example.test"); err != nil {
		t.Fatal(err)
	}
	mv := func(from, to, subject string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(k.DataRoot(), to)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(k.DataRoot(), from), filepath.Join(k.DataRoot(), to)); err != nil {
			t.Fatal(err)
		}
		if err := gitx.Commit(k.Root, subject, "user", "user@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	mv("ops/a.md", "ops/b.md", "concept_move: ops/a")
	first, err := k.RecordedMoves()
	if err != nil || len(first) != 1 || first[0] != (Move{From: "ops/a", To: "ops/b"}) {
		t.Fatalf("first = %+v, %v", first, err)
	}
	again, _ := k.RecordedMoves()
	if &again[0] != &first[0] {
		t.Error("the parse was not reused at the same HEAD")
	}
	if k.movesCache.full != 1 || k.movesCache.incremental != 0 {
		t.Fatalf("first read: full=%d incremental=%d", k.movesCache.full, k.movesCache.incremental)
	}
	mv("ops/dossier/index.md", "ops/folder/index.md", "concept_move: ops/dossier")
	next, err := k.RecordedMoves()
	if err != nil || len(next) != 2 || next[1] != (Move{From: "ops/dossier", To: "ops/folder"}) {
		t.Fatalf("after a new move = %+v, %v", next, err)
	}
	// A fast-forward costs only the commits since the cached HEAD.
	if k.movesCache.full != 1 || k.movesCache.incremental != 1 {
		t.Errorf("fast-forward: full=%d incremental=%d, want 1 and 1", k.movesCache.full, k.movesCache.incremental)
	}
	// A HEAD that does not descend from the cached one (a rewrite) reads the
	// bounded history again, and the moves of the abandoned commits are gone.
	if out, err := exec.Command("git", "-C", k.Root, "reset", "--hard", "HEAD~2").CombinedOutput(); err != nil {
		t.Fatalf("reset: %v: %s", err, out)
	}
	write("ops/other.md")
	if err := gitx.Commit(k.Root, "other", "user", "user@example.test"); err != nil {
		t.Fatal(err)
	}
	rewritten, err := k.RecordedMoves()
	if err != nil || len(rewritten) != 0 {
		t.Fatalf("after a rewrite = %+v, %v", rewritten, err)
	}
	if k.movesCache.full != 2 {
		t.Errorf("rewrite: full=%d, want 2", k.movesCache.full)
	}
}

// A directory move carries the satellites of an expanded concept: the IDs are
// the concept's and each satellite's, in the order git lists the renames.
func TestRecordedMoves_ExpandedConceptDirectoryMove(t *testing.T) {
	k := newTestKB(t)
	for _, rel := range []string{"ops/dossier/index.md", "ops/dossier/sub.md"} {
		p := filepath.Join(k.DataRoot(), rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("---\ntype: Note\ntitle: T\n---\nbody long enough for git to detect the rename of "+rel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := gitx.Commit(k.Root, "setup", "user", "user@example.test"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(k.DataRoot(), "ops/dossier"), filepath.Join(k.DataRoot(), "ops/folder")); err != nil {
		t.Fatal(err)
	}
	if err := gitx.Commit(k.Root, "concept_move: ops/dossier", "user", "user@example.test"); err != nil {
		t.Fatal(err)
	}
	moves, err := k.RecordedMoves()
	if err != nil || len(moves) != 2 {
		t.Fatalf("moves = %+v, %v", moves, err)
	}
	got := map[Move]bool{moves[0]: true, moves[1]: true}
	for _, want := range []Move{{From: "ops/dossier", To: "ops/folder"}, {From: "ops/dossier/sub", To: "ops/folder/sub"}} {
		if !got[want] {
			t.Errorf("missing %+v in %+v", want, moves)
		}
	}
}

func TestResolveMoves(t *testing.T) {
	t.Parallel()
	ex := func(ids ...okf.ConceptID) func(okf.ConceptID) bool {
		return func(id okf.ConceptID) bool {
			for _, e := range ids {
				if e == id {
					return true
				}
			}
			return false
		}
	}
	chain := func(n int) []Move {
		var m []Move
		for i := 0; i < n; i++ {
			m = append(m, Move{From: okf.ConceptID("m/c" + string(rune('a'+i))), To: okf.ConceptID("m/c" + string(rune('a'+i+1)))})
		}
		return m
	}
	cases := []struct {
		name  string
		moves []Move
		ex    func(okf.ConceptID) bool
		want  map[okf.ConceptID]okf.ConceptID
	}{
		{"chain", []Move{{"m/a", "m/b"}, {"m/b", "m/c"}}, ex("m/c"), map[okf.ConceptID]okf.ConceptID{"m/a": "m/c", "m/b": "m/c"}},
		{"target deleted", []Move{{"m/a", "m/b"}}, ex(), map[okf.ConceptID]okf.ConceptID{}},
		{"loop", []Move{{"m/a", "m/b"}, {"m/b", "m/a"}}, ex(), map[okf.ConceptID]okf.ConceptID{}},
		{"loop, one exists", []Move{{"m/a", "m/b"}, {"m/b", "m/a"}}, ex("m/a"), map[okf.ConceptID]okf.ConceptID{"m/b": "m/a"}},
		{"source recreated", []Move{{"m/a", "m/b"}}, ex("m/a", "m/b"), map[okf.ConceptID]okf.ConceptID{}},
		{"too deep", chain(20), ex("m/cu"), map[okf.ConceptID]okf.ConceptID{"m/ce": "m/cu", "m/cf": "m/cu", "m/cg": "m/cu", "m/ch": "m/cu", "m/ci": "m/cu", "m/cj": "m/cu", "m/ck": "m/cu", "m/cl": "m/cu", "m/cm": "m/cu", "m/cn": "m/cu", "m/co": "m/cu", "m/cp": "m/cu", "m/cq": "m/cu", "m/cr": "m/cu", "m/cs": "m/cu", "m/ct": "m/cu"}},
		{"two sources into one target", []Move{{"m/a", "m/c"}, {"m/b", "m/c"}}, ex("m/c"), map[okf.ConceptID]okf.ConceptID{"m/a": "m/c", "m/b": "m/c"}},
		{"later move of a source overrides", []Move{{"m/a", "m/b"}, {"m/a", "m/c"}}, ex("m/b", "m/c"), map[okf.ConceptID]okf.ConceptID{"m/a": "m/c"}},
	}
	for _, c := range cases {
		got := ResolveMoves(c.moves, c.ex, 16)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Errorf("%s: %s = %q, want %q", c.name, k, got[k], v)
			}
		}
	}
}
