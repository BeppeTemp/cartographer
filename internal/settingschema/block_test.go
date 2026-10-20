package settingschema

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// layoutSchema has what the layout of a file depends on: a scalar with notes, a
// map and a nested list that carry an Example, a group of keys, a secret.
var layoutSchema = Schema{Keys: []Key{
	{Path: "name", Scope: ScopeServer, Type: "string", Default: `""`, Env: "X_NAME", Flag: "--name", Restart: true,
		Description: "The name of the thing."},
	{Path: "scopes", Scope: ScopeClient, Type: "map", Values: "provider | workspace", Default: "{}",
		Description: "Where a provider receives KB artifacts: provider (global, under the home directory) or workspace (each bound workspace gets its own).",
		Example:     "claude: workspace"},
	{Path: "workspaces", Scope: ScopeClient, Type: "map", Default: "{}",
		Description: "Which KBs a provider receives while working in a given directory. An unbound workspace gets only the transversal bundle.",
		Example:     "claude:\n  - path: $HOME/projects/work\n    kbs: [work-kb]"},
	{Path: "port", Scope: ScopeServer, Type: "string", Default: `""`, Description: "The port.", Example: `":8080"`},
	{Path: "audit.max_bytes", Scope: ScopeServer, Type: "int", Values: "bytes; 0 keeps the default", Default: "67108864", Restart: true,
		Description: "Size after which the active log segment is rotated, so retention has segments to delete."},
	{Path: "audit.mode", Scope: ScopeServer, Type: "string", Values: "best_effort | required", Default: "best_effort", Env: "AUDIT_MODE", Flag: "--audit-mode", Restart: true,
		Description: "What a failed append does."},
	{Path: "token", Scope: ScopeServer, Type: "string", Default: `""`, Secret: true, Restart: true,
		Description: "The bearer token."},
}}

func layoutFormat(t *testing.T, s Schema, in string) string {
	t.Helper()
	out, err := s.Format([]byte(in))
	if err != nil {
		t.Fatalf("Format: %v", err)
	}
	return string(out)
}

// The whole available block of an empty file: one blank line between the keys,
// notes on one line, the value to uncomment under each.
func TestAvailableBlockLayout(t *testing.T) {
	want := `# --- available settings: uncomment a key to set it ---

# The name of the thing.
# Env X_NAME · flag --name · needs a restart
# name: ""

# Where a provider receives KB artifacts: provider (global, under the home
# directory) or workspace (each bound workspace gets its own).
# Values: provider | workspace
# scopes:
#   claude: workspace

# Which KBs a provider receives while working in a given directory. An unbound
# workspace gets only the transversal bundle.
# workspaces:
#   claude:
#     - path: $HOME/projects/work
#       kbs: [work-kb]

# The port.
# Default: none
# port: ":8080"

# audit:
#   Size after which the active log segment is rotated, so retention has
#   segments to delete.
#   Values: bytes; 0 keeps the default · needs a restart
#   max_bytes: 67108864
#
#   What a failed append does.
#   Values: best_effort | required · Env AUDIT_MODE · flag --audit-mode ·
#   needs a restart
#   mode: best_effort

# The bearer token.
# Needs a restart · secret: keep it out of version control
# token: ""
`
	if got := layoutFormat(t, layoutSchema, ""); got != want {
		t.Errorf("available block:\n%s\nwant:\n%s", got, want)
	}
}

