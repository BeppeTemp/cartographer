package settingschema_test

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/settingschema"
)

var update = flag.Bool("update", false, "rewrite testdata/descriptions.tsv from the schemas once the history checks pass")

const listFile = "testdata/descriptions.tsv"

// registered is a settings file of the repository: its key table, the registry
// of the keys it lost, and where both are written.
type registered struct {
	name      string
	schema    settingschema.Schema
	retired   settingschema.Registry
	keysIn    string
	retiredIn string
}

func registries() []registered {
	return []registered{
		{"client", clientconfig.Schema, clientconfig.Retired, "internal/clientconfig/schema.go", "Retired in internal/clientconfig/realign.go"},
		{"server", config.Schema, config.Retired, "internal/config/schema.go", "Retired in internal/config/schema.go"},
		{"kb", config.KBSettingsSchema(), kb.SettingsRetired, "KBKeys in internal/config/schema.go", "SettingsRetired in internal/kb/settings.go"},
	}
}

// A comment the schema wrote is told from a person's by its text, so the text a
// key had in every release has to be known (settingschema, Key.Previous and
// Removed.Description). testdata/descriptions.tsv lists each key with the
// description it has now. Changing a description without keeping the old one in
// Previous, or taking a key out of the table without recording its last
// description in the registry of removed keys, fails here with the line to add:
// otherwise every file written before the change keeps the old comment for good,
// and the comment of a removed key lands on its neighbour.
func TestDescriptionsAreRecordedWhenTheyChange(t *testing.T) {
	list := readList(t)
	var problems, stale []string
	var lines []string
	for _, r := range registries() {
		live := map[string]bool{}
		for _, k := range r.schema.Keys {
			live[k.Path] = true
			lines = append(lines, listLine(r.name, k.Path, k.Description))
			if strings.ContainsAny(k.Description, "\t\n") {
				problems = append(problems, fmt.Sprintf("%s %s: the description holds a tab or a newline", r.name, k.Path))
			}
			if slices.Contains(k.Previous, k.Description) {
				problems = append(problems, fmt.Sprintf("%s %s: Previous holds the current description; remove it from %s", r.name, k.Path, r.keysIn))
			}
			if hasDuplicate(k.Previous) {
				problems = append(problems, fmt.Sprintf("%s %s: Previous holds a text twice (%s)", r.name, k.Path, r.keysIn))
			}
			old, known := list[r.name+"\t"+k.Path]
			switch {
			case !known:
				stale = append(stale, fmt.Sprintf("%s %s is not in the list", r.name, k.Path))
			case old != k.Description && !slices.Contains(k.Previous, old):
				problems = append(problems, fmt.Sprintf("%s %s: the description changed and the old text is not in Previous. Add it to the key in %s:\n\t\tPrevious: []string{%q},", r.name, k.Path, r.keysIn, old))
			case old != k.Description:
				// Recorded, but the list must follow, or the next change would be
				// judged against a text that is two releases old.
				stale = append(stale, fmt.Sprintf("%s %s changed", r.name, k.Path))
			}
		}
		for _, e := range r.retired.Removed {
			if len(e.Previous) > 0 && e.Description == "" {
				problems = append(problems, fmt.Sprintf("%s %s: Previous without a Description (%s)", r.name, e.Path, r.retiredIn))
			}
			if e.Description != "" && slices.Contains(e.Previous, e.Description) {
				problems = append(problems, fmt.Sprintf("%s %s: Previous holds the last description (%s)", r.name, e.Path, r.retiredIn))
			}
		}
		for key, old := range list {
			name, path, _ := strings.Cut(key, "\t")
			if name != r.name || live[path] {
				continue
			}
			recorded := false
			for _, e := range r.retired.Removed {
				if e.Path == path && (e.Description == old || slices.Contains(e.Previous, old)) {
					recorded = true
				}
			}
			if !recorded {
				problems = append(problems, fmt.Sprintf("%s %s is no longer in the table and its last description is not recorded. Add it to its entry in %s:\n\t\tDescription: %q,", r.name, path, r.retiredIn, old))
			}
			stale = append(stale, fmt.Sprintf("%s %s left the table", r.name, path))
		}
	}
	if len(problems) > 0 {
		t.Fatalf("%d problem(s):\n- %s", len(problems), strings.Join(problems, "\n- "))
	}
	if len(stale) == 0 {
		return
	}
	slices.Sort(stale)
	if !*update {
		t.Fatalf("testdata/descriptions.tsv is out of date (%s). The history is recorded; refresh the list:\n\tgo test ./internal/settingschema -run TestDescriptionsAreRecordedWhenTheyChange -update", strings.Join(stale, "; "))
	}
	if err := os.WriteFile(listFile, []byte(listHeader+strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

const listHeader = `# Every settings key and the description it has now: <file> TAB <path> TAB <description>.
# TestDescriptionsAreRecordedWhenTheyChange compares it with the schemas. Do not edit it by hand:
# change the description in the schema, keep the old text in Previous, then run
#   go test ./internal/settingschema -run TestDescriptionsAreRecordedWhenTheyChange -update
`

func listLine(name, path, description string) string {
	return name + "\t" + path + "\t" + description
}

func hasDuplicate(ss []string) bool {
	seen := map[string]bool{}
	for _, s := range ss {
		if seen[s] {
			return true
		}
		seen[s] = true
	}
	return false
}

// readList returns the list as a map from "<file>\t<path>" to the description.
func readList(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(listFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			t.Fatalf("%s: malformed line %q", listFile, line)
		}
		out[parts[0]+"\t"+parts[1]] = parts[2]
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
