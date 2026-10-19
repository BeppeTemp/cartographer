package settingschema

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

// realignSchema is the schema of a file whose older version also had git.profile,
// items[].tag and legacy, and a git.sync that could be switched off.
var realignSchema = Schema{Keys: []Key{
	{Path: "name", Scope: ScopeServer, Type: "string", Default: `""`, Description: "The name."},
	{Path: "git.sync", Scope: ScopeServer, Type: "bool", Default: "true", Description: "Sync."},
	{Path: "git.branch", Scope: ScopeServer, Type: "string", Default: `""`, Description: "Branch."},
	{Path: "items", Scope: ScopeServer, Type: "list", Default: "[]", Description: "The items."},
	{Path: "items[].id", Scope: ScopeServer, Type: "string", Default: `""`, Description: "Item id."},
}}

var realignRegistry = Registry{
	Removed: []Removed{
		{Path: "git.profile", Replacement: "git.branch", Note: "unprotect the branch", Plan: 903, Refuse: "server"},
		{Path: "items[].tag", Plan: 903},
		{Path: "legacy", Note: "move the folder under data", Plan: 903},
	},
	Off: []Off{{Path: "git.sync", Value: "false", Plan: 903}},
}

func realign(t *testing.T, in string) (string, Report) {
	t.Helper()
	out, rep, err := realignSchema.Realign([]byte(in), realignRegistry)
	if err != nil {
		t.Fatalf("Realign: %v", err)
	}
	return string(out), rep
}

// The three things a realignment does to a file that has all of them: a removed
// key is dropped, an off value is reset, a custom value is kept, and the report
// lists the first two.
func TestRealignDropsRemovedResetsOffKeepsCustom(t *testing.T) {
	in := "name: custom\ngit:\n  sync: false\n  profile: local\n  branch: staging\nlegacy: /srv/kb\nitems:\n  - id: a\n    tag: t\n"
	out, rep := realign(t, in)

	for _, gone := range []string{"profile:", "legacy: /srv", "tag: t", "sync: false"} {
		if strings.Contains(out, "\n"+gone) || strings.HasPrefix(out, gone) || strings.Contains(out, "  "+gone) {
			t.Errorf("%q is still in the file:\n%s", gone, out)
		}
	}
	for _, kept := range []string{"name: custom", "branch: staging", "- id: a"} {
		if !strings.Contains(out, kept) {
			t.Errorf("%q was lost:\n%s", kept, out)
		}
	}
	if !strings.Contains(out, "# The name.") {
		t.Errorf("the schema comments were not written:\n%s", out)
	}
	got := strings.Join(rep.Lines(), "\n")
	for _, want := range []string{
		"removed git.profile: replaced by git.branch (unprotect the branch)",
		"removed legacy: move the folder under data",
		"removed items[0].tag: none, the behaviour is now always on",
		"reset git.sync: false switched a feature off; the default is back",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
}

// A second run changes nothing: same bytes, empty report.
func TestRealignIsIdempotent(t *testing.T) {
	for _, in := range []string{
		"",
		"name: a\n",
		"# top note\nname: a\ngit:\n  sync: false\n  profile: local\n",
		"git:\n  profile: server\n",
		"legacy: x\nitems:\n  - id: a\n    tag: t\n  - tag: only\n",
		"name: a\n# --- available settings ---\n# git:\n#   profile: local\n",
	} {
		once, rep := realign(t, in)
		twice, rep2 := realign(t, once)
		if twice != once {
			t.Errorf("not idempotent for %q:\n%s\n--- second pass\n%s", in, once, twice)
		}
		if !rep2.Empty() {
			t.Errorf("second run reported %v for %q", rep2.Lines(), in)
		}
		_ = rep
	}
}

func TestRealignOfACanonicalFileIsANoOp(t *testing.T) {
	canonical, _ := realign(t, "name: a\n")
	out, rep, err := realignSchema.Realign([]byte(canonical), realignRegistry)
	if err != nil || string(out) != canonical || !rep.Empty() {
		t.Fatalf("a canonical file changed: err=%v report=%v\n%s", err, rep.Lines(), out)
	}
}

// A file with no removed key is still rewritten into the commented form, once.
func TestRealignDocumentsAPlainFile(t *testing.T) {
	out, rep := realign(t, "name: a\n")
	if len(rep.Changes) != 1 || rep.Changes[0].Kind != ChangeDocumented {
		t.Fatalf("report = %v", rep.Lines())
	}
	if !strings.Contains(out, "# The name.") {
		t.Errorf("no comments:\n%s", out)
	}
}

// A mapping the realignment emptied goes with its last key; a list entry too.
func TestRealignPrunesWhatItEmptied(t *testing.T) {
	out, _ := realign(t, "name: a\ngit:\n  profile: local\nitems:\n  - tag: t\n  - id: kept\n    tag: t\n")
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["git"]; ok {
		t.Errorf("the emptied git mapping is still there:\n%s", out)
	}
	items, _ := doc["items"].([]any)
	if len(items) != 1 {
		t.Errorf("items = %v, want only the entry that still has a key", items)
	}

	// Emptied by a person is not ours to drop.
	out, _ = realign(t, "name: a\ngit: {}\nitems: []\n")
	if !strings.Contains(out, "git: {}") || !strings.Contains(out, "items: []") {
		t.Errorf("a mapping and a list the file wrote empty were dropped:\n%s", out)
	}
}

// A comment a person wrote above a removed key moves on; the one the schema
// generated goes with the key.
func TestRealignKeepsAPersonsComment(t *testing.T) {
	out, _ := realign(t, "# keep this note\nlegacy: /x\nname: a\n")
	if !strings.Contains(out, "# keep this note") {
		t.Errorf("the note was lost:\n%s", out)
	}
	out, _ = realign(t, "name: a\ngit:\n  # why we pin it\n  profile: local\n  branch: b\n")
	if !strings.Contains(out, "# why we pin it") {
		t.Errorf("the note above a removed nested key was lost:\n%s", out)
	}
}

// The block of unset settings is rebuilt: it lists what is still a setting, and
// not the key that was removed, nor its description.
func TestRealignRebuildsTheAvailableBlock(t *testing.T) {
	old := "git:\n  branch: b\n  # --- available settings ---\n  # Sync.\n  # Default: true.\n  # sync: true\n  # The profile.\n  # profile: local\n"
	out, _ := realign(t, old)
	if strings.Contains(out, "profile") || strings.Contains(out, "The profile") {
		t.Errorf("the removed key survives in the block:\n%s", out)
	}
	if !strings.Contains(out, "# sync: true") {
		t.Errorf("the block lost a live setting:\n%s", out)
	}
}

func TestRealignRefusesAFileThatIsNotAMapping(t *testing.T) {
	for _, in := range []string{"- a\n- b\n", "just text\n", "a: [unclosed\n"} {
		if _, _, err := realignSchema.Realign([]byte(in), realignRegistry); err == nil {
			t.Errorf("Realign(%q) = nil error", in)
		}
	}
}

// Find is what Load calls: it reports a retired key that was met again, never
// as an error, and names where it is and what to use.
func TestFindNamesTheReplacement(t *testing.T) {
	hits := realignRegistry.FindBytes([]byte("git:\n  profile: local\n  sync: false\nitems:\n  - id: a\n    tag: t\n"))
	var msgs []string
	for _, h := range hits {
		msgs = append(msgs, h.Message())
	}
	got := strings.Join(msgs, "\n")
	for _, want := range []string{
		"git.profile: removed setting, ignored; use git.branch (unprotect the branch)",
		"items[0].tag: removed setting, ignored: the behaviour is now always on",
		"git.sync: false switches a feature off, which is no longer a choice; it is read as the default",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("messages lack %q:\n%s", want, got)
		}
	}
	// An unrelated value of an off key is not a hit.
	if hits := realignRegistry.FindBytes([]byte("git:\n  sync: true\n")); len(hits) != 0 {
		t.Errorf("git.sync: true is a hit: %v", hits)
	}
}

