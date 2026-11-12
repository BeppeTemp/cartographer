package settingschema

import (
	"reflect"
	"strings"
	"testing"
)

var unknownSchema = Schema{Keys: []Key{
	{Path: "name", Scope: ScopeServer, Type: "string", Default: `""`},
	{Path: "server.url", Scope: ScopeClient, Type: "string", Default: `""`},
	{Path: "server.auth", Scope: ScopeClient, Type: "bool", Default: "false"},
	{Path: "secret_hook", Scope: ScopeServer, Type: "string", Default: `""`, Hidden: true},
	{Path: "providers", Scope: ScopeClient, Type: "map", Default: "{}"},
	{Path: "kbs", Scope: ScopeServer, Type: "list", Default: "[]"},
	{Path: "kbs[].remote", Scope: ScopeServer, Type: "string", Default: `""`},
	{Path: "kbs[].branch", Scope: ScopeServer, Type: "string", Default: `""`},
	{Path: "plugins", Scope: ScopeServer, Type: "map", Default: "{}"},
	{Path: "plugins.{name}.digest", Scope: ScopeServer, Type: "string", Default: `""`},
	{Path: "plugins.{name}.enabled", Scope: ScopeServer, Type: "bool", Default: "true"},
}}

var unknownRegistry = Registry{
	Removed: []Removed{{Path: "kbs[].git_profile", Replacement: "branch"}, {Path: "old_flag"}},
	Moved:   []Moved{{From: "addr", To: "server.url"}, {From: "scopes.*", To: "providers.*.scope"}},
}

func unknownPaths(t *testing.T, in string) []Unknown {
	t.Helper()
	return unknownSchema.Unknown([]byte(in), unknownRegistry)
}

// A file that holds only what the table describes (hidden keys, a map whose
// names are the operator's, the {name} entries of a map, the entries of a list)
// has no unknown key.
func TestUnknownKnownKeysAreSilent(t *testing.T) {
	in := `
name: a
secret_hook: x
server: {url: http://example.com, auth: true}
providers:
  claude: {anything: [1, 2], at: all}
kbs:
  - {remote: https://example.com/a.git, branch: b}
plugins:
  team: {digest: abc, enabled: false}
`
	if got := unknownPaths(t, in); len(got) != 0 {
		t.Errorf("unknown = %+v, want none", got)
	}
}

// A typo is reported with its place and the nearest known key at that level,
// at the top, inside a mapping, in a list entry and under a {name} entry.
func TestUnknownReportsTyposWithAHint(t *testing.T) {
	in := `nmae: a
server:
  ulr: http://example.com
kbs:
  - remote: https://example.com/a.git
  - remtoe: https://example.com/b.git
plugins:
  team:
    digset: abc
`
	got := unknownPaths(t, in)
	var lines []string
	for _, u := range got {
		lines = append(lines, u.Path+"|"+u.Hint)
	}
	want := []string{"nmae|name", "server.ulr|server.url", "kbs[1].remtoe|kbs[1].remote", "plugins.team.digset|plugins.team.digest"}
	// The hint of a list entry or a {name} entry is the key's own path there.
	if !reflect.DeepEqual(lines, want) {
		t.Errorf("unknown = %v, want %v", lines, want)
	}
	if got[0].Line != 1 || got[1].Line != 3 || got[1].Column != 3 {
		t.Errorf("positions = %+v", got)
	}
	if msg := got[0].String(); !strings.Contains(msg, "nmae") || !strings.Contains(msg, "did you mean name?") {
		t.Errorf("String() = %q", msg)
	}
}

// A key far from every known one has no hint, and still is reported.
func TestUnknownWithoutAClose(t *testing.T) {
	got := unknownPaths(t, "completely_different: 1\n")
	if len(got) != 1 || got[0].Hint != "" {
		t.Errorf("unknown = %+v, want one without a hint", got)
	}
}

// A retired key has its own message; a parent of a retired path is not unknown
// either (scopes of scopes.<provider>).
func TestUnknownLeavesRetiredKeysAlone(t *testing.T) {
	in := "old_flag: true\naddr: http://example.com\nscopes:\n  claude: workspace\nkbs:\n  - {remote: r, git_profile: p}\n"
	if got := unknownPaths(t, in); len(got) != 0 {
		t.Errorf("unknown = %+v, want none", got)
	}
}

// A key supplied through a merge key is seen like a written one, and an alias
// that leads back to itself ends instead of looping.
func TestUnknownSeesThroughMergeKeys(t *testing.T) {
	in := "base: &b {ulr: x}\nserver:\n  <<: *b\n  auth: true\n"
	got := unknownPaths(t, in)
	if len(got) != 2 || got[1].Path != "server.ulr" || got[1].Hint != "server.url" {
		t.Errorf("unknown = %+v, want base and server.ulr", got)
	}
	if got := unknownPaths(t, "server: &a\n  <<: *a\n"); len(got) != 0 {
		t.Errorf("self merge: unknown = %+v", got)
	}
}

func TestUnknownOfUnparseableOrEmptyData(t *testing.T) {
	for _, in := range []string{"", "a: [", "- 1\n- 2\n", "just text"} {
		if got := unknownPaths(t, in); len(got) != 0 {
			t.Errorf("%q: unknown = %+v, want none", in, got)
		}
	}
}
