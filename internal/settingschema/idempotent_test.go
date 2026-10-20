package settingschema

import (
	"strings"
	"testing"
)

// shapeSchema has the shapes the server config has: a nested mapping whose last
// key can be a block list (auth.tokens, auth.roles), a list of mappings at the
// top, and a nested mapping that nests another.
var shapeSchema = Schema{Keys: []Key{
	{Path: "name", Scope: ScopeServer, Type: "string", Default: `""`, Description: "The name.", Example: "demo"},
	{Path: "auth.mode", Scope: ScopeServer, Type: "string", Values: "on | off | auto", Default: "auto", Example: "auto", Description: "Whether auth is on."},
	{Path: "auth.tokens", Scope: ScopeServer, Type: "list", Default: "[]", Secret: true,
		Example: "- token: ${T}\n  id: ci\n- ${ADMIN}", Description: "The tokens."},
	{Path: "auth.roles", Scope: ScopeServer, Type: "list", Default: "[]",
		Example: "- name: editor\n  rules:\n    - kb: kb-a\n      access: rw", Description: "The roles."},
	{Path: "kbs", Scope: ScopeServer, Type: "list", Default: "[]", Example: "- remote: r\n  name: n", Description: "The KBs."},
	{Path: "kbs[].remote", Scope: ScopeServer, Type: "string", Default: `""`, Description: "The remote."},
	{Path: "kbs[].name", Scope: ScopeServer, Type: "string", Default: `""`, Description: "The KB name."},
	{Path: "git.sync", Scope: ScopeServer, Type: "bool", Default: "true", Description: "Sync."},
	{Path: "git.hooks.pre", Scope: ScopeServer, Type: "string", Default: `""`, Description: "Pre hook."},
	{Path: "git.hooks.post", Scope: ScopeServer, Type: "string", Default: `""`, Description: "Post hook."},
}}

// shapeInputs end a mapping in every way a person's file does: a block list, a
// list of mappings, a nested mapping, an unknown key, a comment.
var shapeInputs = []string{
	"",
	"name: a\n",
	"auth:\n  mode: \"on\"\n  tokens:\n    - token: ${T}\n      id: ci\n",
	"auth:\n  tokens:\n    - a\n    - b\n",
	"auth:\n  mode: \"on\"\n  tokens:\n    - token: ${T}\n      id: ci\n  roles:\n    - name: editor\n      rules:\n        - kb: kb-a\n          access: rw\n",
	"auth:\n  roles:\n    - name: editor\n",
	"auth:\n  tokens: [a, b]\n",
	"auth:\n  mode: \"on\"\n  tokens:\n    - token: ${T}\n      id: ci\n# a note after the list\nname: a\n",
	"kbs:\n  - remote: r\n    name: n\nname: a\n",
	"name: a\nkbs:\n  - remote: r\n    name: n\n",
	"git:\n  sync: false\n  hooks:\n    pre: x\n",
	"git:\n  sync: false\n  hooks:\n    pre: x\n    custom:\n      - one\n      - two\n",
	"git:\n  sync: false\n  extra:\n    - one\n    - two\n",
	"name: a\nextra:\n  - one\n  - two\n",
	"# header\nauth:\n  # mine\n  tokens:\n    - token: ${T}\n",
	"auth:\n  tokens:\n    - a\n  # trailing note\nname: x\n",
	"auth:\n  tokens:\n    - a\n\n  # trailing note\n",
	"auth:\n  tokens:\n    - a\n    - b\n  # trailing note\n\n# root footer\n",
	"auth:\n  mode: x\n  tokens:\n    - id: a\n      token: b\n\n# end of file note\n",
	"git:\n  hooks:\n    pre: a\n    custom:\n      - one\n\n  # mine\n",
}

