package kb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/repoindex"
)

// PathRegistryFile is the KB-root file in which a KB declares its path
// placeholder vocabulary (D263): every {{path:<key>}} and {{repo:<key>}} key
// its concepts and artifacts may cite, with a description and an optional
// home-anchored default. It is data consumed by tools — sync_pull serves it,
// lint checks citations against it — not prose, and it is never materialized
// on a client.
const PathRegistryFile = "paths.yaml"

// PathDecl is one declared placeholder key. Remote is only meaningful for a
// repo key and is stored normalized ("host/owner/name"). The JSON shape is the
// sync_pull wire format of `path_registry`; the client decodes it into
// provisioning.PathRegistry, which mirrors these tags.
type PathDecl struct {
	Description string `json:"description"`
	Default     string `json:"default,omitempty"`
	Remote      string `json:"remote,omitempty"`
}

// PathRegistry is the parsed content of paths.yaml: `paths:` declares
// {{path:<key>}} keys, `repos:` declares {{repo:<key>}} keys.
type PathRegistry struct {
	Paths map[string]PathDecl `json:"paths,omitempty"`
	Repos map[string]PathDecl `json:"repos,omitempty"`
}

// Declared reports whether kind ("path" or "repo") declares key.
func (r PathRegistry) Declared(kind, key string) (PathDecl, bool) {
	var d PathDecl
	var ok bool
	switch kind {
	case "path":
		d, ok = r.Paths[key]
	case "repo":
		d, ok = r.Repos[key]
	}
	return d, ok
}

// IDs returns every declared key as a sorted "kind:key" string, the form
// every placeholder list uses (okf.Placeholders).
func (r PathRegistry) IDs() []string {
	out := make([]string, 0, len(r.Paths)+len(r.Repos))
	for k := range r.Paths {
		out = append(out, "path:"+k)
	}
	for k := range r.Repos {
		out = append(out, "repo:"+k)
	}
	sort.Strings(out)
	return out
}

// IsEmpty reports whether the registry declares nothing.
func (r PathRegistry) IsEmpty() bool { return len(r.Paths) == 0 && len(r.Repos) == 0 }

// PathRegistryMalformed is one entry of paths.yaml that was left out of the
// parsed registry, and why. Entry is "paths.<key>", "repos.<key>", a
// top-level key, or "" for the file as a whole.
type PathRegistryMalformed struct {
	Entry  string
	Reason string
}

func (m PathRegistryMalformed) String() string {
	if m.Entry == "" {
		return PathRegistryFile + ": " + m.Reason
	}
	return PathRegistryFile + ": " + m.Entry + ": " + m.Reason
}

// pathRegistryKeyPattern is the artifact slug pattern (tools_artifact.go's
// artifactSlugPattern): a registry key is authored like an artifact name, and
// a key that could not be one is a typo waiting to be copied into concepts.
var pathRegistryKeyPattern = regexp.MustCompile(`^[a-z0-9]+(-{1,2}[a-z0-9]+)*$`)

// pathRegistryFields are the fields each section accepts; anything else is
// rejected rather than ignored, so a misspelt `defualt:` is not a silently
// missing default.
var pathRegistryFields = map[string]map[string]bool{
	"paths": {"description": true, "default": true},
	"repos": {"description": true, "default": true, "remote": true},
}

