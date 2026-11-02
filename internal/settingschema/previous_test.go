package settingschema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// historySchema is a schema whose descriptions changed since a file was written:
// name and roots read differently now (and roots moved its default), auth lost the
// mention of a key that is gone.
var historySchema = Schema{Keys: []Key{
	{Path: "name", Scope: ScopeServer, Type: "string", Default: `""`, Env: "X_NAME", Example: "demo",
		Description: "The name of the thing, as it is shown.", Previous: []string{"The name."}},
	{Path: "roots", Scope: ScopeServer, Type: "list", Default: `["a", "b"]`, Example: "- a",
		Description: "Directories scanned for clones, four levels deep.", Previous: []string{"Directories scanned for clones."}},
	{Path: "mode", Scope: ScopeServer, Type: "string", Values: "on | off", Default: "off",
		Description: "Whether the mode is on, from the second release.", Previous: []string{"Whether the mode is on.", "Mode switch."}},
	{Path: "auth", Scope: ScopeServer, Type: "bool", Default: "false",
		Description: "The server requires a bearer token; the clients then send the token.",
		Previous:    []string{"The server requires a bearer token; the clients then send the one read from token_env."}},
	{Path: "token", Scope: ScopeServer, Type: "string", Default: "${T}", Secret: true,
		Description: "The bearer token itself."},
}}

// historyRegistry retires the keys whose comments a file of the old release holds.
var historyRegistry = Registry{Removed: []Removed{
	{Path: "agents", Plan: 906, Description: "The agent clients connected to the server, for example claude or opencode. connect and disconnect maintain it."},
	{Path: "token_env", Plan: 906, Replacement: "token", Description: "Name of the environment variable that holds the bearer token.",
		Previous: []string{"Name of the variable with the token."}},
	{Path: "trust", Plan: 906, Description: "Treat what a KB provides as trusted at every sync."},
}}

func realignHistory(t *testing.T, in string) string {
	t.Helper()
	out, _, err := historySchema.Realign([]byte(in), historyRegistry)
	if err != nil {
		t.Fatalf("Realign: %v", err)
	}
	again, rep, err := historySchema.Realign(out, historyRegistry)
	if err != nil || !rep.Empty() || string(again) != string(out) {
		t.Errorf("a second realignment changes the file: err=%v report=%v\n%s\n--- second\n%s", err, rep.Lines(), out, again)
	}
	return string(out)
}

// A comment the schema wrote when the description read differently is rewritten
// to the current one, in the layout it was written in and whatever notes it
// carried then: the default and the variable are not compared.
func TestEarlierDescriptionIsRewrittenToTheCurrentOne(t *testing.T) {
	const (
		name  = "# The name of the thing, as it is shown.\n# Default: none · Env X_NAME\nname: a\n"
		roots = "# Directories scanned for clones, four levels deep.\n# Default: [\"a\", \"b\"]\nroots:\n  - a\n"
		mode  = "# Whether the mode is on, from the second release.\n# Values: on | off · Default: off\nmode: \"on\"\n"
		auth  = "# The server requires a bearer token; the clients then send the token.\n# Default: false\nauth: true\n"
	)
	for what, tc := range map[string]struct{ in, want string }{
		"compact layout":            {"# The name.\n# Default: none · Env X_OLD\nname: a\n", name},
		"compact, no notes":         {"# The name.\nname: a\n", name},
		"legacy layout":             {"# The name.\n# Default: \"\". Env: X_NAME.\nname: a\n", name},
		"legacy, other default":     {"# The name.\n# Default: \"x\". Env: X_NAME. Needs a restart.\nname: a\n", name},
		"a values line, then notes": {"# Whether the mode is on.\n# Values: on | off · Default: off\nmode: \"on\"\n", mode},
		"a values line, legacy":     {"# Whether the mode is on.\n# Values: on | maybe | off\n# Default: off.\nmode: \"on\"\n", mode},
		"the oldest of two":         {"# Mode switch.\n# Default: off.\nmode: \"on\"\n", mode},
		"a wrapped description":     {"# The server requires a bearer token; the clients then send the one read from\n# token_env.\n# Default: false.\nauth: true\n", auth},
		"a default in brackets":     {"# Directories scanned for clones.\n# Default: [\"~/Documents\"].\nroots:\n  - a\n", roots},
		"the same, compact":         {"# Directories scanned for clones.\n# Default: [\"~/Documents\", \"~/src\"]\nroots:\n  - a\n", roots},
	} {
		t.Run(what, func(t *testing.T) {
			if out := realignHistory(t, tc.in); !strings.HasPrefix(out, tc.want) {
				t.Errorf("want the file to open with\n%s\n--- got\n%s", tc.want, out)
			}
		})
	}
}

