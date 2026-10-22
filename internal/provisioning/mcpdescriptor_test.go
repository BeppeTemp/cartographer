package provisioning_test

// Direct unit coverage for internal/provisioning/mcpdescriptor.go (D116).

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

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
