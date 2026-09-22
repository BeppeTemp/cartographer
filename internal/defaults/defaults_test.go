package defaults

import "testing"

func TestLocalEndpointDefaults(t *testing.T) {
	if DefaultPort != 39273 {
		t.Errorf("DefaultPort = %d, want 39273", DefaultPort)
	}
	if DefaultListenAddress != "127.0.0.1:39273" {
		t.Errorf("DefaultListenAddress = %q, want 127.0.0.1:39273", DefaultListenAddress)
	}
	if DefaultMCPURL != "http://127.0.0.1:39273/mcp" {
		t.Errorf("DefaultMCPURL = %q, want http://127.0.0.1:39273/mcp", DefaultMCPURL)
	}
}

func TestMigrateLegacyMCPURL(t *testing.T) {
	cases := map[string]string{
		"http://localhost:39273/mcp":              "http://127.0.0.1:39273/mcp",
		"http://LOCALHOST:39273/mcp/homelab-wiki": "http://127.0.0.1:39273/mcp/homelab-wiki",
		"http://localhost:39273":                  "http://127.0.0.1:39273",
		"http://127.0.0.1:39273/mcp":              "http://127.0.0.1:39273/mcp",
		"http://localhost:9000/mcp":               "http://localhost:9000/mcp",
		"http://localhost:392730/mcp":             "http://localhost:392730/mcp",
		"https://localhost:39273/mcp":             "https://localhost:39273/mcp",
		"https://cartographer.example.com/mcp":    "https://cartographer.example.com/mcp",
		"":                                        "",
	}
	for in, want := range cases {
		if got := MigrateLegacyMCPURL(in); got != want {
			t.Errorf("MigrateLegacyMCPURL(%q) = %q, want %q", in, got, want)
		}
	}
}