// A person's line under an earlier description makes the comment theirs: it is
// kept whole, as it is for the current description.
func TestAPersonsLineUnderAnEarlierDescriptionIsKept(t *testing.T) {
	for _, in := range []string{
		"# The name.\n# but only on Tuesdays\nname: a\n",
		"# The name.\n# Default: none · Env X_OLD\n# my note\nname: a\n",
		"# The name.\n# Default: none · Env X_OLD and more\nname: a\n",
		"# my note\n# The name.\nname: a\n",
		"# Directories scanned for clones.\n# Default: [\"a\"]. Do not touch.\nroots:\n  - a\n",
	} {
		if out := realignHistory(t, in); !strings.HasPrefix(out, in) {
			t.Errorf("a comment with a person's line was rewritten:\n%s\n--- became\n%s", in, out)
		}
	}
}

// A key listed in the block of unset settings under an earlier description is
// listed again under the current one; an assignment the person edited keeps its
// text with it.
func TestEarlierDescriptionInTheBlockIsListedAgain(t *testing.T) {
	in := `token: x

# --- available settings: uncomment a key to set it ---

# The name.
# Default: none · Env X_OLD
# name: demo

# Directories scanned for clones.
# Default: ["a"]
# roots:
#   - a

# Whether the mode is on.
# Values: on | off
# mode: off
`
	out := realignHistory(t, in)
	for _, want := range []string{
		"# The name of the thing, as it is shown.\n# Default: none · Env X_NAME\n# name: demo",
		"# Directories scanned for clones, four levels deep.\n# Default: [\"a\", \"b\"]\n# roots:\n#   - a",
		"# Whether the mode is on, from the second release.\n# Values: on | off\n# mode: off",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the block lacks %q:\n%s", want, out)
		}
	}
	edited := strings.Replace(in, "# name: demo", "# name: mine # why", 1)
	out = realignHistory(t, edited)
	if !strings.Contains(out, "# The name.\n# Default: none · Env X_OLD\n# name: mine # why") {
		t.Errorf("an edited key did not stay as written:\n%s", out)
	}
}

// The comment of a removed key goes with it, in the text it had when the key was
// removed or in an earlier one; the note a person wrote above it moves to the next
// key, and a comment that is none of the schema's is the person's wherever it is.
func TestRetiredKeyTakesItsCommentAndSparesAPersonsNote(t *testing.T) {
	gen := "# Name of the environment variable that holds the bearer token.\n# Default: CARTOGRAPHER_TOKENS.\n"
	for what, tc := range map[string]struct{ in, want, gone string }{
		"its own comment": {"name: a\n" + gen + "token_env: T\nauth: true\n", "name: a\n\n# The server requires", gen},
		"an earlier text": {"# Name of the variable with the token.\n# Default: X.\ntoken_env: T\nauth: true\n", "# The server requires", "Name of the variable"},
		"a note above it": {"# mine, about the token\n" + gen + "token_env: T\nauth: true\n", "# mine, about the token\nauth: true", gen},
		"a note below it": {gen + "# mine, about the token\ntoken_env: T\nauth: true\n", gen + "# mine, about the token\nauth: true", ""},
		"only a note":     {"# mine, about the token\ntoken_env: T\nauth: true\n", "# mine, about the token\nauth: true", ""},
		"the last key":    {"auth: true\n" + gen + "token_env: T\n", "auth: true\n", "Name of the environment"},
	} {
		t.Run(what, func(t *testing.T) {
			out := realignHistory(t, tc.in)
			if tc.gone != "" && strings.Contains(out, tc.gone) {
				t.Errorf("%q is still in the file:\n%s", tc.gone, out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("the file lacks %q:\n%s", tc.want, out)
			}
			if strings.Contains(out, "token_env: T") {
				t.Errorf("the retired key is still there:\n%s", out)
			}
		})
	}
}

// What an earlier realignment moved onto the next key, the comment of a retired
// key written in its release's wording, is taken off again; the key's own
// comment, in an earlier text, is brought up to date at the same time. This is the
// file a real installation holds.
func TestFossilsOnTheNeighbourAreCleaned(t *testing.T) {
	in := `# The agent clients connected to the server, for example claude or opencode.
# connect and disconnect maintain it.
# Default: [].
# The server requires a bearer token; the clients then send the one read from
# token_env.
# Default: false.
auth: true

# Name of the environment variable that holds the bearer token.
# Default: CARTOGRAPHER_TOKENS.
# Treat what a KB provides as trusted at every sync.
# Default: true.
token: ${T}
`
	out := realignHistory(t, in)
	want := "# The server requires a bearer token; the clients then send the token.\n# Default: false\nauth: true\n\n# The bearer token itself.\n# Default: ${T} · secret: keep it out of version control\ntoken: ${T}\n"
	if !strings.Contains(out, want) {
		t.Errorf("the fossils are still there, or the neighbours were not documented:\n%s", out)
	}
	for _, gone := range []string{"agent clients", "Name of the environment", "Treat what a KB", "token_env"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q survives:\n%s", gone, out)
		}
	}
}

