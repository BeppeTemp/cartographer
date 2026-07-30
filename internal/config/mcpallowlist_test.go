package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMCPAllowlistValidation(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"valid":       "kbs:\n  - path: /tmp/kb\n    mcp_allowlist:\n      - name: tools\n        transport: http\n        target: https://example.com/mcp\n",
		"duplicate":   "kbs:\n  - path: /tmp/kb\n    mcp_allowlist:\n      - {name: tools, transport: http, target: https://example.com/a}\n      - {name: tools, transport: http, target: https://example.com/b}\n",
		"credentials": "kbs:\n  - path: /tmp/kb\n    mcp_allowlist:\n      - {name: tools, transport: http, target: https://u:p@example.com/mcp}\n",
		"fragment":    "kbs:\n  - path: /tmp/kb\n    mcp_allowlist:\n      - {name: tools, transport: http, target: https://example.com/mcp#x}\n",
	} {
		path := filepath.Join(dir, name+".yaml")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if name == "valid" && err != nil {
			t.Errorf("valid: %v", err)
		}
		if name != "valid" && err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
