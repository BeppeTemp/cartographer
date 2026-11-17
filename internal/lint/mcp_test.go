package lint

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// A descriptor sync would leave out is a finding; a valid one is not (#2219).
func TestCheckMCP_FlagsDescriptorWithoutType(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "mcp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("notype.json", `{"url":"https://example.com/mcp"}`)
	write("good.json", `{"type":"http","url":"https://example.com/mcp"}`)
	got := checkMCP(&kb.KB{Root: root})
	if len(got) != 1 || got[0].Check != "mcp_invalid" || got[0].Path != "mcp/notype.json" {
		t.Fatalf("findings = %+v, want one mcp_invalid on mcp/notype.json", got)
	}
}
