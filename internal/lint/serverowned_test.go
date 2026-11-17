package lint

import (
	"fmt"
	"testing"
)

// #2226: the Source lifecycle fields are the server's: no proposal for them,
// and a narrow contract never refuses their documented values.
func TestMissingValueContract_SkipsServerOwnedFields(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "sources/_map.md", "---\ntype: Map\nkind: journal\ntitle: S\n---\n# S\n")
	for i := 0; i < 6; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("sources/s%d.md", i),
			fmt.Sprintf("---\ntype: Source\ntitle: S%d\ningest_status: pending\nsource_shape: system\n---\nx\n", i))
	}
	if f := findingFor(observed(t, k), "sources/_map.md", "missing_value_contract"); f != nil {
		t.Errorf("proposal for server-owned fields: %s", f.Message)
	}
}

func TestInvalidFieldValue_ServerOwnedFieldNeverRefused(t *testing.T) {
	t.Parallel()
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "sources/_map.md", "---\ntype: Map\nkind: journal\ntitle: S\n"+
		"field_values.Source.ingest_status: [pending]\nfield_values.Source.source_shape: [system]\n---\n# S\n")
	has := func(content string) bool {
		for _, f := range CheckConcept(k, "sources/a", content) {
			if f.Check == "invalid_field_value" {
				return true
			}
		}
		return false
	}
	if has("---\ntype: Source\ntitle: A\ningest_status: ingested\nsource_shape: feed\n---\nx\n") {
		t.Error("documented value refused")
	}
	if !has("---\ntype: Source\ntitle: A\ningest_status: bogus\n---\nx\n") {
		t.Error("a value outside the vocabulary must still be refused")
	}
}
