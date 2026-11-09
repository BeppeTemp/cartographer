package kb

import (
	"reflect"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

func TestIngestTemplatesShape(t *testing.T) {
	t.Parallel()
	plan, ok := ParseTemplate("ingest-plan", IngestPlanTemplate())
	if !ok || plan.Type != IngestPlanType || !plan.OpenSections {
		t.Fatalf("plan = %+v ok=%v", plan, ok)
	}
	if !reflect.DeepEqual(plan.RequiredFields, []string{"status"}) ||
		!reflect.DeepEqual(plan.OptionalFields, []string{"sources", "maps", "timestamp"}) ||
		!reflect.DeepEqual(plan.FieldValues["status"], IngestPlanStatuses) {
		t.Errorf("plan = %+v", plan)
	}
	unit, ok := ParseTemplate("ingest-unit", IngestUnitTemplate())
	if !ok || unit.Type != IngestUnitType || !unit.OpenSections {
		t.Fatalf("unit = %+v ok=%v", unit, ok)
	}
	if !reflect.DeepEqual(unit.RequiredFields, []string{"source", "targets", "extract", "verify", "executor", "status"}) ||
		!reflect.DeepEqual(unit.OptionalFields, []string{"plan", "claimed_by", "claimed_at", "review_after", "waiting_on", "timestamp", "content_asset", "observed_group", "observed_revision", "observed_change", "observed_at"}) {
		t.Errorf("unit fields = %+v", unit)
	}
	if !reflect.DeepEqual(unit.FieldValues["status"], IngestUnitStatuses) || !reflect.DeepEqual(unit.FieldValues["executor"], IngestExecutors) ||
		!reflect.DeepEqual(unit.FieldValues["observed_change"], IngestObservedChanges) {
		t.Errorf("unit values = %v", unit.FieldValues)
	}
}

func TestIngestTypesAreKeyed(t *testing.T) {
	t.Parallel()
	for _, ty := range []string{IngestPlanType, IngestUnitType} {
		if !IsKeyedType(ty) {
			t.Errorf("%s not keyed", ty)
		}
	}
	if IsIngestUnitType("ingestunit") || IsIngestPlanType("ingestunit") {
		t.Error("match must be exact")
	}
}

func TestIngestUnitTemplateRenders(t *testing.T) {
	t.Parallel()
	fm, _, _ := okf.SplitFrontmatter(IngestUnitTemplate())
	if _, err := okf.ParseFrontmatter(fm); err != nil {
		t.Fatal(err)
	}
}
