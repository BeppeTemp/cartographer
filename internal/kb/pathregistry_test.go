package kb

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const validRegistry = `paths:
  claude-home:
    description: Claude Code's per-user directory
    default: ~/.claude
  ssh-config: {description: the ssh client config, default: $HOME/.ssh/config}
  scratch: {description: a scratch area with no default}
repos:
  kb-tools:
    description: the tools repository
    remote: git@gitlab.example.com:group/kb-tools.git
    default: ~/src/kb-tools
  other: {description: another repo, remote: example.com/owner/other}
`

func TestParsePathRegistry_Valid(t *testing.T) {
	reg, bad, err := ParsePathRegistry([]byte(validRegistry))
	if err != nil || len(bad) != 0 {
		t.Fatalf("err=%v bad=%v", err, bad)
	}
	if got := reg.Paths["claude-home"]; got.Default != "~/.claude" || got.Description == "" {
		t.Errorf("claude-home = %+v", got)
	}
	if got := reg.Repos["kb-tools"].Remote; got != "gitlab.example.com/group/kb-tools" {
		t.Errorf("remote not normalized: %q", got)
	}
	if got := reg.Repos["other"].Remote; got != "example.com/owner/other" {
		t.Errorf("canonical remote: %q", got)
	}
	want := []string{"path:claude-home", "path:scratch", "path:ssh-config", "repo:kb-tools", "repo:other"}
	if got := reg.IDs(); !reflect.DeepEqual(got, want) {
		t.Errorf("IDs = %v, want %v", got, want)
	}
	if err := ValidatePathRegistry([]byte(validRegistry)); err != nil {
		t.Errorf("strict validation rejected a valid file: %v", err)
	}
}

func TestParsePathRegistry_EmptyFile(t *testing.T) {
	for _, in := range []string{"", "\n", "# only a comment\n"} {
		reg, bad, err := ParsePathRegistry([]byte(in))
		if err != nil || len(bad) != 0 || !reg.IsEmpty() {
			t.Errorf("%q: reg=%+v bad=%v err=%v", in, reg, bad, err)
		}
	}
}

// Every malformed shape is rejected by the strict form with a message naming
// the entry, and left out (not fatal) by the tolerant one.
func TestParsePathRegistry_Malformed(t *testing.T) {
	cases := []struct {
		name, yaml, entry, want string
	}{
		{"unknown field", "paths:\n  claude-home: {description: d, defualt: ~/.claude}\n", "paths.claude-home", `unknown field "defualt"`},
		{"remote on a path", "paths:\n  claude-home: {description: d, remote: example.com/o/n}\n", "paths.claude-home", `unknown field "remote"`},
		{"missing description", "paths:\n  claude-home: {default: ~/.claude}\n", "paths.claude-home", "description is required"},
		{"absolute default", "paths:\n  claude-home: {description: d, default: /Users/x/.claude}\n", "paths.claude-home", "must start with ~/ or $HOME/"},
		{"relative default", "paths:\n  claude-home: {description: d, default: .claude}\n", "paths.claude-home", "must start with ~/ or $HOME/"},
		{"other user's home", "paths:\n  claude-home: {description: d, default: ~bob/.claude}\n", "paths.claude-home", "must start with ~/ or $HOME/"},
		{"dotdot default", "paths:\n  claude-home: {description: d, default: ~/../etc}\n", "paths.claude-home", "must not contain a .. segment"},
		{"bad slug", "paths:\n  Claude_Home: {description: d}\n", "paths.Claude_Home", "slug"},
		{"bad remote", "repos:\n  kb-tools: {description: d, remote: not-a-remote}\n", "repos.kb-tools", `remote "not-a-remote"`},
		{"unknown section", "things:\n  a: {description: d}\n", "things", "unknown top-level field"},
		{"not a mapping", "paths:\n  claude-home: ~/.claude\n", "paths.claude-home", "must be a mapping"},
		{"non-string field", "paths:\n  claude-home: {description: [a, b]}\n", "paths.claude-home", `field "description" must be a string`},
		{"duplicate key", "paths:\n  a: {description: d}\n  a: {description: e}\n", "paths.a", "declared twice"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A valid sibling survives the tolerant parse.
			in := tc.yaml
			if !strings.HasPrefix(in, "paths:") {
				in = "paths:\n  ok-key: {description: fine}\n" + in
			} else {
				in = strings.Replace(in, "paths:\n", "paths:\n  ok-key: {description: fine}\n", 1)
			}
			reg, bad, err := ParsePathRegistry([]byte(in))
			if err != nil {
				t.Fatalf("tolerant parse failed: %v", err)
			}
			if len(bad) != 1 || bad[0].Entry != tc.entry || !strings.Contains(bad[0].Reason, tc.want) {
				t.Fatalf("bad = %+v, want entry %q reason ~ %q", bad, tc.entry, tc.want)
			}
			if _, ok := reg.Paths["ok-key"]; !ok {
				t.Errorf("valid sibling dropped: %+v", reg)
			}
			err = ValidatePathRegistry([]byte(in))
			if err == nil || !strings.Contains(err.Error(), tc.entry) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("strict: %v, want it to name %q and %q", err, tc.entry, tc.want)
			}
		})
	}
}

func TestParsePathRegistry_NotAMapping(t *testing.T) {
	for _, in := range []string{"- a\n- b\n", "paths: [\n"} {
		if _, _, err := ParsePathRegistry([]byte(in)); err == nil {
			t.Errorf("%q: want an error", in)
		}
		if err := ValidatePathRegistry([]byte(in)); err == nil {
			t.Errorf("%q: strict want an error", in)
		}
	}
}

func TestReadPathRegistry(t *testing.T) {
	dir := tempKB(t)
	k := &KB{Root: dir}
	st, err := k.ReadPathRegistry()
	if err != nil || st.Present {
		t.Fatalf("absent: %+v %v", st, err)
	}
	if err := os.WriteFile(filepath.Join(dir, PathRegistryFile), []byte(validRegistry+"  bad-one: {default: /abs}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err = k.ReadPathRegistry()
	if err != nil || !st.Present || st.Unparseable || len(st.Registry.Repos) != 3-1 || len(st.Malformed) != 1 {
		t.Fatalf("present: %+v %v", st, err)
	}
	if err := os.WriteFile(filepath.Join(dir, PathRegistryFile), []byte("- a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err = k.ReadPathRegistry()
	if err != nil || !st.Present || !st.Unparseable || len(st.Malformed) != 1 {
		t.Fatalf("unparseable: %+v %v", st, err)
	}
}

func TestReadPathRegistry_RefusesSymlink(t *testing.T) {
	dir := tempKB(t)
	target := filepath.Join(t.TempDir(), "elsewhere.yaml")
	if err := os.WriteFile(target, []byte(validRegistry), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, PathRegistryFile)); err != nil {
		t.Skipf("symlink: %v", err)
	}
	if _, err := (&KB{Root: dir}).ReadPathRegistry(); err == nil {
		t.Fatal("a symlinked paths.yaml was read")
	}
}

func TestArtifactPlaceholders(t *testing.T) {
	dir := tempKB(t)
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("skills/a/SKILL.md", "see {{path:claude-home}} and {{\\path:escaped}}")
	write("instructions.md", "repo at {{repo:kb-tools}}")
	write("templates/t.md", "{{path:not-scanned}}")
	write("data/m/c.md", "{{path:concept-only}}")
	got, err := (&KB{Root: dir}).ArtifactPlaceholders()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"path:claude-home", "repo:kb-tools"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
