package repodocs

import (
	"encoding/json"
	"strings"
	"testing"
)

// The image's entrypoint is `cartographer` with no CMD, and with no arguments
// and no terminal the binary prints its usage and exits 0 (cmd/cartographer
// main.go), so a registry entry whose package declares no arguments starts no
// MCP server (#1438). The entry must launch `serve --kb /kb` and ask the client
// for the KB folder it bind-mounts there.
func TestServerJSONLaunchesAServer(t *testing.T) {
	var sj struct {
		Packages []struct {
			RegistryType     string `json:"registryType"`
			RuntimeHint      string `json:"runtimeHint"`
			PackageArguments []struct {
				Type, Name, Value string
			} `json:"packageArguments"`
			RuntimeArguments []struct {
				Value     string `json:"value"`
				Variables map[string]struct {
					IsRequired bool `json:"isRequired"`
				} `json:"variables"`
			} `json:"runtimeArguments"`
			Transport struct {
				Type string `json:"type"`
			} `json:"transport"`
		} `json:"packages"`
	}
	if err := json.Unmarshal([]byte(readFile(t, repoRoot(t), "server.json")), &sj); err != nil {
		t.Fatalf("server.json: %v", err)
	}
	if len(sj.Packages) == 0 {
		t.Fatal("server.json declares no package")
	}
	for i, p := range sj.Packages {
		if p.Transport.Type != "stdio" {
			continue
		}
		var args []string
		for _, a := range p.PackageArguments {
			args = append(args, a.Name, a.Value)
		}
		if got := strings.TrimSpace(strings.Join(args, " ")); got != "serve --kb /kb" {
			t.Errorf("packages[%d]: stdio package arguments are %q, want `serve --kb /kb`: without them the image prints its usage and exits 0 (#1438)", i, got)
		}
		if p.RegistryType == "oci" && p.RuntimeHint != "docker" {
			t.Errorf("packages[%d]: runtimeHint is %q, want docker", i, p.RuntimeHint)
		}
		mounted := false
		for _, r := range p.RuntimeArguments {
			if !strings.Contains(r.Value, "dst=/kb") {
				continue
			}
			for name, v := range r.Variables {
				if strings.Contains(r.Value, "{"+name+"}") && v.IsRequired {
					mounted = true
				}
			}
		}
		if p.RegistryType == "oci" && !mounted {
			t.Errorf("packages[%d]: no runtime argument bind-mounts a required, user-supplied variable onto /kb", i)
		}
	}
}
