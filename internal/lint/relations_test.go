package lint

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

func relGitIn(t *testing.T, k *kb.KB, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", k.Root, "-c", "user.name=user", "-c", "user.email=user@example.com"}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// relNode makes a KB named name whose origin is host/org/<repo> and whose
// default-branch settings.yaml declares deps (name, repo pairs).
func relNode(t *testing.T, name, repo string, deps ...[2]string) *kb.KB {
	t.Helper()
	k := tempKB(t)
	k.AuthName = name
	relGitIn(t, k, "remote", "add", "origin", "https://git.example.com/org/"+repo+".git")
	if len(deps) > 0 {
		var b strings.Builder
		b.WriteString("depends_on:\n")
		for _, d := range deps {
			fmt.Fprintf(&b, "  - {name: %s, remote: git.example.com/org/%s}\n", d[0], d[1])
		}
		if err := os.WriteFile(filepath.Join(k.Root, kb.SettingsFile), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		relGitIn(t, k, "add", kb.SettingsFile)
		relGitIn(t, k, "commit", "-q", "-m", "deps")
	}
	relGitIn(t, k, "update-ref", "refs/remotes/origin/main", "HEAD")
	relGitIn(t, k, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	return k
}

func mountAll(byName map[string]*kb.KB, order []string, aliases map[string]string) {
	m := kb.NewMounts(byName, order, aliases)
	for _, k := range byName {
		k.SetMounts(m)
	}
}

// AC-kbrel-21
func TestRelationFindings(t *testing.T) {
	t.Parallel()
	client := relNode(t, "client-kb", "client-kb",
		[2]string{"gone-kb", "gone-kb"}, [2]string{"other-kb", "other-kb"}, [2]string{"old-core", "core-kb"}, [2]string{"loop-kb", "loop-kb"})
	other := relNode(t, "other-kb", "someone-else") // mounted, another origin
	core := relNode(t, "core-kb", "core-kb")
	loop := relNode(t, "loop-kb", "loop-kb", [2]string{"client-kb", "client-kb"})
	byName := map[string]*kb.KB{"client-kb": client, "other-kb": other, "core-kb": core, "loop-kb": loop}
	mountAll(byName, []string{"client-kb", "other-kb", "core-kb", "loop-kb"}, map[string]string{"old-core": "core-kb"})

	fs, err := RelationFindings(client)
	if err != nil {
		t.Fatal(err)
	}
	var unresolved, cycles []Finding
	for _, f := range fs {
		if f.Path != kb.SettingsFile {
			t.Errorf("path %q, want settings.yaml", f.Path)
		}
		switch f.Check {
		case "kb_relation_unresolved":
			unresolved = append(unresolved, f)
		case "kb_relation_cycle":
			cycles = append(cycles, f)
		default:
			t.Errorf("unexpected check %q", f.Check)
		}
	}
	// gone-kb (not mounted) and other-kb (another origin): one finding each, the
	// same wording; the alias to core-kb resolves and says nothing.
	if len(unresolved) != 2 {
		t.Fatalf("unresolved findings: %+v", unresolved)
	}
	shape := func(f Finding) string {
		m := strings.ReplaceAll(f.Message, `"gone-kb"`, "NAME")
		m = strings.ReplaceAll(m, `"other-kb"`, "NAME")
		m = strings.ReplaceAll(m, "git.example.com/org/gone-kb", "REMOTE")
		return strings.ReplaceAll(m, "git.example.com/org/other-kb", "REMOTE")
	}
	if shape(unresolved[0]) != shape(unresolved[1]) {
		t.Errorf("not_mounted and remote_mismatch read differently: %q / %q", unresolved[0].Message, unresolved[1].Message)
	}
	if got := [][]string{unresolved[0].OtherKBs, unresolved[1].OtherKBs}; !reflect.DeepEqual(got, [][]string{{"gone-kb"}, {"other-kb"}}) {
		t.Errorf("OtherKBs = %v", got)
	}
	if len(cycles) != 1 || !reflect.DeepEqual(cycles[0].OtherKBs, []string{"loop-kb"}) || strings.Contains(cycles[0].Message, "client-kb") {
		t.Errorf("cycle findings: %+v", cycles)
	}
	// Through the alias, a KB renamed: the finding names the current name.
	mountAll(map[string]*kb.KB{"client-kb": client, "other-kb": other, "core-kb": core, "loop-kb": loop},
		[]string{"client-kb", "other-kb", "core-kb", "loop-kb"}, nil)
	fs2, err := RelationFindings(client)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs2) != len(fs)+1 {
		t.Errorf("without the alias the old name is one more unresolved relation: %d vs %d", len(fs2), len(fs))
	}
}

func TestRelationFindingsSelfAndNoDeclaration(t *testing.T) {
	t.Parallel()
	lone := relNode(t, "lone-kb", "lone-kb")
	mountAll(map[string]*kb.KB{"lone-kb": lone}, []string{"lone-kb"}, nil)
	if fs, err := RelationFindings(lone); err != nil || len(fs) != 0 {
		t.Errorf("a KB declaring nothing: %v, %v", fs, err)
	}
	me := relNode(t, "me-kb", "me-kb", [2]string{"me-kb", "me-kb"})
	mountAll(map[string]*kb.KB{"me-kb": me}, []string{"me-kb"}, nil)
	fs, err := RelationFindings(me)
	if err != nil || len(fs) != 1 || len(fs[0].OtherKBs) != 0 || fs[0].Check != "kb_relation_unresolved" {
		t.Errorf("self: %+v, %v", fs, err)
	}
}

// AC-kbrel-22
func TestRelationChecksNeverBlockAWrite(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"kb_relation_unresolved", "kb_relation_cycle"} {
		s, ok := Spec(name)
		if !ok {
			t.Fatalf("%s is not registered", name)
		}
		if s.OnWrite || s.AvoidableAtWrite || len(s.FixKinds) > 0 || s.Level != LevelKB || s.Category != CategoryKB {
			t.Errorf("%s: %+v must be a KB-level check no write gate or repair sees", name, s)
		}
	}
}

// The cycle finding names every KB on the path back, not only the target.
func TestRelationCycleNamesItsWholePath(t *testing.T) {
	t.Parallel()
	a := relNode(t, "a-kb", "a-kb", [2]string{"b-kb", "b-kb"})
	b := relNode(t, "b-kb", "b-kb", [2]string{"c-kb", "c-kb"})
	c := relNode(t, "c-kb", "c-kb", [2]string{"a-kb", "a-kb"})
	mountAll(map[string]*kb.KB{"a-kb": a, "b-kb": b, "c-kb": c}, []string{"a-kb", "b-kb", "c-kb"}, nil)
	fs, err := RelationFindings(a)
	if err != nil || len(fs) != 1 || fs[0].Check != "kb_relation_cycle" {
		t.Fatalf("%+v, %v", fs, err)
	}
	if !reflect.DeepEqual(fs[0].OtherKBs, []string{"b-kb", "c-kb"}) {
		t.Errorf("OtherKBs = %v, want the path b-kb, c-kb (this KB excluded)", fs[0].OtherKBs)
	}
}
