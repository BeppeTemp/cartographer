package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/service"
	"gopkg.in/yaml.v3"
)

// renamePlan is what the preflight determined, before anything moved. It
// carries both what the command will change and what it will only report:
// a KB's name is an identity, and most of the places that use it are
// configured out of band (D177).
type renamePlan struct {
	Old, New         string
	OldPath, NewPath string

	// ConfigPath is the server config to rewrite, "" when none exists.
	// EntryIndex is the kbs[] entry to rewrite, -1 when the KB is mounted
	// by discovery and has no entry at all.
	ConfigPath string
	EntryIndex int

	// DerivedPrefix means the tool prefix follows the KB name, so every
	// tool the agents see is about to be renamed with it.
	DerivedPrefix        bool
	OldPrefix, NewPrefix string

	// Scopes/SigningKey/Approvals are references to the old name this
	// command deliberately does NOT migrate: rewriting an operator's auth
	// configuration silently would be worse than telling them what to change.
	Scopes     []string
	SigningKey bool
	Approvals  bool
}

// cmdKBRename implements `cartographer kb rename <old> <new> [--data <dir>]
// [--config <path>] [--restart]`.
//
// It is offline and local: the data dir and the local server config, never a
// server, a client or a git remote. The git `origin` is untouched — renaming
// a local mount point is not renaming a repository. The scope is deliberately
// bounded to the pair (directory, server config), which is the only rollback
// that can honestly be implemented, with a preflight that reports every other
// reference it can find (D177).
func cmdKBRename(args []string) int {
	old, rest := splitPositional(args, "")
	newName, rest := splitPositional(rest, "")

	fs := flag.NewFlagSet("kb rename", flag.ExitOnError)
	dataFlag := fs.String("data", "", "KB data directory (default: the server config's data:, or "+defaultDataDir()+")")
	configFlag := fs.String("config", "", "Server config YAML to rewrite (default: the standard path)")
	localFlag := fs.Bool("local", false, "Act on the local data dir even though the client points at a remote server")
	restartFlag := fs.Bool("restart", false, "Restart the local service and wait until healthy after the rename")
	fs.Parse(rest)

	if old == "" || newName == "" || fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "Usage: cartographer kb rename <old> <new> [--data <dir>] [--config <path>] [--restart]")
		return 2
	}
	if code := checkLocalTarget(*dataFlag, *localFlag); code != 0 {
		return code
	}
	dataDir := *dataFlag
	if dataDir == "" {
		dataDir = resolveServerDataDir(*configFlag)
	}

	plan, err := planKBRename(old, newName, dataDir, *configFlag)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	printRenamePreflight(os.Stdout, plan)

	if err := applyKBRename(plan); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}

	fmt.Printf("KB %q renamed to %q at %s\n", plan.Old, plan.New, plan.NewPath)
	if plan.EntryIndex >= 0 {
		fmt.Printf("updated the kbs[] entry in %s\n", plan.ConfigPath)
	}
	fmt.Println("every connected client realigns its MCP entries on its next `cartographer sync`.")
	printPostCreateGuidanceFn(*configFlag, *restartFlag)
	return 0
}