// A fossil with a person's line in the same comment stays: the line is theirs, and
// the run is not cut out from under it.
func TestFossilWithAPersonsLineStays(t *testing.T) {
	in := "# Treat what a KB provides as trusted at every sync.\n# Default: true.\n# mine\ntoken: ${T}\n"
	out := realignHistory(t, in)
	if !strings.Contains(out, "# mine\ntoken: ${T}") || !strings.Contains(out, "Treat what a KB") {
		t.Errorf("a comment with a person's line was cut:\n%s", out)
	}
}

// The end of the file is where the comment of a last key goes when it is removed.
func TestFossilAtTheEndOfTheFileIsCleaned(t *testing.T) {
	in := "auth: true\n\n# mine\n# Treat what a KB provides as trusted at every sync.\n# Default: true.\n"
	out := realignHistory(t, in)
	if strings.Contains(out, "Treat what a KB") || !strings.Contains(out, "# mine") {
		t.Errorf("the end of the file:\n%s", out)
	}
}

// DropFossils is what a writer that removes a key itself (the client's Save) asks.
func TestDropFossils(t *testing.T) {
	gen := "# Treat what a KB provides as trusted at every sync.\n# Default: true."
	for in, want := range map[string]string{
		gen:              "",
		"# mine\n" + gen: "# mine",
		gen + "\n# mine": gen + "\n# mine",
		gen + "\n" + gen: "",
		"# The bearer token itself.\n# Secret: keep it out of version control\n" + gen: "# The bearer token itself.\n# Secret: keep it out of version control",
		"# Something else.": "# Something else.",
		"":                  "",
	} {
		if got := historySchema.DropFossils(in, historyRegistry); got != want {
			t.Errorf("DropFossils(%q) = %q, want %q", in, got, want)
		}
	}
	if got := historySchema.DropFossils(gen, Registry{}); got != gen {
		t.Errorf("an empty registry changed the comment: %q", got)
	}
}

func TestIsNotes(t *testing.T) {
	for in, want := range map[string]bool{
		"":                                     true,
		"# Default: none":                      true,
		"# Default: none · Env X_Y · flag --x": true,
		"# Values: on | off · Default: off":    true,
		"# Values: on | off":                   true,
		"# Needs a restart · secret: keep it out of version control": true,
		"# Env A_B, C_D · needs a restart":                           true,
		"# Default: [\"~/a\", \"~/b\"]":                              true,
		"# Default: {}":                                              true,
		"# Default: \"\". Env: X.":                                   true,
		"# Default: [\"~/a\", \"~/b\"]. Needs a restart. Secret: keep it out of version control.": true,
		"# Values: a | b\n# Default: a.":                  true,
		"# Values: on | off · Env X\n# · needs a restart": true,
		"# Default: none\n# my note":                      false,
		"# Default: none · and more":                      false,
		"# Default: one two":                              false,
		"# my note":                                       false,
		"# Default:":                                      false,
		"Default: none":                                   false,
		"#":                                               false,
		"# Default: none\n\n# Env X":                      false,
	} {
		var lines []string
		if in != "" {
			lines = strings.Split(in, "\n")
		}
		if got := isNotes(lines); got != want {
			t.Errorf("isNotes(%q) = %v, want %v", in, got, want)
		}
	}
}

// A file realigned twice is written once: the second realignment finds nothing to
// do and leaves no second backup. The file holds a fossil and an earlier text.
func TestRealignFileOfAFossilWritesOneBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.yaml")
	in := "# Treat what a KB provides as trusted at every sync.\n# Default: true.\n# The server requires a bearer token; the clients then send the one read from\n# token_env.\n# Default: false.\nauth: true\n"
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, _, err := historySchema.RealignFile(path, historyRegistry, "v1"); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "Treat what a KB") || strings.Contains(string(got), "token_env") {
		t.Errorf("the file keeps its fossils:\n%s", got)
	}
	baks, _ := filepath.Glob(path + ".bak-*")
	if len(baks) != 1 {
		t.Errorf("want one backup, got %v", baks)
	}
}
