package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"gopkg.in/yaml.v3"
)

// IdentityDefault is the git.identities key for every KB without an entry of
// its own.
const IdentityDefault = "default"

// loadGitIdentities sets GitIdentities from the git: block (D950). An invalid
// value is dropped with a warning, never a refusal to start: the server identity
// applies to that KB. The retired git.identity, and the retired git.author_name
// and git.author_email, are read as git.identities.default until the
// realignment has written the new key (Retired.Moved); a file that sets both
// with different values is refused by Load before this runs.
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
	_, hasDefault := g.Identities[IdentityDefault]
	if v, ok := valid("git.identity", g.Identity); ok && v != "" && !hasDefault {
		c.setIdentity(IdentityDefault, v)
		c.Warnings = append(c.Warnings, "git.identity is read as git.identities.default; the realignment moves it")
	}
	if _, set := c.GitIdentities[IdentityDefault]; !set && !hasDefault && strings.TrimSpace(g.Identity) == "" {
		if legacy := legacyGitIdentity(data); legacy != "" {
			if v, ok := valid("git.author_name/git.author_email", legacy); ok {
				c.setIdentity(IdentityDefault, v)
				c.Warnings = append(c.Warnings, "git.author_name and git.author_email are read as git.identities.default; the realignment writes the new key")
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
			// A present empty entry is kept: it says this KB has none.
			c.setIdentity(k, v)
		}
	}
}

func (c *Config) setIdentity(kb, v string) {
	if c.GitIdentities == nil {
		c.GitIdentities = map[string]string{}
	}
	c.GitIdentities[kb] = v
}

// IdentityFor is the git identity the server commits as in the KB named kb
// (D950): its git.identities entry, else the default entry.
// An entry that is present and empty means none: the server identity. ok is
// false when there is none to apply.
func (c *Config) IdentityFor(kb string) (name, email string, ok bool) {
	v, found := c.GitIdentities[kb]
	if !found {
		v, found = c.GitIdentities[IdentityDefault]
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

// HasIdentityEntry reports whether git.identities says anything for the KB named
// kb, its own entry or the default one, an empty entry included ("none: the
// server identity", on purpose).
func (c *Config) HasIdentityEntry(kb string) bool {
	if _, found := c.GitIdentities[kb]; found {
		return true
	}
	_, found := c.GitIdentities[IdentityDefault]
	return found
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

// MigrateGitIdentity writes git.identities.default into the server YAML at
// configPath from the retired git.author_name and git.author_email, when the
// file has the two and neither git.identity nor that entry, keeping the rest of the file (comments and key order)
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
	return writeDefaultIdentity(configPath, ident, false)
}

// SetDefaultGitIdentity writes ident ("Name <email>") as git.identities.default
// into the server YAML at configPath when the file has neither that entry nor
// the retired git.identity, creating the git block if the file lacks one and
// keeping the rest of the file as it is (D1725, and D1974 for
// the personal install, where `connect` writes the user's own git identity). It
// reports whether it wrote the key.
func SetDefaultGitIdentity(configPath, ident string) (bool, error) {
	return writeDefaultIdentity(configPath, ident, true)
}

func writeDefaultIdentity(configPath, ident string, createGit bool) (bool, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return false, err
	}
	if _, _, problem := gitx.ParseIdent(ident); problem != "" {
		return false, fmt.Errorf("git identity: %s", problem)
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
		if root.Content[i].Value != "git" {
			continue
		}
		if v := root.Content[i+1]; v.Kind == yaml.MappingNode {
			git = v
		} else if createGit && v.Kind == yaml.ScalarNode && v.ShortTag() == "!!null" {
			// A bare `git:`: fill it, never add a second key of the same name.
			*v = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", HeadComment: v.HeadComment, FootComment: v.FootComment}
			git = v
		} else if createGit {
			return false, nil // not a mapping: leave the file to the operator
		}
	}
	if git == nil {
		if !createGit {
			return false, nil
		}
		git = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "git"}, git)
	}
	var ids *yaml.Node
	for i := 0; i+1 < len(git.Content); i += 2 {
		switch git.Content[i].Value {
		case "identity":
			return false, nil // Retired.Moved carries it
		case "identities":
			switch v := git.Content[i+1]; {
			case v.Kind == yaml.MappingNode:
				ids = v
			case v.Kind == yaml.ScalarNode && v.ShortTag() == "!!null":
				// A bare `identities:` is an empty mapping that was not written out:
				// fill it, never add a second key of the same name.
				*v = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", HeadComment: v.HeadComment, FootComment: v.FootComment}
				ids = v
			default:
				return false, nil // not a mapping: leave the file to the operator
			}
		}
	}
	str := func(v string, style yaml.Style) *yaml.Node {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Style: style}
	}
	if ids == nil {
		ids = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		git.Content = append(git.Content, str("identities", 0), ids)
	}
	for i := 0; i+1 < len(ids.Content); i += 2 {
		if ids.Content[i].Value == IdentityDefault {
			return false, nil
		}
	}
	name, email, _ := gitx.ParseIdent(ident)
	ids.Content = append(ids.Content, str(IdentityDefault, 0), str(name+" <"+email+">", yaml.DoubleQuotedStyle))
	return true, WriteYAML(configPath, &doc)
}