// A file the schema has rendered is a fixed point: rendering it again changes
// nothing, and realigning it reports nothing. A list or a mapping as the last
// key of a mapping must not move the available-settings block into itself.
func TestRenderIsIdempotentWhateverEndsAMapping(t *testing.T) {
	for _, in := range shapeInputs {
		once := layoutFormat(t, shapeSchema, in)
		if twice := layoutFormat(t, shapeSchema, once); twice != once {
			t.Errorf("Format is not idempotent for %q:\n%s\n--- second pass\n%s", in, once, twice)
		}
		out, rep, err := shapeSchema.Realign([]byte(once), Registry{})
		if err != nil {
			t.Fatalf("Realign(%q): %v", in, err)
		}
		if !rep.Empty() || string(out) != once {
			t.Errorf("Realign of a formatted file is not a no-op for %q: report %v\n%s\n--- realigned\n%s", in, rep.Lines(), once, out)
		}
		// Only the owners with unset keys carry a block: auth, git, hooks, the root.
		if n := strings.Count(once, "# --- available settings"); n > 4 {
			t.Errorf("%q: %d dividers:\n%s", in, n, once)
		}
		// What a person wrote is still there.
		for _, l := range strings.Split(in, "\n") {
			if l = strings.TrimSpace(l); strings.HasPrefix(l, "# ") && !strings.Contains(once, l) {
				t.Errorf("%q: lost %q:\n%s", in, l, once)
			}
		}
	}
}

// A block an earlier render wrote inside the last list entry is put back under
// the mapping it lists: one realignment, then nothing left to do.
func TestBlockInsideTheLastListEntryIsRepaired(t *testing.T) {
	broken := `auth:
  mode: "on"
  tokens:
    - token: ${T}
      id: ci

      # --- available settings: uncomment a key to set it ---

      # The roles.
      # roles:
      #   - name: editor

# --- available settings: uncomment a key to set it ---

# The name.
# Default: none
# name: demo
`
	out, rep, err := shapeSchema.Realign([]byte(broken), Registry{})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if rep.Empty() {
		t.Errorf("a misplaced block was reported as aligned")
	}
	if strings.Contains(got, "      # --- available") || strings.Count(got, "# The roles.") != 1 ||
		!strings.Contains(got, "      id: ci\n\n  # --- available settings: uncomment a key to set it ---\n\n  # The roles.") {
		t.Errorf("the block is not back under auth:\n%s", got)
	}
	if again, rep, _ := shapeSchema.Realign(out, Registry{}); string(again) != got || !rep.Empty() {
		t.Errorf("second realign is not a no-op: %v\n%s\n---\n%s", rep.Lines(), got, again)
	}
}

// A removed key listed in a block that sits behind a list is dropped from it too.
func TestRetiredKeyIsDroppedFromABlockBehindAList(t *testing.T) {
	old := "auth:\n  tokens:\n    - token: ${T}\n      id: ci\n\n  # --- available settings ---\n  # The roles.\n  # roles: []\n  # The legacy mode.\n  # legacy_mode: x\n"
	out, _, err := shapeSchema.Realign([]byte(old), Registry{Removed: []Removed{{Path: "auth.legacy_mode", Plan: 903}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "legacy_mode") || strings.Contains(string(out), "The legacy mode") {
		t.Errorf("the removed key survives in the block:\n%s", out)
	}
}

// Each block stays with the mapping it lists when an inner mapping ends in a
// list and the root has a block of its own after it: neither takes the other's.
func TestEachMappingKeepsItsOwnBlock(t *testing.T) {
	in := "auth:\n  mode: \"on\"\n  tokens:\n    - token: ${T}\n      id: ci\n"
	once := layoutFormat(t, shapeSchema, in)
	if n := strings.Count(once, "\n  # --- available settings"); n != 1 {
		t.Errorf("want one block under auth, got %d:\n%s", n, once)
	}
	if n := strings.Count(once, "\n# --- available settings"); n != 1 {
		t.Errorf("want one block at the root, got %d:\n%s", n, once)
	}
	if got := strings.Count(once, "# The roles."); got != 1 {
		t.Errorf("the roles are listed %d times:\n%s", got, once)
	}
	if twice := layoutFormat(t, shapeSchema, once); twice != once {
		t.Errorf("not idempotent:\n%s\n---\n%s", once, twice)
	}
}