// Uncommenting the assignment of an unset key gives a value that parses to the
// Example (to the Default when there is none): that is what "ready to uncomment"
// means.
func TestCommentedAssignmentsParseToTheShownValue(t *testing.T) {
	var flat Schema
	for _, k := range layoutSchema.Keys {
		if !strings.HasPrefix(k.Path, "audit.") {
			flat.Keys = append(flat.Keys, k)
		}
	}
	out := layoutFormat(t, flat, "")
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(l, "# "), l == "#":
			lines = append(lines, l)
		}
	}
	// Keep the assignment lines: "# key:" and what is indented under it. The top
	// level keys are the ones at column 2 that end a description.
	assign := regexp.MustCompile(`^# [a-z_]+:( |$)`)
	var yamlText []string
	in := false
	for _, l := range lines {
		switch {
		case assign.MatchString(l):
			in = true
			yamlText = append(yamlText, strings.TrimPrefix(l, "# "))
		case in && strings.HasPrefix(l, "#  "):
			yamlText = append(yamlText, strings.TrimPrefix(l, "# "))
		default:
			in = false
		}
	}
	var got map[string]any
	if err := yaml.Unmarshal([]byte(strings.Join(yamlText, "\n")), &got); err != nil {
		t.Fatalf("the uncommented assignments are not YAML: %v\n%s", err, strings.Join(yamlText, "\n"))
	}
	want := map[string]any{
		"name":       "",
		"scopes":     map[string]any{"claude": "workspace"},
		"workspaces": map[string]any{"claude": []any{map[string]any{"path": "$HOME/projects/work", "kbs": []any{"work-kb"}}}},
		"port":       ":8080",
		"token":      "",
	}
	for k, v := range want {
		if !reflect.DeepEqual(got[k], v) {
			t.Errorf("%s uncommented = %#v, want %#v", k, got[k], v)
		}
	}
}

// The default is stated only where the line next to the comment does not show
// it: a set key (the line shows its own value) or an unset one whose Example
// differs. An empty map or list has nothing to state.
func TestDefaultIsStatedOnlyWhereItSaysSomething(t *testing.T) {
	block := layoutFormat(t, layoutSchema, "")
	if strings.Contains(block, "Default: 67108864") || strings.Contains(block, "Default: best_effort") {
		t.Errorf("a default is repeated next to a line that shows it:\n%s", block)
	}
	if !strings.Contains(block, "# The port.\n# Default: none\n# port: \":8080\"") {
		t.Errorf("a differing Example does not state the default:\n%s", block)
	}
	if strings.Contains(block, "Default: {}") || strings.Contains(block, "Default: none\n# scopes") {
		t.Errorf("an empty map states its default:\n%s", block)
	}
	set := layoutFormat(t, layoutSchema, "audit:\n  max_bytes: 10\n")
	if !strings.Contains(set, "Values: bytes; 0 keeps the default · Default: 67108864 · needs a restart\n  max_bytes: 10") {
		t.Errorf("a set key does not state its default:\n%s", set)
	}
}

// Set keys are apart by one blank line too, the comment a person wrote stays as
// it is, and a second render changes nothing.
func TestSetKeysAreApartByABlankLine(t *testing.T) {
	in := "name: a\n# mine\nport: \":1\"\naudit:\n  mode: required\ntoken: x\n"
	out := layoutFormat(t, layoutSchema, in)
	for _, want := range []string{
		"name: a\n\n# mine\nport: \":1\"\n\naudit:\n  # What a failed append does.",
		"  mode: required\n\n  # --- available settings: uncomment a key to set it ---\n\n  # Size after which",
		"\n\n# The bearer token.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if again := layoutFormat(t, layoutSchema, out); again != out {
		t.Errorf("not idempotent:\n%s\n--- second pass\n%s", out, again)
	}
	for i, l := range strings.Split(out, "\n") {
		if strings.TrimRight(l, " \t") != l {
			t.Errorf("line %d carries trailing whitespace: %q", i+1, l)
		}
	}
}

