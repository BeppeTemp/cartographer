package mcpserver

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// kbArtifact is one artifact a KB ships, as both artifact_list and the Atlas
// UI's artifact routes see it (D238). Files carry KB-root-relative paths and
// their content in memory.
type kbArtifact struct {
	Kind  string
	Name  string
	Files []provisioning.ArtifactFile
	// Manifest is the provisioning entry (hash, signature); nil for the
	// curated instructions.md and for templates, which sync does not ship as
	// files.
	Manifest *provisioning.Artifact
}

// artifactSource is what listKBArtifacts needs beside the KB itself.
type artifactSource struct {
	allowlist []provisioning.MCPAllowlistEntry
	signer    ed25519.PrivateKey
}

// kbArtifactCatalog is a KB's artifacts, sorted by kind then name, and why a
// skill was left out (D191): an excluded skill has no entry to attach its
// reason to. The MCP allowlist diagnostics are deliberately not collected:
// they name a descriptor's target, which artifact_read hides from the same
// reader when the descriptor is not allowed.
type kbArtifactCatalog struct {
	Artifacts []kbArtifact
	Issues    []string
}

// listKBArtifacts enumerates what a KB ships. It reuses
// provisioning.BuildManifest without the bundle, so the kind classification
// stays in one place (D71 WP1) and bundled skills, which belong to the binary,
// are not listed. signer, when set, is the KB's artifact signer: the UI shows
// whether an artifact reaches clients signed.
func listKBArtifacts(k *kb.KB, allowlist []provisioning.MCPAllowlistEntry, signer ed25519.PrivateKey) (kbArtifactCatalog, error) {
	if err := rejectArtifactTreeSymlinks(k.Root); err != nil {
		return kbArtifactCatalog{}, err
	}
	var issues []string
	note := func(msg string) {
		// The manifest names this KB by its internal key; the reader is
		// already looking at it.
		issues = append(issues, strings.TrimPrefix(msg, `KB "`+artifactManifestKBKey+`": `))
	}
	kbRoots := map[string]string{artifactManifestKBKey: k.Root}
	opts := provisioning.BuildOptions{
		MCPAllowlists:   map[string][]provisioning.MCPAllowlistEntry{artifactManifestKBKey: allowlist},
		SkillDiagnostic: note,
	}
	if signer != nil {
		opts.Signers = map[string]ed25519.PrivateKey{artifactManifestKBKey: signer}
	}
	m, err := provisioning.BuildManifest(nil, kbRoots, opts)
	if err != nil {
		return kbArtifactCatalog{}, err
	}

	var out []kbArtifact
	for i := range m.Artifacts {
		a := m.Artifacts[i]
		switch a.Kind {
		case "skill", "agent", "hook", "mcp":
			files, err := provisioning.ReadArtifactFiles(a, nil, kbRoots)
			if err != nil {
				continue // best-effort: skip an artifact that fails to read
			}
			prefix := artifactFilePrefix(a.Kind, a.Name)
			entry := kbArtifact{Kind: a.Kind, Name: a.Name, Manifest: &a}
			for _, f := range files {
				f.Path = prefix + f.Path
				entry.Files = append(entry.Files, f)
			}
			out = append(out, entry)
		case "instructions":
			// The manifest artifact holds GENERATED content (D56, see
			// generateKBInstructions) — not the raw curated file on
			// disk. List the raw instructions.md instead, consistent
			// with what artifact_read/artifact_write operate on.
			data, readErr := os.ReadFile(filepath.Join(k.Root, "instructions.md"))
			if readErr != nil {
				continue // no curated instructions.md: nothing to list
			}
			out = append(out, kbArtifact{
				Kind:  "instructions",
				Name:  "instructions",
				Files: []provisioning.ArtifactFile{{Path: "instructions.md", Content: data}},
			})
		}
	}
	templates, err := listTemplateSlugs(k)
	if err != nil {
		return kbArtifactCatalog{}, err
	}
	for _, slug := range templates {
		data, err := os.ReadFile(filepath.Join(k.Root, "templates", slug+".md"))
		if err != nil {
			return kbArtifactCatalog{}, err
		}
		out = append(out, kbArtifact{
			Kind: "template", Name: slug,
			Files: []provisioning.ArtifactFile{{Path: "templates/" + slug + ".md", Content: data}},
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return kbArtifactCatalog{Artifacts: out, Issues: issues}, nil
}

// description is the one-line summary a human sees beside an artifact: a
// skill's or an agent's frontmatter `description`, a template's `title`.
// Missing or unparsable frontmatter yields none, never an error.
func (a kbArtifact) description() string {
	var file, key string
	switch a.Kind {
	case "skill":
		file, key = artifactFilePrefix(a.Kind, a.Name)+"SKILL.md", "description"
	case "agent":
		file, key = "agents/"+a.Name+".md", "description"
	case "template":
		file, key = "templates/"+a.Name+".md", "title"
	default:
		return ""
	}
	for _, f := range a.Files {
		if f.Path != file {
			continue
		}
		raw, _, ok := okf.SplitFrontmatter(string(f.Content))
		if !ok {
			return ""
		}
		fm, err := okf.ParseFrontmatter(raw)
		if err != nil {
			return ""
		}
		if v, ok := fm.Get(key); ok {
			if s, ok := v.(string); ok {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}
