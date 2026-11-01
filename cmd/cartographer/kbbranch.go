package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/service"
	"gopkg.in/yaml.v3"
)

// checkBranchFlag validates --branch of `kb create` and `kb clone` (D1093)
// before anything is created: the branch name is checked like the `branch` of a
// settings.yaml, and a value that is only whitespace is a mistake, not "unset".
// It returns the trimmed branch ("" when the flag was not given) and 0, or the
// usage exit code (2).
func checkBranchFlag(fs *flag.FlagSet, value string) (string, int) {
	given := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "branch" {
			given = true
		}
	})
	if !given {
		return "", 0
	}
	branch := strings.TrimSpace(value)
	if branch == "" {
		fmt.Fprintln(os.Stderr, "Error: --branch is empty; give a branch name or omit the flag")
		return "", 2
	}
	if err := kb.ValidateBranchName(branch); err != nil {
		fmt.Fprintln(os.Stderr, "Error: --branch:", err)
		return "", 2
	}
	return branch, 0
}

// remoteTargetBranch is the branch a first push of a new KB to remote lands on:
// the remote's default branch when it already has refs (a forge's initial
// commit is adopted on it, D264), gitx.DefaultBranch for an empty one. An
// unreachable remote reads as empty: the push that follows reports the failure.
func remoteTargetBranch(remote string) string {
	refs, err := gitx.LsRemote(os.TempDir(), remote)
	if err != nil || !refs.HasRefs {
		return gitx.DefaultBranch
	}
	if b := canonicalOf(refs); b != "" {
		return b
	}
	return gitx.DefaultBranch
}

// canonicalOf is the default branch of a remote that has refs: its HEAD, else
// its only branch.
func canonicalOf(refs gitx.RemoteRefs) string {
	if refs.Default != "" {
		return refs.Default
	}
	if len(refs.Branches) == 1 {
		return refs.Branches[0]
	}
	if refs.HasBranch(gitx.DefaultBranch) {
		return gitx.DefaultBranch
	}
	return ""
}

// pushKBBranch creates branch from the current branch of the KB at path, pushes
// it with upstream tracking and leaves the clone on it (D1093): the second step
// of `kb create --branch`, after the default branch carrying the settings.yaml
// is on the remote. Nothing on the remote is deleted or rewritten.
func pushKBBranch(path, branch string) error {
	if err := gitx.CheckoutNewBranch(path, branch, "HEAD"); err != nil {
		return err
	}
	return gitx.PushSetUpstream(path, "origin", branch)
}

// serverConfigFile is the server YAML `kb clone --branch` edits: --config, or
// the standard path of the service.
func serverConfigFile(configFlag string) (string, error) {
	if configFlag != "" {
		return configFlag, nil
	}
	return service.ConfigPath()
}

// readServerConfigDoc parses the server file at path.
func readServerConfigDoc(path string) (*yaml.Node, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &doc, nil
}

// checkBranchEntryFree is the check `kb clone --branch` makes before it clones:
// the server file exists and has no kbs[] entry for this remote or this name,
// whose `branch` the operator would have to set by hand instead (D1093).
func checkBranchEntryFree(configPath, remote, name string) error {
	doc, err := readServerConfigDoc(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("--branch writes kbs[].branch in the server config, which does not exist at %s (use --config, or run `cartographer service install` first)", configPath)
		}
		return err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil
	}
	kbs := mapValue(doc.Content[0], "kbs")
	if kbs == nil || kbs.Kind != yaml.SequenceNode {
		return nil
	}
	for i, entry := range kbs.Content {
		r, n := "", ""
		if v := mapValue(entry, "remote"); v != nil {
			r = strings.TrimSpace(v.Value)
		}
		if v := mapValue(entry, "name"); v != nil {
			n = strings.TrimSpace(v.Value)
		}
		if n == "" && r != "" {
			n = remoteKBName(r)
		}
		if r == remote || n == name {
			return fmt.Errorf("%s already has an entry for this KB (kbs[%d]); set its `branch` there instead of cloning with --branch", configPath, i)
		}
	}
	return nil
}

// appendBranchEntry adds the kbs[] entry {remote, name (only when it differs
// from the one derived from the remote), branch} to the server file, in place
// through a yaml.Node document so the operator's comments, key order and
// unmodelled fields survive (as `kb rename` does).
func appendBranchEntry(configPath, remote, name, branch string) error {
	doc, err := readServerConfigDoc(configPath)
	if err != nil {
		return err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		doc.Kind = yaml.DocumentNode
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: not a YAML mapping", configPath)
	}
	str := func(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }
	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	add := func(key, val, schemaPath string) {
		entry.Content = append(entry.Content, str(key), str(val))
		config.Schema.CommentKey(entry, key, schemaPath)
	}
	add("remote", remote, "kbs[].remote")
	if name != remoteKBName(remote) {
		add("name", name, "kbs[].name")
	}
	add("branch", branch, "kbs[].branch")

	kbs := mapValue(root, "kbs")
	switch {
	case kbs == nil:
		root.Content = append(root.Content, str("kbs"), &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{entry}})
	case kbs.Kind == yaml.SequenceNode:
		kbs.Content = append(kbs.Content, entry)
	case kbs.Kind == yaml.ScalarNode && kbs.Tag == "!!null":
		*kbs = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{entry}}
	default:
		return fmt.Errorf("%s: kbs is not a list", configPath)
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(configPath); err == nil {
		mode = fi.Mode().Perm()
	}
	return writeFileAtomic(configPath, out, mode)
}

// writeFileAtomic replaces path with data through a temporary file in the same
// directory and a rename, so a crash never leaves the server file half written.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