// A file written by an earlier version has the old divider and the old wording.
// It is realigned to the new layout in one pass, lists each key once, and the
// next realignment finds nothing to do.
func TestOldDividerIsRecognisedAndRealignedOnce(t *testing.T) {
	old := `# The name of the thing.
# Default: "". Env: X_NAME. Flag: --name. Needs a restart.
name: custom
audit:
  # What a failed append does.
  # Values: best_effort | required
  # Default: best_effort. Env: AUDIT_MODE. Flag: --audit-mode. Needs a restart.
  mode: required
  # --- available settings ---
  # Size after which the active log segment is rotated, so retention has
  # segments to delete.
  # Values: bytes; 0 keeps the default
  # Default: 67108864. Needs a restart.
  # max_bytes: 67108864

# --- available settings ---
# Where a provider receives KB artifacts: provider (global, under the home
# directory) or workspace (each bound workspace gets its own).
# Values: provider | workspace
# Default: {}.
# scopes: {}
# The port.
# Default: "".
# port: ":8080"
# The bearer token.
# Default: "". Needs a restart. Secret: keep it out of version control.
# token: ""
`
	out, rep, err := layoutSchema.Realign([]byte(old), Registry{})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Count(got, "# --- available settings") != 2 || strings.Contains(got, "# --- available settings ---") {
		t.Errorf("the dividers were not rewritten, or the block is listed twice:\n%s", got)
	}
	for _, want := range []string{
		// The comments above set keys are the new wording...
		"# The name of the thing.\n# Default: none · Env X_NAME · flag --name · needs a restart\nname: custom",
		"  # What a failed append does.\n  # Values: best_effort | required · Default: best_effort · Env AUDIT_MODE ·\n  # flag --audit-mode · needs a restart\n  mode: required",
		// ...and so is every key of the blocks, the map with its Example.
		"  # Values: bytes; 0 keeps the default · needs a restart\n  # max_bytes: 67108864\n",
		"# Values: provider | workspace\n# scopes:\n#   claude: workspace\n",
		"# The port.\n# Default: none\n# port: \":8080\"\n",
		"# Needs a restart · secret: keep it out of version control\n# token: \"\"\n",
		"# workspaces:\n#   claude:\n#     - path: $HOME/projects/work\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("realigned file lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Default: \"\".") || strings.Contains(got, "Needs a restart.") {
		t.Errorf("the old wording survives:\n%s", got)
	}
	if rep.Empty() {
		t.Errorf("a file of the old layout was reported as already aligned")
	}
	again, rep2, err := layoutSchema.Realign(out, Registry{})
	if err != nil || string(again) != got || !rep2.Empty() {
		t.Errorf("second realign is not a no-op: err=%v report=%v\n%s", err, rep2.Lines(), again)
	}
}

// What a person edited in the block is theirs, in the old layout as in the new:
// the whole key stays as written; the keys they left alone move to the new form.
func TestEditedKeyOfAnOldBlockStaysAsWritten(t *testing.T) {
	old := `name: a
# --- available settings ---
# The port.
# Default: "".
# port: ":9999" # my port
# The bearer token.
# Default: "". Needs a restart. Secret: keep it out of version control.
# token: ""
`
	out, _, err := layoutSchema.Realign([]byte(old), Registry{})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "# The port.\n# Default: \"\".\n# port: \":9999\" # my port\n") {
		t.Errorf("the edited key did not stay as written:\n%s", got)
	}
	if !strings.Contains(got, "# The bearer token.\n# Needs a restart · secret: keep it out of version control\n# token: \"\"\n") {
		t.Errorf("the untouched key was not listed in the new form:\n%s", got)
	}
	if again, _, _ := layoutSchema.Realign(out, Registry{}); string(again) != got {
		t.Errorf("not idempotent:\n%s\n---\n%s", got, again)
	}
}

// A comment a person wrote is never taken for one the schema generated, whatever
// its spacing; one the schema wrote in the old wording is.
func TestOldHeadCommentIsUpgradedAPersonsIsNot(t *testing.T) {
	in := "# Default: \"\". Env: X_NAME. Flag: --name. Needs a restart.\nname: a\n"
	if out := layoutFormat(t, layoutSchema, in); strings.Contains(out, "The name of the thing") {
		t.Errorf("a person's comment was replaced:\n%s", out)
	}
	in = "# The name of the thing.\n# Default: \"\". Env: X_NAME. Flag: --name. Needs a restart.\nname: a\n"
	if out := layoutFormat(t, layoutSchema, in); !strings.Contains(out, "# The name of the thing.\n# Default: none · Env X_NAME") {
		t.Errorf("the old wording was kept:\n%s", out)
	}
}

