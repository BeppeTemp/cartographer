package defaults

import "testing"

func TestLocalEndpointDefaults(t *testing.T) {
	if DefaultPort != 39273 {
		t.Errorf("DefaultPort = %d, want 39273", DefaultPort)
	}
	if DefaultListenAddress != "127.0.0.1:39273" {
		t.Errorf("DefaultListenAddress = %q, want 127.0.0.1:39273", DefaultListenAddress)
	}
	if DefaultMCPURL != "http://localhost:39273/mcp" {
		t.Errorf("DefaultMCPURL = %q, want http://localhost:39273/mcp", DefaultMCPURL)
	}
}