func TestFindRefusedOnlyForTheRefusedValue(t *testing.T) {
	for in, want := range map[string]bool{"git:\n  profile: server\n": true, "git:\n  profile: local\n": false} {
		refused := false
		for _, h := range realignRegistry.FindBytes([]byte(in)) {
			if _, ok := h.Refused(); ok {
				refused = true
			}
		}
		if refused != want {
			t.Errorf("%q: refused = %v, want %v", in, refused, want)
		}
	}
}

// A key whose every value would change where writes go if ignored (D903,
// kbs[].git_branch) is refused for any value it holds, and the message names
// the value and where it has to go; an empty or null value refuses nothing.
func TestFindRefusedAnyValue(t *testing.T) {
	reg := Registry{Removed: []Removed{{Path: "items[].branch", Replacement: "branch in the KB", Note: "on the default branch", Plan: 903, RefuseAny: true}}}
	for in, want := range map[string]bool{
		"items:\n  - branch: team/kb-data\n": true,
		"items:\n  - branch: main\n":         true,
		"items:\n  - branch: \"\"\n":         false,
		"items:\n  - branch:\n":              false,
		"items:\n  - branch: ~\n":            false,
	} {
		hits := reg.FindBytes([]byte(in))
		if len(hits) != 1 {
			t.Fatalf("%q: hits = %v, want one", in, hits)
		}
		msg, refused := hits[0].Refused()
		if refused != want {
			t.Errorf("%q: refused = %v, want %v", in, refused, want)
		}
		if refused && (!strings.Contains(msg, "items[0].branch") || !strings.Contains(msg, hits[0].Value) || !strings.Contains(msg, "branch in the KB (on the default branch)")) {
			t.Errorf("%q: refusal %q does not name the key, its value and where it goes", in, msg)
		}
	}
}

// Processes realigning the same file at once each keep their own original: a
// backup name is taken exclusively, never shared, so none is overwritten.
func TestWriteBackupConcurrentCallersGetDistinctNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	names := make([]string, 16)
	var wg sync.WaitGroup
	for i := range names {
		wg.Go(func() {
			p, err := writeBackup(path, "0.25.0", fmt.Appendf(nil, "copy %d\n", i), 0o600)
			if err != nil {
				t.Error(err)
				return
			}
			names[i] = p
		})
	}
	wg.Wait()
	seen := map[string]bool{}
	for i, p := range names {
		if seen[p] {
			t.Errorf("backup %s was taken twice", p)
		}
		seen[p] = true
		if got, _ := os.ReadFile(p); string(got) != fmt.Sprintf("copy %d\n", i) {
			t.Errorf("%s holds %q, want copy %d", p, got, i)
		}
	}
}
