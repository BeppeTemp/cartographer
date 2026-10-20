package kb

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// A contract names a field under the standard name the page repair renames it
// to; forbidding a synonym and the date-shaped words are left as written.
func TestCanonicalizeContractFields(t *testing.T) {
	t.Parallel()
	fm, err := okf.ParseFrontmatter("type: Map\nrequired_fields.*: [updated, owner]\nrequired_fields.Note: [date]\nforbidden_fields: [state]\nfield_values.*.state: [open]\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := ContractSynonymKeys(fm); strings.Join(got, ",") != "field_values.*.state,required_fields.*" {
		t.Fatalf("synonym keys = %v", got)
	}
	canonicalizeContractFields(fm)
	out := fm.Serialize()
	for _, want := range []string{"required_fields.*: [owner, timestamp]", "required_fields.Note: [date]", "forbidden_fields: [state]", "field_values.*.status: [open]"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "updated") {
		t.Errorf("updated survives:\n%s", out)
	}
}
