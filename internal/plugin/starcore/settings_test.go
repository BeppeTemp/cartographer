package starcore

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

func TestSettingsIntAcceptsDecodedNumbers(t *testing.T) {
	decl := map[string]core.Setting{"n": {Type: "int"}}
	for _, v := range []any{float64(7), json.Number("7"), 7, int64(7), uint64(7)} {
		s, err := newSettings(decl, map[string]any{"n": v})
		if err != nil || s.vals["n"].String() != "7" {
			t.Errorf("%T: %v, %v", v, s, err)
		}
	}
	for _, v := range []any{1.5, json.Number("1.5"), "7", math.Inf(1)} {
		if _, err := newSettings(decl, map[string]any{"n": v}); err == nil {
			t.Errorf("%T %v accepted", v, v)
		}
	}
}
