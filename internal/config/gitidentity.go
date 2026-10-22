package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"gopkg.in/yaml.v3"
)

// IdentityDefault is the git.identities key for every KB without an entry of
// its own.
const IdentityDefault = "default"

// loadGitIdentities sets GitIdentity and GitIdentities from the git: block
// (D950). An invalid value is dropped with a warning, never a refusal to start:
// the server identity applies to that KB. A file that still has the retired
// git.author_name and git.author_email, and no git.identity, is read as if they
// were its git.identity, until the realignment has written the new key.
func (c *Config) loadGitIdentities(data []byte, g rawGit) {
	valid := func(key, v string) (string, bool) {
		if strings.TrimSpace(v) == "" {
			return "", true
		}
		name, email, problem := gitx.ParseIdent(v)
		if problem != "" {
			c.Warnings = append(c.Warnings, fmt.Sprintf("%s is ignored: %s (want Name <email>)", key, problem))
			return "", false
		}
		return name + " <" + email + ">", true
	}
	if v, ok := valid("git.identity", g.Identity); ok {
		c.GitIdentity = v
	}
	if c.GitIdentity == "" && strings.TrimSpace(g.Identity) == "" {
		if legacy := legacyGitIdentity(data); legacy != "" {
			if v, ok := valid("git.author_name/git.author_email", legacy); ok {
				c.GitIdentity = v
				c.Warnings = append(c.Warnings, "git.author_name and git.author_email are read as git.identity; the realignment writes the new key")
			}
		}
	}
	keys := make([]string, 0, len(g.Identities))
	for k := range g.Identities {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if v, ok := valid("git.identities."+k, g.Identities[k]); ok {
			if c.GitIdentities == nil {
				c.GitIdentities = map[string]string{}
			}
			// A present empty entry is kept: it says this KB has none.
			c.GitIdentities[k] = v
		}
	}
}

// IdentityFor is the git identity the server commits as in the KB named kb
// (D950): its git.identities entry, else the default entry, else git.identity.
// An entry that is present and empty means none: the server identity. ok is
// false when there is none to apply.
func (c *Config) IdentityFor(kb string) (name, email string, ok bool) {
	v, found := c.GitIdentities[kb]
	if !found {
		v, found = c.GitIdentities[IdentityDefault]
	}
	if !found {
		v = c.GitIdentity
	}
	if v == "" {
		return "", "", false
	}
	n, e, problem := gitx.ParseIdent(v)
	if problem != "" {
		return "", "", false
	}
	return n, e, true
}

// legacyGitIdentity is "Name <email>" built from the retired git.author_name
// and git.author_email of the server YAML, "" when either is missing.
func legacyGitIdentity(data []byte) string {
	var legacy struct {
		Git struct {
			AuthorName  string `yaml:"author_name"`
			AuthorEmail string `yaml:"author_email"`
		} `yaml:"git"`
	}
	if yaml.Unmarshal(data, &legacy) != nil {
		return ""
	}
	n, e := strings.TrimSpace(legacy.Git.AuthorName), strings.TrimSpace(legacy.Git.AuthorEmail)
	if n == "" || e == "" {
		return ""
	}
	return n + " <" + e + ">"
}

// MigrateGitIdentity writes git.identity into the server YAML at configPath
// from the retired git.author_name and git.author_email, when the file has the
// two and no git.identity, keeping the rest of the file (comments and key order)
// as it is. The realignment drops the retired keys right after, so the identity
// must be in place first. It reports whether it wrote the key.
func MigrateGitIdentity(configPath string) (bool, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false, err
	}
	ident := legacyGitIdentity(data)
	if ident == "" {
		return false, nil
	}
	if _, _, problem := gitx.ParseIdent(ident); problem != "" {
		return false, fmt.Errorf("git.author_name/git.author_email: %s", problem)
	}
	// A second document would be lost by the rewrite: leave such a file alone.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return false, nil
	}
	var second yaml.Node
	if err := dec.Decode(&second); !errors.Is(err, io.EOF) {
		return false, errors.New("the file holds more than one YAML document, which a rewrite would not keep")
	}
	var git *yaml.Node
	root := doc.Content[0]
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "git" && root.Content[i+1].Kind == yaml.MappingNode {
			git = root.Content[i+1]
		}
	}
	if git == nil {
		return false, nil
	}
	for i := 0; i+1 < len(git.Content); i += 2 {
		if git.Content[i].Value == "identity" {
			return false, nil
		}
	}
	name, email, _ := gitx.ParseIdent(ident)
	git.Content = append(git.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "identity"},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name + " <" + email + ">", Style: yaml.DoubleQuotedStyle})
	out, err := yaml.Marshal(&doc)
	if err != nil {
		return false, err
	}
	return true, writeFileAtomic(configPath, out)
}

// writeFileAtomic replaces the file at path (the file a symlink points to, when
// it is one) with data through a temporary file in the same directory, keeping
// its permission bits: a crash or a full disk leaves the old file, never a
// truncated one that would turn authentication off.
func writeFileAtomic(path string, data []byte) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
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
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
