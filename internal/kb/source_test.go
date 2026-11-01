package kb

import (
	"reflect"
	"testing"
)

// D1072: the template source_register writes is built from the server's shape.
func TestSourceTemplateShape(t *testing.T) {
	t.Parallel()
	info, ok := ParseTemplate("source", SourceTemplate())
	if !ok {
		t.Fatal("not parseable")
	}
	if info.Type != SourceType || !info.OpenSections || len(info.Sections) != 0 {
		t.Fatalf("info = %+v", info)
	}
	if !reflect.DeepEqual(info.RequiredFields, []string{"source_kind", "ingest_status"}) {
		t.Errorf("required = %v", info.RequiredFields)
	}
	if !reflect.DeepEqual(info.FieldValues["ingest_status"], SourceIngestStatuses) {
		t.Errorf("ingest_status = %v", info.FieldValues["ingest_status"])
	}
}
