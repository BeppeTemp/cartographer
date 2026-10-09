package mcpserver

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// D353: the client-side write-findings hook matches the tools in
// provisioning.WriteFindingsTools. A write tool whose handler returns
// `findings` and is missing there would give findings nobody is forced to read;
// a name there that no handler emits would be a dead matcher entry.
func TestWriteFindingsToolsMatchTheHandlers(t *testing.T) {
	// Every tools_*.go file: a write tool may live outside tools_write.go
	// (concept_archive is in tools_archive.go).
	files, err := filepath.Glob("tools_*.go")
	if err != nil {
		t.Fatal(err)
	}
	name := regexp.MustCompile(`(?m)^\t\tName:\s+"(\w+)"`)
	emits := regexp.MustCompile(`result\["findings"\]\s*=|\bFindings:\s|\\nfindings:|applyConceptMoves\(`)
	var got []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		locs := name.FindAllSubmatchIndex(src, -1)
		for i, l := range locs {
			end := len(src)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			if emits.Match(src[l[0]:end]) {
				got = append(got, string(src[l[2]:l[3]]))
			}
		}
	}
	want := append([]string(nil), provisioning.WriteFindingsTools...)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("tools whose handlers emit findings = %v, provisioning.WriteFindingsTools = %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("tools whose handlers emit findings = %v, provisioning.WriteFindingsTools = %v", got, want)
		}
	}
}
