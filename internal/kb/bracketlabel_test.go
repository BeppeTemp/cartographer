package kb

import (
	"reflect"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

func TestExtractLinks_BracketedLabel(t *testing.T) {
	body := "- [Issue #100 — [RFC] foo](x.md)\n- [plain](y.md)\n![img [a]](pic.png)\n"
	got := ExtractLinks(body, "m/index.md")
	want := []okf.ConceptID{"m/x", "m/y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractLinks = %v, want %v", got, want)
	}
}
