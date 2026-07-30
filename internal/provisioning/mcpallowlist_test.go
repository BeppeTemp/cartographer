package provisioning_test

// Direct unit coverage for internal/provisioning/mcpallowlist.go's stdio
// branch (D116): the http branch is already exercised indirectly through the
// BuildManifest-level tests in provisioning_mcp_test.go.

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func TestValidateMCPAllowlist_Stdio(t *testing.T) {
	for name, entries := range map[string][]provisioning.MCPAllowlistEntry{
		"bare-command":     {{Name: "x", Transport: "stdio", Target: "tool"}},
		"absolute-command": {{Name: "x", Transport: "stdio", Target: "/usr/local/bin/tool"}},
	} {
		t.Run("valid/"+name, func(t *testing.T) {
			if err := provisioning.ValidateMCPAllowlist(entries); err != nil {
				t.Fatalf("rejected valid stdio entry: %v", err)
			}
		})
	}

	for name, entries := range map[string][]provisioning.MCPAllowlistEntry{
		"relative-with-separator": {{Name: "x", Transport: "stdio", Target: "bin/tool"}},
		"unclean-absolute":        {{Name: "x", Transport: "stdio", Target: "/usr/local/../bin/tool"}},
		"empty-command":           {{Name: "x", Transport: "stdio", Target: " "}},
		"unsupported-transport":   {{Name: "x", Transport: "socket", Target: "tool"}},
	} {
		t.Run("invalid/"+name, func(t *testing.T) {
			if err := provisioning.ValidateMCPAllowlist(entries); err == nil {
				t.Fatalf("accepted invalid entries: %+v", entries)
			}
		})
	}
}

func TestMCPAllowed_StdioExactCommandIdentity(t *testing.T) {
	entries := []provisioning.MCPAllowlistEntry{{Name: "tools", Transport: "stdio", Target: "tool"}}

	if !provisioning.MCPAllowed(entries, "tools", provisioning.MCPServerSpec{Type: "stdio", Command: "tool"}) {
		t.Fatal("exact stdio command identity was denied")
	}
	if provisioning.MCPAllowed(entries, "tools", provisioning.MCPServerSpec{Type: "stdio", Command: "other-tool"}) {
		t.Fatal("mismatched stdio command was allowed")
	}
	if provisioning.MCPAllowed(entries, "tools", provisioning.MCPServerSpec{Type: "http", URL: "https://example.com/mcp"}) {
		t.Fatal("transport mismatch (http spec vs stdio entry) was allowed")
	}
	if provisioning.MCPAllowed(nil, "tools", provisioning.MCPServerSpec{Type: "stdio", Command: "tool"}) {
		t.Fatal("empty allow-list must deny by default")
	}
}

func TestMCPDescriptorTarget(t *testing.T) {
	target, err := provisioning.MCPDescriptorTarget(provisioning.MCPServerSpec{Type: "http", URL: "https://Example.com/mcp"})
	if err != nil || target != "https://example.com/mcp" {
		t.Fatalf("http target = %q, err = %v", target, err)
	}
	target, err = provisioning.MCPDescriptorTarget(provisioning.MCPServerSpec{Type: "stdio", Command: "tool"})
	if err != nil || target != "tool" {
		t.Fatalf("stdio target = %q, err = %v", target, err)
	}
	if _, err := provisioning.MCPDescriptorTarget(provisioning.MCPServerSpec{Type: "socket"}); err == nil {
		t.Fatal("expected error for unsupported transport")
	}
	if _, err := provisioning.MCPDescriptorTarget(provisioning.MCPServerSpec{Type: "stdio", Command: "bin/tool"}); err == nil || !strings.Contains(err.Error(), "bare executable name or absolute path") {
		t.Fatalf("invalid stdio command target error = %v", err)
	}
}