func TestIsMarker(t *testing.T) {
	for l, want := range map[string]bool{
		"# --- available settings ---":                            true,
		"  # --- available settings ---  ":                        true,
		"# --- available settings: uncomment a key to set it ---": true,
		"# --- available settings: some later wording ---":        true,
		"# --- available settings":                                false,
		"# available settings ---":                                false,
		"# --- available settings --":                             false,
		"# note: --- available settings ---":                      false,
		"# --- available settingsXYZ---":                          false,
		"":                                                        false,
	} {
		if got := isMarker(l); got != want {
			t.Errorf("isMarker(%q) = %v, want %v", l, got, want)
		}
	}
}

// The example file sets scalar Examples only: a map or a list shows the shape of
// an unset key, and must not configure it.
func TestDocumentLeavesOutStructuredExamples(t *testing.T) {
	doc, err := layoutSchema.Document()
	if err != nil {
		t.Fatal(err)
	}
	b, err := Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(b); strings.Contains(got, "scopes") || strings.Contains(got, "workspaces") || !strings.Contains(got, "port:") {
		t.Errorf("document:\n%s", got)
	}
}

// Text a person wrote in the available-settings block is theirs: the key below it
// is listed again as the schema generates it, and the person's lines stay where
// they were. Only generated text is rewritten.
func TestAPersonsLinesInTheBlockSurviveARealign(t *testing.T) {
	head := "name: a\n\n" + Marker + "\n\n"
	for name, tc := range map[string]struct{ in, want string }{
		"a note above a key in the current wording": {
			in:   head + "# my reminder: never enable this\n# The port.\n# Default: none\n# port: \":8080\"\n",
			want: head + "# my reminder: never enable this\n# The port.\n# Default: none\n# port: \":8080\"\n",
		},
		"a note above a key in the old wording": {
			in:   head + "# my reminder: never enable this\n# The port.\n# Default: \"\".\n# port: \":8080\"\n",
			want: head + "# my reminder: never enable this\n# The port.\n# Default: none\n# port: \":8080\"\n",
		},
		"a note between the description and the assignment": {
			in:   head + "# The port.\n# Default: none\n# keep it above 1024\n# port: \":8080\"\n",
			want: head + "# The port.\n# Default: none\n# keep it above 1024\n# port: \":8080\"\n",
		},
		"a note above a key with no description of the schema's": {
			in:   head + "# my reminder: never enable this\n# port: \":8080\"\n",
			want: head + "# my reminder: never enable this\n# port: \":8080\"\n",
		},
		"an edited description": {
			in:   head + "# The port the thing listens on.\n# Default: none\n# port: \":8080\"\n",
			want: head + "# The port the thing listens on.\n# Default: none\n# port: \":8080\"\n",
		},
		"a note and a key of the old layout, both kept, only the wording moves": {
			in:   head + "# my reminder\n# The bearer token.\n# Default: \"\". Needs a restart. Secret: keep it out of version control.\n# token: \"\"\n",
			want: head + "# my reminder\n# The bearer token.\n# Needs a restart · secret: keep it out of version control\n# token: \"\"\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Each case holds the key it names; the rest of the block is the schema's.
			out, _, err := layoutSchema.Realign([]byte(tc.in), Registry{})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(out), strings.TrimPrefix(tc.want, head)) {
				t.Errorf("lost or changed a person's line:\n%s\nwant it to hold:\n%s", out, strings.TrimPrefix(tc.want, head))
			}
			if again, rep, _ := layoutSchema.Realign(out, Registry{}); string(again) != string(out) || !rep.Empty() {
				t.Errorf("not idempotent: %v\n%s\n---\n%s", rep.Lines(), out, again)
			}
		})
	}
}