// planKBRename validates the rename and collects what it affects, writing
// nothing. A KB is recognised by data/index.md — the same file kb.Open
// checks — read directly, because kb.Open self-migrates the repository's
// git-exclude entry and a preflight must not write into what it inspects.
func planKBRename(old, newName, dataDir, configPath string) (*renamePlan, error) {
	if err := validateKBName(old); err != nil {
		return nil, err
	}
	if err := validateKBName(newName); err != nil {
		return nil, err
	}
	if old == newName {
		return nil, fmt.Errorf("KB %q is already called that", old)
	}

	p := &renamePlan{
		Old: old, New: newName,
		OldPath:    filepath.Join(dataDir, old),
		NewPath:    filepath.Join(dataDir, newName),
		EntryIndex: -1,
	}
	if _, err := os.Stat(filepath.Join(p.OldPath, "data", "index.md")); err != nil {
		return nil, fmt.Errorf("%s is not an OKF KB (no data/index.md): nothing to rename", p.OldPath)
	}
	if _, err := os.Stat(p.NewPath); err == nil {
		return nil, fmt.Errorf("%s already exists", p.NewPath)
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	if configPath == "" {
		if resolved, err := service.ConfigPath(); err == nil {
			configPath = resolved
		}
	}
	if configPath == "" {
		return p, nil
	}
	if _, err := os.Stat(configPath); errors.Is(err, os.ErrNotExist) {
		return p, nil
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return nil, fmt.Errorf("server config %s cannot be read, refusing to rewrite it: %w", configPath, err)
	}
	p.ConfigPath = configPath

	if err := locateKBEntry(p, cfg); err != nil {
		return nil, err
	}
	describePrefixChange(p, cfg)
	p.Scopes = scopeRefs(cfg, old)
	p.SigningKey, p.Approvals = clientRefs(old)
	return p, nil
}

// locateKBEntry finds the kbs[] entry that mounts the KB, by resolved name or
// by path. More than one match is a refusal: guessing here silently detaches
// a KB from its configuration. NO match is not a refusal — a KB created by
// `kb create` or found by discovery legitimately has no entry (D151), and
// renaming its directory is then the whole job.
func locateKBEntry(p *renamePlan, cfg *config.Config) error {
	var matches []int
	for i, spec := range cfg.KBs {
		name := resolveKBName(spec, spec.Path)
		if name == p.Old || (spec.Path != "" && sameDir(spec.Path, p.OldPath)) {
			matches = append(matches, i)
		}
	}
	if len(matches) > 1 {
		var found []string
		for _, i := range matches {
			found = append(found, fmt.Sprintf("kbs[%d] (name=%q path=%q remote=%q)", i, cfg.KBs[i].Name, cfg.KBs[i].Path, cfg.KBs[i].Remote))
		}
		return fmt.Errorf("KB %q matches %d entries in %s: %s — make the intended one unambiguous first",
			p.Old, len(matches), p.ConfigPath, strings.Join(found, ", "))
	}
	if len(matches) == 1 {
		p.EntryIndex = matches[0]
	}
	return nil
}

// sameDir compares two directory paths after cleaning, tolerating a trailing
// separator. Symlinks are not resolved: the config's own spelling is what the
// server uses.
func sameDir(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

// describePrefixChange records whether the MCP tool prefix follows the name.
// An explicit kbs[].tool_prefix is preserved verbatim by the rewrite and
// nothing changes for the agents; a derived one renames every tool they see,
// which is the kind of consequence an operator must be told before it happens.
func describePrefixChange(p *renamePlan, cfg *config.Config) {
	var spec config.KBSpec
	if p.EntryIndex >= 0 {
		spec = cfg.KBs[p.EntryIndex]
	}
	if spec.ToolPrefix != "" || cfg.MCP.ToolPrefixMode != "kb-name" {
		return
	}
	p.DerivedPrefix = true
	p.OldPrefix = config.SanitizeToolPrefix(p.Old)
	p.NewPrefix = config.SanitizeToolPrefix(p.New)
}

// scopeRefs lists the configured tokens whose scopes name the old KB. They
// are reported, never rewritten: a token is a credential, and editing one on
// an operator's behalf is not this command's business.
func scopeRefs(cfg *config.Config, old string) []string {
	var refs []string
	prefix := "kb:" + old + ":"
	for i, tok := range cfg.Auth.Tokens {
		id := tok.ID
		if id == "" {
			id = fmt.Sprintf("tokens[%d]", i)
		}
		for _, s := range tok.Scopes {
			if strings.HasPrefix(s, prefix) {
				refs = append(refs, fmt.Sprintf("%s: %s", id, s))
			}
		}
	}
	for _, role := range cfg.Auth.Roles {
		for i, rule := range role.Rules {
			if rule.KB == old {
				refs = append(refs, fmt.Sprintf("role %q rule %d: kb: %s", role.Name, i, rule.KB))
			}
		}
	}
	return refs
}

// clientRefs reports whether this machine's client config pins a signing key
// or an MCP approval under the old KB name. Both are keyed by source KB and
// neither is migrated by a later sync.
func clientRefs(old string) (signingKey, approvals bool) {
	dir, err := clientconfig.TargetDir()
	if err != nil {
		return false, false
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		return false, false
	}
	_, signingKey = cfg.SigningKeys[old]
	_, approvals = cfg.MCPApprovals[old]
	return signingKey, approvals
}

// printRenamePreflight states what will change and what the operator must
// change themselves. The second list is the point: a rename that quietly left
// an operator with a token scoped to a name nothing answers to would be a
// worse outcome than a noisy one.
func printRenamePreflight(w io.Writer, p *renamePlan) {
	fmt.Fprintf(w, "renaming %s → %s\n", p.OldPath, p.NewPath)
	switch {
	case p.ConfigPath == "":
		fmt.Fprintln(w, "no server config found: only the directory changes")
	case p.EntryIndex < 0:
		fmt.Fprintf(w, "%s has no kbs[] entry for this KB (mounted by discovery): only the directory changes\n", p.ConfigPath)
	default:
		fmt.Fprintf(w, "%s: rewriting kbs[%d]\n", p.ConfigPath, p.EntryIndex)
	}
	if p.DerivedPrefix {
		fmt.Fprintf(w, "\nWARNING: the MCP tool prefix is derived from the KB name (mcp.tool_prefix_mode: kb-name)\n")
		fmt.Fprintf(w, "  every tool the agents see is renamed: %s… → %s…\n", p.OldPrefix, p.NewPrefix)
	}
	if len(p.Scopes) == 0 && !p.SigningKey && !p.Approvals {
		return
	}
	fmt.Fprintf(w, "\nNOT migrated by this command or by a later sync — update them yourself:\n")
	for _, s := range p.Scopes {
		fmt.Fprintf(w, "  auth scope in %s → %s\n", p.ConfigPath, s)
	}
	if p.SigningKey {
		fmt.Fprintf(w, "  signing_keys[%q] in this machine's .cartographer.yaml\n", p.Old)
	}
	if p.Approvals {
		fmt.Fprintf(w, "  mcp_approvals[%q] in this machine's .cartographer.yaml\n", p.Old)
	}
}

// applyKBRename moves the directory and rewrites the config entry, or neither.
// The directory goes first because it is the operation that can fail for
// reasons outside this process (permissions, a different filesystem); a failed
// config write is then undone by renaming it back, which is the only rollback
// this command claims.
func applyKBRename(p *renamePlan) error {
	if err := os.Rename(p.OldPath, p.NewPath); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			return fmt.Errorf("%s and %s are on different filesystems: move the KB yourself (a recursive copy would silently change the ownership and timestamps of a git repository), then rerun with the directory already in place", p.OldPath, p.NewPath)
		}
		return err
	}
	if p.EntryIndex < 0 {
		return nil
	}
	if err := rewriteKBEntry(p); err != nil {
		if back := os.Rename(p.NewPath, p.OldPath); back != nil {
			return fmt.Errorf("%w (and the directory could not be renamed back: %v — it is now at %s)", err, back, p.NewPath)
		}
		return err
	}
	return nil
}

