package lint

import "testing"

// A lint_judgement item keeps the KBs its finding names, so the server can
// withhold it from a caller who cannot read them.
func TestLintJudgementItemKeepsOtherKBs(t *testing.T) {
	t.Parallel()
	items := lintJudgementItems([]Finding{{Path: "ops/p.md", Check: "stale_open", Message: "m", OtherKBs: []string{"kb-b"}}}, nil)
	if len(items) != 1 || len(items[0].OtherKBs) != 1 || items[0].OtherKBs[0] != "kb-b" {
		t.Fatalf("items = %+v", items)
	}
}
