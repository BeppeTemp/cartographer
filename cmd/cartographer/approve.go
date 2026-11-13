package main

import (
	"bufio"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// cmdApprove manages client-local, hash-bound approvals for MCP descriptors,
// hooks and artifacts shipping an executable file (D906, D1141). It never sends
// an approval to the server.
// Seams for the tests: the manifest fetch needs a server and the prompt a terminal.
var (
	approveManifest    = fetchMergedManifest
	approveInteractive = isInteractive
)

const approveUsage = "Usage: cartographer approve [revoke] <kind> <name> [--kb <kb>] [--yes] [--sync]   (kind: mcp, hook, skill, agent)\n       cartographer approve --pending [--kb <kb>] [--yes] [--sync]\n  --sync runs cartographer sync right after the approval (or the revoke) is written"

func cmdApprove(args []string) int {
	if hasHelpFlag(args) {
		fmt.Fprintln(os.Stdout, approveUsage)
		return 0
	}
	// --sync is stripped here so both forms and revoke share it; a refused or
	// declined approval returns before it is looked at.
	runSync := false
	if i := slices.Index(args, "--sync"); i >= 0 {
		runSync, args = true, slices.Delete(slices.Clone(args), i, i+1)
	}
	then := func(code int) int {
		if code == 0 && runSync {
			return syncFn(nil)
		}
		return code
	}
	if i := slices.Index(args, "--pending"); i >= 0 {
		code, approved := cmdApprovePending(slices.Delete(slices.Clone(args), i, i+1))
		if !approved {
			// Nothing pending, a declined prompt or an error: no sync.
			return code
		}
		return then(code)
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
		// "Nothing to revoke" and "ambiguous" are different answers, and a
		// revoke of an approval that was never given must not claim success.
		matches := approvalSources(cfg, approvalEntryName(kind, name))
		switch {
		case kbName != "" && !slices.Contains(matches, kbName):
			fmt.Fprintf(os.Stderr, "Error: no approval for %s %s from KB %s to revoke\n", kind, name, kbName)
			return 2
		case kbName == "" && len(matches) == 0:
			fmt.Fprintf(os.Stderr, "Error: no approval for %s %s to revoke\n", kind, name)
			return 2
		case kbName == "" && len(matches) > 1:
			fmt.Fprintf(os.Stderr, "Error: revoke is ambiguous: %s %s is approved from %s; pass --kb <name>\n", kind, name, strings.Join(matches, ", "))
			return 2
		case kbName == "":
			kbName = matches[0]
		}
		if err := withClientConfig(dir, func(c *clientconfig.Config) error {
			c.RevokeMCP(kbName, approvalEntryName(kind, name))
			return nil
		}); err != nil {
			fmt.Fprintln(os.Stderr, "Error:", err)
			return 2
		}
		if kind == "mcp" {
			fmt.Printf("revoked approval for MCP %s from KB %s; run cartographer sync to prune generated provider config\n", name, kbName)
		} else {
			fmt.Printf("revoked approval for %s %s from KB %s; what is already installed stays installed and registered (and keeps running) but is no longer refreshed: to remove it, delete its files and its registration in the client's settings by hand\n", kind, name, kbName)
		}
		return then(0)
	}
	m, err := approveManifest(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	var matches []contract.Artifact
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
	if err := printApprovalPreview(os.Stdout, a, previousApproval(cfg, source, approvalEntryName(kind, name))); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
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
	content := approvalContent(a)
	// Reloaded under the lock: the preview and the prompt above can take as
	// long as the operator does, and a sync may have saved meanwhile (D172).
	if err := withClientConfig(dir, func(c *clientconfig.Config) error {
		return c.ApproveMCPContent(source, approvalEntryName(kind, name), a.ContentHash, content, time.Now())
	}); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	fmt.Printf("approved %s %s from KB %s; run cartographer sync to materialize it\n", kind, escapeControl(name), escapeControl(source))
	return then(0)
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

// previousApproval is the stored approval of one source/name, nil when none.
func previousApproval(cfg *clientconfig.Config, source, name string) *clientconfig.MCPApproval {
	if p, ok := cfg.MCPApprovals[source][name]; ok {
		return &p
	}
	return nil
}

// approvalContent is the text an approval stores for the next diff: the
// descriptor of an MCP artifact, nothing for the kinds whose preview shows files.
func approvalContent(a contract.Artifact) string {
	if a.Kind != "mcp" {
		return ""
	}
	files, err := contract.ReadArtifactFiles(a, nil, nil)
	if err != nil || len(files) != 1 {
		return ""
	}
	return string(files[0].Content)
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
	return provisioning.ApprovalName(contract.Artifact{Kind: kind, Name: name})
}

// printApprovalPreview prints what approving a would let run, the one thing both
// the single and the --pending forms show before they ask (D1141): for an MCP
// descriptor its transport and target, for anything else every file with its
// size and execute bit and the content of the files that decide what runs.
// Everything the server chose (names, source, paths, target) goes through
// escapeControl.
func printApprovalPreview(w io.Writer, a contract.Artifact, prev *clientconfig.MCPApproval) error {
	source := escapeControl(strings.TrimPrefix(a.Source, "kb:"))
	name := escapeControl(a.Name)
	files, err := contract.ReadArtifactFiles(a, nil, nil)
	if a.Kind == "mcp" {
		if err != nil || len(files) != 1 {
			return fmt.Errorf("invalid MCP descriptor")
		}
		spec, err := contract.ParseMCPServerSpec(a.Name, files[0].Content)
		if err != nil {
			return err
		}
		// The approved identity is transport-specific: the normalized endpoint for
		// http, the local command for stdio (D116).
		target, err := provisioning.MCPDescriptorTarget(spec)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "Approve MCP %s from KB %s\ntransport: %s\ntarget: %s\nhash: %s\n", name, source, escapeControl(string(spec.Type)), escapeControl(target), shortHash(a.ContentHash))
		// What the process starts with beyond the command line: the constants the
		// descriptor sets (NODE_OPTIONS, LD_PRELOAD, PATH can change what runs, so
		// names and values are shown) and the variables it reads from the
		// environment of the client.
		for _, k := range slices.Sorted(maps.Keys(spec.EnvLiteral)) {
			fmt.Fprintf(w, "env_literal: %s=%s\n", escapeControl(k), escapeControl(spec.EnvLiteral[k]))
		}
		if names := spec.EnvRefNames(); len(names) > 0 {
			fmt.Fprintf(w, "reads: %s\n", escapeControl(strings.Join(names, ", ")))
		}
		if spec.HasSecretRef() {
			// Names only, never a value: the person approves knowing this
			// descriptor is started through the launcher with KB secrets.
			var refs []string
			for _, m := range []map[string]string{spec.Env, spec.Headers} {
				for _, v := range m {
					for _, r := range contract.SecretRefs(v) {
						refs = append(refs, r.Concept+"#"+r.Name)
					}
				}
			}
			slices.Sort(refs)
			fmt.Fprintf(w, "secrets: %s\nlauncher: yes\n", escapeControl(strings.Join(refs, ", ")))
		}
		printApprovalDiff(w, prev, a.ContentHash, files[0].Content)
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot read the artifact files: %w", err)
	}
	fmt.Fprintf(w, "Approve %s %s from KB %s\nhash: %s\n", escapeControl(a.Kind), name, source, shortHash(a.ContentHash))
	printApprovalContent(w, a, files)
	return nil
}

// escapeControl makes text the server chose safe to print on a terminal: any
// rune that is not printable (a newline that forges a line, an ESC that
// rewrites the screen, a bidi override or zero-width character that makes a
// command read as another) is shown as its escape, tab excepted. Printable
// text, accented letters included, is untouched.
func escapeControl(s string) string { return client.Escape(s) }

// hasControlRune reports whether s holds a control character (C0, DEL, C1): a
// name or label a server sends is refused when it does, and printed escaped
// (escapeControl) wherever it is shown.
func hasControlRune(s string) bool { return client.HasControl(s) }

// maxApprovalLines caps what is printed per file; the rest is counted, never hidden.
const maxApprovalLines = 200

// printApprovalContent shows what the person is about to let run: every file
// with its size and execute bit, then the content of the files that decide what
// runs (hook.json, every executable file, the frontmatter of a skill or agent
// that declares hooks or MCP servers) (D1141).
func printApprovalContent(w io.Writer, a contract.Artifact, files []contract.ArtifactFile) {
	printApprovalFiles(w, a, files, false)
}

// printApprovalFiles is printApprovalContent; all also prints the content of the
// files that do not decide what runs (a skill or agent body), for an update that
// asks consent for a changed text.
func printApprovalFiles(w io.Writer, a contract.Artifact, files []contract.ArtifactFile, all bool) {
	fmt.Fprintln(w, "files:")
	for _, f := range files {
		mark := ""
		if f.Executable {
			mark = " (executable)"
		}
		fmt.Fprintf(w, "  %s  %d bytes%s\n", escapeControl(f.Path), len(f.Content), mark)
	}
	for _, f := range files {
		if !(all || f.Executable || (a.Kind == "hook" && f.Path == "hook.json") || provisioning.FrontmatterDeclaresCommands(f.Content)) {
			continue
		}
		lines := strings.Split(strings.TrimRight(string(f.Content), "\n"), "\n")
		fmt.Fprintf(w, "--- %s ---\n", escapeControl(f.Path))
		for i, l := range lines {
			if i == maxApprovalLines {
				fmt.Fprintf(w, "... %d more line(s) not shown; read the whole file before approving\n", len(lines)-i)
				break
			}
			fmt.Fprintln(w, escapeControl(l))
		}
	}
}

// cmdApprovePending lists every artifact sync would report as needs_approval and
// approves them all after one confirmation (D1166). It reports whether it wrote an
// approval: only then does --sync run. Each one is shown exactly as
// the single form shows it (printApprovalPreview, D1141). An MCP descriptor is
// left out and named: it receives the agent's data and is approved one by one.
func cmdApprovePending(args []string) (code int, approved bool) {
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
			return 2, false
		}
	}
	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2, false
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2, false
	}
	m, err := approveManifest(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2, false
	}
	opts := provisioning.ApplyOptions{ApprovedMCP: cfg.ApprovedMCPHashes()}
	var pending, mcps []contract.Artifact
	for _, a := range m.Artifacts {
		if kbName != "" && a.Source != "kb:"+kbName {
			continue
		}
		if !provisioning.PendingApproval(a, opts) {
			continue
		}
		if a.Kind == "mcp" {
			mcps = append(mcps, a)
		} else {
			pending = append(pending, a)
		}
	}
	for _, a := range mcps {
		fmt.Printf("MCP %s [%s] is not approved by --pending: run `cartographer approve mcp %s --kb %s` to review and approve it\n",
			escapeControl(a.Name), escapeControl(a.Source), escapeControl(a.Name), escapeControl(strings.TrimPrefix(a.Source, "kb:")))
	}
	if len(pending) == 0 {
		fmt.Println("nothing is waiting for approval")
		return 0, false
	}
	fmt.Printf("%d artifact(s) waiting for approval:\n\n", len(pending))
	for _, a := range pending {
		if err := printApprovalPreview(os.Stdout, a, nil); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s %s: %v\n", escapeControl(a.Kind), escapeControl(a.Name), err)
			return 2, false
		}
		fmt.Println()
	}
	if !yes {
		if !approveInteractive() {
			fmt.Fprintln(os.Stderr, "Error: non-interactive approval requires --yes")
			return 2, false
		}
		fmt.Print("Approve all of them, exactly as shown above? [y/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer != "y" && answer != "yes" {
			fmt.Println("not approved")
			return 0, false
		}
	}
	now := time.Now()
	if err := withClientConfig(dir, func(c *clientconfig.Config) error {
		for _, a := range pending {
			if err := c.ApproveMCP(strings.TrimPrefix(a.Source, "kb:"), provisioning.ApprovalName(a), a.ContentHash, now); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2, false
	}
	fmt.Printf("approved %d artifact(s); run cartographer sync to materialize them\n", len(pending))
	return 0, true
}