// ParsePathRegistry parses paths.yaml tolerantly: every well-formed entry is
// returned, every malformed one is left out and listed — the same stance as
// MapContract.Malformed, because a read path (sync_pull, lint) must not lose a
// whole vocabulary to one bad line. err is non-nil only when the file is not
// a YAML mapping at all; ValidatePathRegistry is the strict write-path form.
func ParsePathRegistry(data []byte) (PathRegistry, []PathRegistryMalformed, error) {
	reg := PathRegistry{}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return reg, nil, fmt.Errorf("%s: not valid YAML: %v", PathRegistryFile, err)
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		return reg, nil, nil // empty file: an empty vocabulary
	}
	doc := root.Content[0]
	if doc.Kind == yaml.ScalarNode && doc.Tag == "!!null" {
		return reg, nil, nil
	}
	if doc.Kind != yaml.MappingNode {
		return reg, nil, fmt.Errorf("%s: top level must be a mapping with paths: and repos:", PathRegistryFile)
	}
	var bad []PathRegistryMalformed
	seenTop := map[string]bool{}
	for i := 0; i+1 < len(doc.Content); i += 2 {
		k, v := doc.Content[i], doc.Content[i+1]
		section := k.Value
		fields, known := pathRegistryFields[section]
		if !known {
			bad = append(bad, PathRegistryMalformed{Entry: section, Reason: "unknown top-level field (expected paths or repos)"})
			continue
		}
		if seenTop[section] {
			bad = append(bad, PathRegistryMalformed{Entry: section, Reason: "declared twice"})
			continue
		}
		seenTop[section] = true
		if v.Kind == yaml.ScalarNode && v.Tag == "!!null" {
			continue
		}
		if v.Kind != yaml.MappingNode {
			bad = append(bad, PathRegistryMalformed{Entry: section, Reason: "must be a mapping of key -> {description, …}"})
			continue
		}
		out := map[string]PathDecl{}
		for j := 0; j+1 < len(v.Content); j += 2 {
			keyNode, entry := v.Content[j], v.Content[j+1]
			key := keyNode.Value
			name := section + "." + key
			if _, dup := out[key]; dup {
				delete(out, key)
				bad = append(bad, PathRegistryMalformed{Entry: name, Reason: "declared twice"})
				continue
			}
			decl, reason := parsePathDecl(section, key, entry, fields)
			if reason != "" {
				bad = append(bad, PathRegistryMalformed{Entry: name, Reason: reason})
				continue
			}
			out[key] = decl
		}
		if len(out) == 0 {
			continue
		}
		if section == "paths" {
			reg.Paths = out
		} else {
			reg.Repos = out
		}
	}
	return reg, bad, nil
}

// parsePathDecl validates one entry; a non-empty reason means it is malformed.
func parsePathDecl(section, key string, entry *yaml.Node, fields map[string]bool) (PathDecl, string) {
	if !pathRegistryKeyPattern.MatchString(key) {
		return PathDecl{}, "key must be a lowercase-hyphenated slug (e.g. claude-home)"
	}
	if entry.Kind != yaml.MappingNode {
		return PathDecl{}, "must be a mapping with at least description:"
	}
	var d PathDecl
	seen := map[string]bool{}
	for i := 0; i+1 < len(entry.Content); i += 2 {
		f, v := entry.Content[i].Value, entry.Content[i+1]
		if !fields[f] {
			return PathDecl{}, fmt.Sprintf("unknown field %q", f)
		}
		if seen[f] {
			return PathDecl{}, fmt.Sprintf("field %q declared twice", f)
		}
		seen[f] = true
		if v.Kind != yaml.ScalarNode || (v.Tag != "!!str" && v.Tag != "!!null") {
			return PathDecl{}, fmt.Sprintf("field %q must be a string", f)
		}
		val := strings.TrimSpace(v.Value)
		switch f {
		case "description":
			d.Description = val
		case "default":
			d.Default = val
		case "remote":
			d.Remote = val
		}
	}
	if d.Description == "" {
		return PathDecl{}, "description is required"
	}
	if seen["default"] {
		if reason := checkRegistryDefault(d.Default); reason != "" {
			return PathDecl{}, reason
		}
	}
	if seen["remote"] {
		raw := d.Remote
		// The canonical form the registry documents — host/owner/name — has
		// neither a scheme nor the scp-like colon NormalizeRemote needs.
		if !strings.Contains(raw, "://") && !strings.Contains(raw, ":") {
			raw = "https://" + raw
		}
		norm, err := repoindex.NormalizeRemote(raw)
		if err != nil || strings.Count(string(norm), "/") < 2 {
			return PathDecl{}, fmt.Sprintf("remote %q is not a git remote (expected host/owner/name or a clone URL)", d.Remote)
		}
		d.Remote = string(norm)
	}
	return d, ""
}

