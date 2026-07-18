package provisioning

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteInstructionsBlock_ReplacesLegacyLocalizedMarker pins the
// legacy-compat behavior of replaceBetweenMarkers: files written by older
// versions carry the begin marker with an Italian tail after the em dash.
// Recognition must go through instructionsBlockBeginPrefix, or the next sync
// would append a second block instead of replacing the existing one.
func TestWriteInstructionsBlock_ReplacesLegacyLocalizedMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "CLAUDE.md")
	legacy := "# My notes\n\n" +
		"<!-- cartographer:instructions:begin — blocco gestito da Cartographer, non modificare a mano -->\n" +
		"old body\n" +
		"<!-- cartographer:instructions:end -->\n"
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := writeInstructionsBlock(path, "new body"); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if got := strings.Count(content, instructionsBlockBeginPrefix); got != 1 {
		t.Errorf("begin marker count = %d, want 1 (block duplicated?):\n%s", got, content)
	}
	if !strings.Contains(content, instructionsBlockBegin+"\nnew body\n") {
		t.Errorf("legacy block not replaced with the new marker+body:\n%s", content)
	}
	if strings.Contains(content, "blocco gestito") {
		t.Errorf("legacy Italian marker still present:\n%s", content)
	}
	if !strings.Contains(content, "# My notes") {
		t.Errorf("user content outside the block was lost:\n%s", content)
	}
}
