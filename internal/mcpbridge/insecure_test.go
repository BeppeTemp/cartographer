package mcpbridge

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

func TestBridgeEndpointGate(t *testing.T) {
	t.Setenv("CARTOGRAPHER_ALLOW_INSECURE_HTTP", "")
	mk := func(url string, allow bool) *Bridge {
		return &Bridge{Cfg: &clientconfig.Config{ServerURL: url, ServerRoutedPath: "/mcp", AllowInsecureHTTP: allow}}
	}
	if _, err := mk("http://10.0.0.5:39273/mcp", false).endpoint(Scope{}); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("remote http without opt-in: err = %v, want a refusal", err)
	}
	if _, err := mk("http://10.0.0.5:39273/mcp", true).endpoint(Scope{}); err != nil {
		t.Errorf("remote http with opt-in: %v", err)
	}
	if _, err := mk("http://127.0.0.1:39273/mcp", false).endpoint(Scope{}); err != nil {
		t.Errorf("loopback http: %v", err)
	}
	if _, err := mk("https://example.com/mcp", false).endpoint(Scope{}); err != nil {
		t.Errorf("https: %v", err)
	}
}