// checkRegistryDefault accepts only a home-anchored default: "~", "$HOME", or
// either followed by "/…" with no ".." segment. A registry is read on every
// client, so an absolute path would be the author's machine hardcoded into
// everybody's — the exact problem placeholders remove (D75) — and a relative
// one has no anchor at all. "~user/…" is someone else's home.
func checkRegistryDefault(p string) string {
	if p == "" {
		return "default must not be empty (omit it instead)"
	}
	rest, ok := "", false
	for _, anchor := range []string{"~", "$HOME"} {
		if p == anchor {
			rest, ok = "", true
			break
		}
		if strings.HasPrefix(p, anchor+"/") {
			rest, ok = p[len(anchor)+1:], true
			break
		}
	}
	if !ok {
		return fmt.Sprintf("default %q must start with ~/ or $HOME/ (a default is read on every machine, so it cannot name an absolute or relative path)", p)
	}
	for _, seg := range strings.Split(rest, "/") {
		if seg == ".." {
			return fmt.Sprintf("default %q must not contain a .. segment", p)
		}
	}
	return ""
}

// ValidatePathRegistry is the strict form artifact_write applies before a
// paths.yaml reaches disk: any malformed entry rejects the whole file, with
// every reason named. A file that can be written can also be read in full.
func ValidatePathRegistry(data []byte) error {
	_, bad, err := ParsePathRegistry(data)
	if err != nil {
		return err
	}
	if len(bad) == 0 {
		return nil
	}
	msgs := make([]string, len(bad))
	for i, m := range bad {
		msgs[i] = m.String()
	}
	return errors.New(strings.Join(msgs, "; "))
}

// PathRegistryState is what ReadPathRegistry found at the KB root.
type PathRegistryState struct {
	// Present is false when the KB has no paths.yaml: nothing is declared and
	// nothing is enforced (the lint is opt-in by presence).
	Present bool
	// Unparseable is set when the file exists but is not a YAML mapping; its
	// reason is Malformed[0]. The registry is then empty, and a reader must
	// not treat every key as undeclared because of one syntax error.
	Unparseable bool
	Registry    PathRegistry
	Malformed   []PathRegistryMalformed
}

// ReadPathRegistry reads the KB's paths.yaml tolerantly (see
// ParsePathRegistry). A symlinked paths.yaml is an error, as every KB-root
// artifact is (D148): what the KB declares must be what the KB contains.
func (kb *KB) ReadPathRegistry() (PathRegistryState, error) {
	path := filepath.Join(kb.Root, PathRegistryFile)
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return PathRegistryState{}, nil
	}
	if err != nil {
		return PathRegistryState{}, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return PathRegistryState{}, fmt.Errorf("%s: symlink not allowed", PathRegistryFile)
	}
	if !fi.Mode().IsRegular() {
		return PathRegistryState{}, fmt.Errorf("%s: not a regular file", PathRegistryFile)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return PathRegistryState{}, err
	}
	reg, bad, perr := ParsePathRegistry(data)
	if perr != nil {
		msg := strings.TrimPrefix(perr.Error(), PathRegistryFile+": ")
		return PathRegistryState{Present: true, Unparseable: true, Malformed: []PathRegistryMalformed{{Reason: msg}}}, nil
	}
	return PathRegistryState{Present: true, Registry: reg, Malformed: bad}, nil
}

// pathRegistryArtifactRoots are the KB-root artifacts whose text may cite a
// placeholder: the provisioning artifacts a client materializes. templates/
// is absent on purpose — a template rejects colon placeholders outright.
var pathRegistryArtifactRoots = []string{"skills", "agents", "hooks", "mcp", "instructions.md"}

// ArtifactPlaceholders returns the placeholders the KB's own provisioning
// artifacts cite, as sorted, unique "kind:key" strings: the half of "is this
// declared key used?" that concepts do not answer (D263). Symlinks are not
// followed, and an unreadable entry is skipped: a lint input, not a gate.
func (kb *KB) ArtifactPlaceholders() ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, rel := range pathRegistryArtifactRoots {
		root := filepath.Join(kb.Root, rel)
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			// A missing root, an unreadable directory, a symlink (never
			// followed: WalkDir reports it as a non-regular entry) or anything
			// else that is not a plain file is simply skipped.
			if err != nil || !d.Type().IsRegular() {
				return nil
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			for _, id := range okf.Placeholders(string(data)) {
				if !seen[id] {
					seen[id] = true
					out = append(out, id)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}
