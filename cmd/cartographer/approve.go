package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// cmdApprove manages client-local, hash-bound approvals for MCP descriptors.
// It never sends an approval to the server.
func cmdApprove(args []string) int {
	revoke := false
	if len(args) > 0 && args[0] == "revoke" {
		revoke, args = true, args[1:]
	}
	if len(args) == 0 || args[0] != "mcp" {
		fmt.Fprintln(os.Stderr, "Usage: cartographer approve [revoke] mcp <name> [--kb <kb>] [--yes]")
		return 2
	}
	name, kbName, yes, ok := parseApproveMCPArgs(args[1:])
	if !ok {
		fmt.Fprintln(os.Stderr, "Usage: cartographer approve [revoke] mcp <name> [--kb <kb>] [--yes]")
		return 2
	}
	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if revoke {
		if kbName == "" {
			matches := approvalSources(cfg, name)
			if len(matches) != 1 {
				fmt.Fprintln(os.Stderr, "Error: revoke is ambiguous; pass --kb <name>")
				return 2
			}
			kbName = matches[0]
		}
		cfg.RevokeMCP(kbName, name)
		if err := clientconfig.Save(dir, cfg); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
		fmt.Printf("revoked approval for MCP %s from KB %s; run cartographer sync to prune generated provider config\n", name, kbName)
		return 0
	}
	m, err := fetchMergedManifest(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	var matches []provisioning.Artifact
	for _, a := range m.Artifacts {
		if a.Kind == "mcp" && a.Name == name && (kbName == "" || a.Source == "kb:"+kbName) {
			matches = append(matches, a)
		}
	}
	if len(matches) == 0 {
		fmt.Fprintln(os.Stderr, "Error: MCP artifact is not advertised by the server")
		return 2
	}
	if len(matches) != 1 {
		fmt.Fprintln(os.Stderr, "Error: MCP artifact name is ambiguous; pass --kb <name>")
		return 2
	}
	a := matches[0]
	source := strings.TrimPrefix(a.Source, "kb:")
	files, err := provisioning.ReadArtifactFiles(a, nil, nil)
	if err != nil || len(files) != 1 {
		fmt.Fprintln(os.Stderr, "Error: invalid MCP descriptor")
		return 2
	}
	spec, err := provisioning.ParseMCPServerSpec(a.Name, files[0].Content)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	// The approved identity is transport-specific: the normalized endpoint for
	// http, the local command for stdio (D116).
	target, err := provisioning.MCPDescriptorTarget(spec)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	fmt.Printf("Approve MCP %s from KB %s\ntransport: %s\ntarget: %s\nhash: %s\n", name, source, spec.Type, target, shortHash(a.ContentHash))
	if !yes {
		if !isInteractive() {
			fmt.Fprintln(os.Stderr, "Error: non-interactive approval requires --yes")
			return 2
		}
		fmt.Print("Approve this exact descriptor? [y/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			fmt.Println("not approved")
			return 0
		}
	}
	if err := cfg.ApproveMCP(source, name, a.ContentHash, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	fmt.Printf("approved MCP %s from KB %s; run cartographer sync to materialize it\n", name, source)
	return 0
}

func parseApproveMCPArgs(args []string) (name, kb string, yes, ok bool) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--yes":
			yes = true
		case "--kb":
			if i+1 == len(args) || args[i+1] == "" {
				return "", "", false, false
			}
			i++
			kb = args[i]
		default:
			if strings.HasPrefix(args[i], "--kb=") {
				kb = strings.TrimPrefix(args[i], "--kb=")
				if kb == "" {
					return "", "", false, false
				}
			} else if strings.HasPrefix(args[i], "-") || name != "" {
				return "", "", false, false
			} else {
				name = args[i]
			}
		}
	}
	return name, kb, yes, name != ""
}

func approvalSources(cfg *clientconfig.Config, name string) []string {
	var sources []string
	for source, entries := range cfg.MCPApprovals {
		if _, ok := entries[name]; ok {
			sources = append(sources, source)
		}
	}
	return sources
}

func shortHash(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}
