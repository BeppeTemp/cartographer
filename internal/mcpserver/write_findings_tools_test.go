package mcpserver

import (
	"os"
	"regexp"
	"sort"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// D353: the client-side write-findings hook matches the tools in
// provisioning.WriteFindingsTools. A write tool whose handler returns
// `findings` and is missing there would give findings nobody is forced to read;
// a name there that no handler emits would be a dead matcher entry.
func TestWriteFindingsToolsMatchTheHandlers(t *testing.T) {
	src, err := os.ReadFile("tools_write.go")
	if err != nil {
		t.Fatal(err)
	}
	name := regexp.MustCompile(`(?m)^\t\tName:\s+"(\w+)"`)
	emits := regexp.MustCompile(`result\["findings"\]\s*=|\bFindings:\s|\\nfindings:`)
	locs := name.FindAllSubmatchIndex(src, -1)
	var got []string
	for i, l := range locs {
		end := len(src)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		if emits.Match(src[l[0]:end]) {
			got = append(got, string(src[l[2]:l[3]]))
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
