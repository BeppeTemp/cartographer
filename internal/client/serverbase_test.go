package client_test

import (
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
)

func TestServerBaseAndAtlasURL(t *testing.T) {
	for _, c := range []struct{ in, base, atlas string }{
		{"https://example.com/mcp", "https://example.com", "https://example.com/ui/"},
		{"https://example.com/mcp/", "https://example.com", "https://example.com/ui/"},
		{"https://example.com", "https://example.com", "https://example.com/ui/"},
		{"https://example.com:8443/prefix/mcp?x=1#f", "https://example.com:8443/prefix", "https://example.com:8443/prefix/ui/"},
		{"http://127.0.0.1:39273/mcp", "http://127.0.0.1:39273", "http://127.0.0.1:39273/ui/"},
		{"https://u:p@example.com/mcp", "https://u:p@example.com", "https://example.com/ui/"},
	} {
		u, err := client.ServerBase(c.in)
		if err != nil || u.String() != c.base {
			t.Errorf("ServerBase(%q) = %v, %v; want %q", c.in, u, err, c.base)
		}
		if got, err := client.AtlasURL(c.in); err != nil || got != c.atlas {
			t.Errorf("AtlasURL(%q) = %q, %v; want %q", c.in, got, err, c.atlas)
		}
	}
	if _, err := client.ServerBase("http://[::1"); err == nil {
		t.Error("an unparsable URL gave no error")
	}
}
