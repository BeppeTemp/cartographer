package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// cmdApprove manages client-local, hash-bound approvals for MCP descriptors,
// hooks and artifacts shipping an executable file (D906, D1141). It never sends
// an approval to the server.
// Seams for the tests: the manifest fetch needs a server and the prompt a terminal.
var (
	approveManifest    = fetchMergedManifest
	approveInteractive = isInteractive
)

const approveUsage = "Usage: cartographer approve [revoke] <kind> <name> [--kb <kb>] [--yes]   (kind: mcp, hook, skill, agent)\n       cartographer approve --pending [--kb <kb>] [--yes]"

func cmdApprove(args []string) int {
	if i := slices.Index(args, "--pending"); i >= 0 {
		return cmdApprovePending(slices.Delete(slices.Clone(args), i, i+1))
	}
	revoke := false
	if len(args) > 0 && args[0] == "revoke" {
		revoke, args = true, args[1:]
	}
	if len(args) == 0 || !approvableKind(args[0]) {
		fmt.Fprintln(os.Stderr, approveUsage)
		return 2
	}
	kind := args[0]
	name, kbName, yes, ok := parseApproveMCPArgs(args[1:])
	if !ok {
		fmt.Fprintln(os.Stderr, approveUsage)
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
			matches := approvalSources(cfg, approvalEntryName(kind, name))
			if len(matches) != 1 {
				fmt.Fprintln(os.Stderr, "Error: revoke is ambiguous; pass --kb <name>")
				return 2
			}
			kbName = matches[0]
		}
		cfg.RevokeMCP(kbName, approvalEntryName(kind, name))
		if err := clientconfig.Save(dir, cfg); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
		if kind == "mcp" {
			fmt.Printf("revoked approval for MCP %s from KB %s; run cartographer sync to prune generated provider config\n", name, kbName)
		} else {
			fmt.Printf("revoked approval for %s %s from KB %s; what is already installed stays installed and registered (and keeps running) but is no longer refreshed: to remove it, delete its files and its registration in the client's settings by hand\n", kind, name, kbName)
		}
		return 0
	}
	m, err := approveManifest(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	var matches []provisioning.Artifact
	for _, a := range m.Artifacts {
		if a.Kind == kind && a.Name == name && (kbName == "" || a.Source == "kb:"+kbName) {
			matches = append(matches, a)
		}
	}
	if len(matches) == 0 {
		fmt.Fprintf(os.Stderr, "Error: %s artifact is not advertised by the server\n", kind)
		return 2
	}
	if len(matches) != 1 {
		fmt.Fprintf(os.Stderr, "Error: %s artifact name is ambiguous; pass --kb <name>\n", kind)
		return 2
	}
	a := matches[0]
	source := strings.TrimPrefix(a.Source, "kb:")
	if !provisioning.ArtifactRequiresApproval(a, provisioning.ApplyOptions{}) {
		fmt.Fprintf(os.Stderr, "Error: %s %s is installed without approval\n", kind, name)
		return 2
	}
	if kind == "mcp" {
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
	} else {
		// What is approved is what will run here: list every file, flag the
		// ones that are executable, so the person sees the scripts (D1141).
		files, err := provisioning.ReadArtifactFiles(a, nil, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Error: cannot read the artifact files:", err)
			return 2
		}
		fmt.Printf("Approve %s %s from KB %s\nhash: %s\n", kind, name, source, shortHash(a.ContentHash))
		printApprovalContent(os.Stdout, a, files)
	}
	if !yes {
		if !approveInteractive() {
			fmt.Fprintln(os.Stderr, "Error: non-interactive approval requires --yes")
			return 2
		}
		fmt.Print("Approve this exact content? [y/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			fmt.Println("not approved")
			return 0
		}
	}
	if err := cfg.ApproveMCP(source, approvalEntryName(kind, name), a.ContentHash, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	fmt.Printf("approved %s %s from KB %s; run cartographer sync to materialize it\n", kind, name, source)
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

// approvableKind lists the kinds a KB can ship that wait for an approval (D1141).
// A skill or an agent only does when it carries an executable file.
func approvableKind(k string) bool {
	switch k {
	case "mcp", "hook", "skill", "agent":
		return true
	}
	return false
}

// approvalEntryName is the name the approval is stored under in the source KB.
func approvalEntryName(kind, name string) string {
	return provisioning.ApprovalName(provisioning.Artifact{Kind: kind, Name: name})
}

// maxApprovalLines caps what is printed per file; the rest is counted, never hidden.
const maxApprovalLines = 200

// printApprovalContent shows what the person is about to let run: every file
// with its size and execute bit, then the content of the files that decide what
// runs (hook.json, every executable file, the frontmatter of a skill or agent
// that declares hooks or MCP servers) (D1141).
func printApprovalContent(w io.Writer, a provisioning.Artifact, files []provisioning.ArtifactFile) {
	fmt.Fprintln(w, "files:")
	for _, f := range files {
		mark := ""
		if f.Executable {
			mark = " (executable)"
		}
		fmt.Fprintf(w, "  %s  %d bytes%s\n", f.Path, len(f.Content), mark)
	}
	for _, f := range files {
		if !(f.Executable || (a.Kind == "hook" && f.Path == "hook.json") || provisioning.FrontmatterDeclaresCommands(f.Content)) {
			continue
		}
		lines := strings.Split(strings.TrimRight(string(f.Content), "\n"), "\n")
		fmt.Fprintf(w, "--- %s ---\n", f.Path)
		for i, l := range lines {
			if i == maxApprovalLines {
				fmt.Fprintf(w, "... %d more line(s) not shown; read the whole file before approving\n", len(lines)-i)
				break
			}
			fmt.Fprintln(w, l)
		}
	}
}

// cmdApprovePending lists every artifact sync would report as needs_approval and
// approves them all after one confirmation (D1166). It shows the hash of each,
// not its content: `cartographer approve <kind> <name>` is the form that prints
// the files first.
func cmdApprovePending(args []string) int {
	var kbName string
	yes := false
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--yes":
			yes = true
		case args[i] == "--kb" && i+1 < len(args) && args[i+1] != "":
			i++
			kbName = args[i]
		case strings.HasPrefix(args[i], "--kb=") && len(args[i]) > len("--kb="):
			kbName = strings.TrimPrefix(args[i], "--kb=")
		default:
			fmt.Fprintln(os.Stderr, approveUsage)
			return 2
		}
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
	m, err := approveManifest(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	opts := provisioning.ApplyOptions{ApprovedMCP: cfg.ApprovedMCPHashes()}
	var pending []provisioning.Artifact
	for _, a := range m.Artifacts {
		if kbName != "" && a.Source != "kb:"+kbName {
			continue
		}
		if provisioning.PendingApproval(a, opts) {
			pending = append(pending, a)
		}
	}
	if len(pending) == 0 {
		fmt.Println("nothing is waiting for approval")
		return 0
	}
	fmt.Printf("%d artifact(s) waiting for approval:\n", len(pending))
	for _, a := range pending {
		files, _ := provisioning.ReadArtifactFiles(a, nil, nil)
		fmt.Printf("  %s/%s [%s]  %s  hash %s\n", a.Kind, a.Name, a.Source, provisioning.ApprovalReason(a, files), shortHash(a.ContentHash))
	}
	if !yes {
		if !approveInteractive() {
			fmt.Fprintln(os.Stderr, "Error: non-interactive approval requires --yes")
			return 2
		}
		fmt.Print("Approve all of them, exactly as listed? [y/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			fmt.Println("not approved")
			return 0
		}
	}
	now := time.Now()
	for _, a := range pending {
		if err := cfg.ApproveMCP(strings.TrimPrefix(a.Source, "kb:"), provisioning.ApprovalName(a), a.ContentHash, now); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	fmt.Printf("approved %d artifact(s); run cartographer sync to materialize them\n", len(pending))
	return 0
}