// rewriteKBEntry edits the kbs[] entry in place through a yaml.Node document,
// so the operator's comments, key order and unmodelled fields survive: a
// rename must not silently reformat a hand-written server config.
func rewriteKBEntry(p *renamePlan) error {
	data, err := os.ReadFile(p.ConfigPath)
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse %s: %w", p.ConfigPath, err)
	}
	entry, err := kbEntryNode(&doc, p.EntryIndex)
	if err != nil {
		return fmt.Errorf("%s: %w", p.ConfigPath, err)
	}

	if path := mapValue(entry, "path"); path != nil {
		path.Value = p.NewPath
		path.Tag = "!!str"
	}
	if name := mapValue(entry, "name"); name != nil {
		name.Value = p.New
		name.Tag = "!!str"
	} else if mapValue(entry, "remote") != nil {
		// The name was derived from the remote, which the rename does not
		// touch: without an explicit name the KB would keep its old identity.
		entry.Content = append(entry.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "name"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p.New})
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return os.WriteFile(p.ConfigPath, out, 0o600)
}

// kbEntryNode returns the index-th element of the kbs sequence.
func kbEntryNode(doc *yaml.Node, index int) (*yaml.Node, error) {
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, errors.New("not a YAML document")
	}
	kbs := mapValue(doc.Content[0], "kbs")
	if kbs == nil || kbs.Kind != yaml.SequenceNode {
		return nil, errors.New("no kbs: sequence")
	}
	if index >= len(kbs.Content) {
		return nil, fmt.Errorf("kbs[%d] is out of range", index)
	}
	entry := kbs.Content[index]
	if entry.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("kbs[%d] is not a mapping", index)
	}
	return entry, nil
}

// mapValue returns the value node for key in a mapping node, or nil.
func mapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}
