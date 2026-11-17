package client

import (
	"strings"
	"testing"
)

// #2217: WithKB on the routed endpoint sends the KB as a tool argument, never
// as ?kb= (the routed mount answers that with HTTP 400).
func TestWithKB_OnTheRoutedEndpointUsesAToolArgument(t *testing.T) {
	c := New("http://example.com/mcp/routed", "").WithKB("kb-a")
	u, err := c.requestURL()
	if err != nil || strings.Contains(u, "kb=") {
		t.Fatalf("url = %q, %v", u, err)
	}
	args, err := c.toolArgs(map[string]any{"x": 1})
	if m, ok := args.(map[string]any); err != nil || !ok || m["kb"] != "kb-a" {
		t.Fatalf("args = %v, %v", args, err)
	}
	if u, _ := New("http://example.com/mcp", "").WithKB("kb-a").requestURL(); !strings.Contains(u, "kb=kb-a") {
		t.Errorf("plain /mcp lost ?kb=: %q", u)
	}
}
