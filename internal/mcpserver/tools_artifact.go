// tools_artifact.go implements the artifact_read/artifact_write/artifact_list/
// artifact_delete tools: direct MCP editing of KB-root artifacts. Provisioning
// artifacts (skills/, agents/, hooks/, mcp/, instructions.md) are scanned by
// internal/provisioning.BuildManifest; templates/ are KB-only artifacts (D109)
// deliberately excluded from that manifest.
//
// Only artifact_read/artifact_list are always registered (read-only, see
// RegisterKBTools); artifact_write/artifact_delete require the per-KB
// KBSpec.AllowArtifactWrite flag (default false, see kb.KB.AllowArtifactWrite).
package mcpserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/skill"
)

// artifactMaxFileSize caps the size of a single artifact file accepted by
// artifact_write (D71): these files are injected as instructions/config that
// client agents execute — a generous but bounded cap keeps a runaway write
// from bloating the KB repo.
const artifactMaxFileSize = 256 * 1024 // 256 KiB

// artifactMaxPathLen caps the length of the "path" argument, defensive
// against pathological inputs before any filesystem call is made.
const artifactMaxPathLen = 400

// artifactSlugPattern matches lowercase-hyphenated artifact names (D71: "slug
// minuscolo-trattinato"), including the "--" namespace separator used by
// KB skills (e.g. "kbinfra--query-rete", see internal/skill doc comment).
var artifactSlugPattern = regexp.MustCompile(`^[a-z0-9]+(-{1,2}[a-z0-9]+)*$`)

// artifactManifestKBKey is the placeholder KB name passed to
// provisioning.BuildManifest/ReadArtifactFiles from artifact_list, which
// operates on a single KB and does not need the real name (Source is not
// part of the tool's output).
const artifactManifestKBKey = "kb"

// artifactPathInfo is the result of classifying a whitelisted artifact path.
type artifactPathInfo struct {
	Kind string // "skill" | "agent" | "hook" | "mcp" | "instructions" | "template"
	Name string // artifact name (slug); "" for instructions
}

// classifyArtifactPath validates relPath against the D71 whitelist and
// extracts its (kind, name). This is the single path guard shared by all
// four artifact_* tools: skills/<slug>/**, agents/<slug>.md, hooks/**,
// mcp/<slug>.json, instructions.md, templates/<slug>.md. Rejects absolute
// paths, traversal ("../"), and anything not matching one of those shapes.
func classifyArtifactPath(relPath string) (artifactPathInfo, error) {
	if relPath == "" {
		return artifactPathInfo{}, fmt.Errorf("'path' is required")
	}
	if len(relPath) > artifactMaxPathLen {
		return artifactPathInfo{}, fmt.Errorf("path too long (max %d chars)", artifactMaxPathLen)
	}
	if filepath.IsAbs(relPath) {
		return artifactPathInfo{}, fmt.Errorf("path not allowed: absolute path %q", relPath)
	}

	clean := filepath.ToSlash(filepath.Clean(relPath))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return artifactPathInfo{}, fmt.Errorf("path not allowed: %q escapes the KB root", relPath)
	}

	if clean == "instructions.md" {
		return artifactPathInfo{Kind: "instructions"}, nil
	}

	segs := strings.Split(clean, "/")
	switch segs[0] {
	case "agents":
		if len(segs) == 2 && strings.HasSuffix(segs[1], ".md") {
			slug := strings.TrimSuffix(segs[1], ".md")
			if artifactSlugPattern.MatchString(slug) {
				return artifactPathInfo{Kind: "agent", Name: slug}, nil
			}
		}
	case "mcp":
		if len(segs) == 2 && strings.HasSuffix(segs[1], ".json") {
			slug := strings.TrimSuffix(segs[1], ".json")
			if artifactSlugPattern.MatchString(slug) {
				return artifactPathInfo{Kind: "mcp", Name: slug}, nil
			}
		}
	case "templates":
		if len(segs) == 2 && strings.HasSuffix(segs[1], ".md") {
			slug := strings.TrimSuffix(segs[1], ".md")
			if artifactSlugPattern.MatchString(slug) {
				return artifactPathInfo{Kind: "template", Name: slug}, nil
			}
		}
	case "skills":
		if len(segs) >= 3 && artifactSlugPattern.MatchString(segs[1]) {
			return artifactPathInfo{Kind: "skill", Name: segs[1]}, nil
		}
	case "hooks":
		if len(segs) >= 2 && segs[1] != "" {
			return artifactPathInfo{Kind: "hook", Name: segs[1]}, nil
		}
	}

	return artifactPathInfo{}, fmt.Errorf(
		"path %q is not a whitelisted artifact path (skills/<slug>/**, agents/<slug>.md, hooks/**, mcp/<slug>.json, instructions.md, templates/<slug>.md)",
		relPath)
}

// sha256Hex returns the hex-encoded sha256 of data — the if_match currency
// used uniformly by the four artifact_* tools (plain content hash, no path
// prefix, unlike provisioning's aggregate ContentHashDir/contentHashFile).
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func rejectArtifactSymlinks(root, relPath string) error {
	current := root
	for _, part := range strings.Split(filepath.ToSlash(relPath), "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path not allowed: symlink %q", relPath)
		}
	}
	return nil
}

func rejectArtifactTreeSymlinks(root string) error {
	for _, rel := range []string{"skills", "agents", "hooks", "mcp", "instructions.md", "templates"} {
		if err := rejectArtifactSymlinks(root, rel); err != nil {
			return err
		}
		path := filepath.Join(root, rel)
		if err := filepath.WalkDir(path, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("path not allowed: symlink %q", path)
			}
			return nil
		}); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// --- artifact_read ---

func toolArtifactRead(k *kb.KB, allowlist []provisioning.MCPAllowlistEntry) Tool {
	return Tool{
		Name:     "artifact_read",
		ReadOnly: true,
		Description: "Reads a KB-root artifact file (provisioning artifacts under skills/, agents/, hooks/, mcp/, " +
			"or instructions.md; KB-only templates/<slug>.md). Returns content and " +
			"sha256 — use the sha256 as if_match for a subsequent artifact_write/artifact_delete.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["path"],
			"properties": {
				"path": {
					"type": "string",
					"description": "Path relative to the KB root, e.g. skills/my-skill/SKILL.md"
				}
				,"encoding": {"type":"string","enum":["text","base64"],"description":"Optional requested response encoding; defaults to text, but non-UTF-8 content is always returned as base64"}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Path     string `json:"path"`
				Encoding string `json:"encoding"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			info, err := classifyArtifactPath(params.Path)
			if err != nil {
				return errorResult(err.Error()), nil
			}
			if params.Encoding != "" && params.Encoding != "text" && params.Encoding != "base64" {
				return errorResult("encoding must be text or base64"), nil
			}
			if err := rejectArtifactSymlinks(k.Root, params.Path); err != nil {
				return errorResult(err.Error()), nil
			}
			abs, err := k.ResolveRootPath(params.Path)
			if err != nil {
				return errorResult(err.Error()), nil
			}
			data, err := os.ReadFile(abs)
			if err != nil {
				if os.IsNotExist(err) {
					return errorResult(fmt.Sprintf("not_found: %s", params.Path)), nil
				}
				return errorResult(fmt.Sprintf("artifact_read %q: %v", params.Path, err)), nil
			}
			if info.Kind == "mcp" {
				spec, parseErr := provisioning.ParseMCPServerSpec(info.Name, data)
				if parseErr != nil || !provisioning.MCPAllowed(allowlist, info.Name, spec) {
					return errorResult(fmt.Sprintf("not_found: %s", params.Path)), nil
				}
			}

			encoding := "text"
			content := string(data)
			if params.Encoding == "base64" || !utf8.Valid(data) {
				encoding, content = "base64", base64.StdEncoding.EncodeToString(data)
			}
			result := map[string]interface{}{
				"path":     params.Path,
				"content":  content,
				"encoding": encoding,
				"sha256":   sha256Hex(data),
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- artifact_list ---

// artifactFileEntry/artifactEntry shape the artifact_list JSON output.
type artifactFileEntry struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable"`
}

type artifactEntry struct {
	Kind  string              `json:"kind"`
	Name  string              `json:"name"`
	Files []artifactFileEntry `json:"files"`
}

// templateListEntry is the metadata emitted by template_list. Bodies stay on
// the explicit artifact_read path.
type templateListEntry struct {
	Slug  string   `json:"slug"`
	Type  string   `json:"type"`
	Title string   `json:"title"`
	Vars  []string `json:"vars"`
}

// artifactFilePrefix maps a (kind, name) to the path prefix used to turn a
// provisioning.ArtifactFile's artifact-relative Path into a KB-root-relative
// path, mirroring the destinations in classifyArtifactPath.
func artifactFilePrefix(kind, name string) string {
	switch kind {
	case "skill":
		return "skills/" + name + "/"
	case "hook":
		return "hooks/" + name + "/"
	case "agent":
		return "agents/"
	case "mcp":
		return "mcp/"
	case "template":
		return "templates/"
	}
	return ""
}

func toolArtifactList(k *kb.KB, allowlist []provisioning.MCPAllowlistEntry) Tool {
	return Tool{
		Name:     "artifact_list",
		ReadOnly: true,
		Description: "Lists KB-root artifacts: provisioning skill/agent/hook/mcp/instructions artifacts and " +
			"KB-only templates. Each has files and sha256 values for artifact_write/artifact_delete.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			if err := rejectArtifactTreeSymlinks(k.Root); err != nil {
				return errorResult("artifact_list: " + err.Error()), nil
			}
			kbRoots := map[string]string{artifactManifestKBKey: k.Root}
			// Reuses provisioning.BuildManifest (no bundle) so the kind
			// classification stays in a single place (D71 WP1).
			m, err := provisioning.BuildManifest(nil, kbRoots, provisioning.BuildOptions{MCPAllowlists: map[string][]provisioning.MCPAllowlistEntry{artifactManifestKBKey: allowlist}})
			if err != nil {
				return errorResult("artifact_list: " + err.Error()), nil
			}

			var out []artifactEntry
			for _, a := range m.Artifacts {
				switch a.Kind {
				case "skill", "agent", "hook", "mcp":
					files, err := provisioning.ReadArtifactFiles(a, nil, kbRoots)
					if err != nil {
						continue // best-effort: skip an artifact that fails to read
					}
					prefix := artifactFilePrefix(a.Kind, a.Name)
					entry := artifactEntry{Kind: a.Kind, Name: a.Name}
					for _, f := range files {
						entry.Files = append(entry.Files, artifactFileEntry{
							Path:       prefix + f.Path,
							SHA256:     sha256Hex(f.Content),
							Executable: f.Executable,
						})
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
					out = append(out, artifactEntry{
						Kind:  "instructions",
						Name:  "instructions",
						Files: []artifactFileEntry{{Path: "instructions.md", SHA256: sha256Hex(data)}},
					})
				}
			}
			templates, err := listTemplateSlugs(k)
			if err != nil {
				return errorResult("artifact_list: " + err.Error()), nil
			}
			for _, slug := range templates {
				data, err := os.ReadFile(filepath.Join(k.Root, "templates", slug+".md"))
				if err != nil {
					return errorResult("artifact_list: " + err.Error()), nil
				}
				out = append(out, artifactEntry{
					Kind: "template", Name: slug,
					Files: []artifactFileEntry{{Path: "templates/" + slug + ".md", SHA256: sha256Hex(data)}},
				})
			}

			sort.Slice(out, func(i, j int) bool {
				if out[i].Kind != out[j].Kind {
					return out[i].Kind < out[j].Kind
				}
				return out[i].Name < out[j].Name
			})

			res, _ := json.MarshalIndent(out, "", "  ")
			return textResult(string(res)), nil
		},
	}
}

// listTemplateSlugs directly scans templates/, because template is deliberately
// absent from the provisioning manifest. It does not validate content: an
// out-of-band invalid template must remain readable and repairable through the
// artifact lifecycle, while concept_new always revalidates the selected file.
func listTemplateSlugs(k *kb.KB) ([]string, error) {
	if err := rejectArtifactSymlinks(k.Root, "templates"); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(k.Root, "templates"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		slug := strings.TrimSuffix(entry.Name(), ".md")
		if !artifactSlugPattern.MatchString(slug) {
			continue
		}
		relPath := "templates/" + entry.Name()
		if err := rejectArtifactSymlinks(k.Root, relPath); err != nil {
			return nil, err
		}
		out = append(out, slug)
	}
	sort.Strings(out)
	return out, nil
}

// scanTemplates validates and projects templates for the discovery tool. A
// malformed out-of-band file is reported here rather than silently advertised
// as renderable; artifact_list/artifact_read still let an agent repair it.
func scanTemplates(k *kb.KB) ([]templateListEntry, error) {
	slugs, err := listTemplateSlugs(k)
	if err != nil {
		return nil, err
	}
	out := make([]templateListEntry, 0, len(slugs))
	for _, slug := range slugs {
		data, err := os.ReadFile(filepath.Join(k.Root, "templates", slug+".md"))
		if err != nil {
			return nil, err
		}
		fm, _, vars, err := validateTemplateArtifact(data)
		if err != nil {
			return nil, fmt.Errorf("template %q: %w", slug, err)
		}
		title, _ := fm.Get("title")
		titleString, _ := title.(string)
		out = append(out, templateListEntry{Slug: slug, Type: fm.Type(), Title: titleString, Vars: vars})
	}
	return out, nil
}

func toolTemplateList(k *kb.KB) Tool {
	return Tool{
		Name: "template_list", ReadOnly: true,
		Description: "Lists KB-only concept templates with slug, literal type, title, and sorted variables. " +
			"Use artifact_read to retrieve a template body.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			templates, err := scanTemplates(k)
			if err != nil {
				return errorResult("template_list: " + err.Error()), nil
			}
			out, _ := json.MarshalIndent(templates, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- artifact_write ---

func toolArtifactWrite(k *kb.KB) Tool {
	return Tool{
		Name: "artifact_write",
		Description: "Creates or updates a KB-root artifact file (skills/<slug>/**, agents/<slug>.md, " +
			"hooks/**, mcp/<slug>.json, instructions.md, templates/<slug>.md). " +
			"On an existing file, if_match is required (sha256 of the current content, from " +
			"artifact_read/artifact_list) — fails with stale_write if it's missing or doesn't " +
			"match. On a new file, omit if_match — fails with already_exists if the file is " +
			"actually already there. Validates structured artifacts, including template frontmatter and variables, " +
			"before writing.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["path", "content"],
			"properties": {
				"path": {
					"type": "string",
					"description": "Path relative to the KB root, e.g. skills/my-skill/SKILL.md"
				},
				"content": {
					"type": "string",
					"description": "New file content, text by default or base64 when encoding is base64"
				},
				"encoding": {"type":"string","enum":["text","base64"],"description":"Content encoding; defaults to text"},
				"executable": {"type":"boolean","description":"Optional executable bit; omitted preserves an existing file mode and defaults false for a new file"},
				"if_match": {
					"type": "string",
					"description": "Expected sha256 of the current content — required when overwriting an existing file, omit when creating a new one"
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Path       string `json:"path"`
				Content    string `json:"content"`
				IfMatch    string `json:"if_match"`
				Encoding   string `json:"encoding"`
				Executable *bool  `json:"executable"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			info, err := classifyArtifactPath(params.Path)
			if err != nil {
				return errorResult(err.Error()), nil
			}

			if params.Encoding != "" && params.Encoding != "text" && params.Encoding != "base64" {
				return errorResult("encoding must be text or base64"), nil
			}
			data := []byte(params.Content)
			if params.Encoding == "base64" {
				var decodeErr error
				data, decodeErr = base64.StdEncoding.DecodeString(params.Content)
				if decodeErr != nil {
					return errorResult("encoding base64: invalid content"), nil
				}
			}
			if len(data) > artifactMaxFileSize {
				return errorResult(fmt.Sprintf(
					"artifact_write %q: decoded content is %d bytes, exceeds the %d bytes cap",
					params.Path, len(data), artifactMaxFileSize)), nil
			}

			abs, err := k.ResolveRootPath(params.Path)
			if err != nil {
				return errorResult(err.Error()), nil
			}
			if err := rejectArtifactSymlinks(k.Root, params.Path); err != nil {
				return errorResult(err.Error()), nil
			}

			existing, statErr := os.ReadFile(abs)
			fileExists := statErr == nil
			if statErr != nil && !os.IsNotExist(statErr) {
				return errorResult(fmt.Sprintf("artifact_write %q: %v", params.Path, statErr)), nil
			}

			switch {
			case fileExists && params.IfMatch == "":
				return errorResult(fmt.Sprintf(
					"already_exists: %s already exists (sha256 %s) — pass if_match to overwrite",
					params.Path, sha256Hex(existing))), nil
			case fileExists && params.IfMatch != sha256Hex(existing):
				return errorResult(fmt.Sprintf("stale_write: %s content-hash mismatch", params.Path)), nil
			case !fileExists && params.IfMatch != "":
				return errorResult(fmt.Sprintf("stale_write: %s not found", params.Path)), nil
			}
			mode := os.FileMode(0o644)
			if fileExists {
				stat, statErr := os.Stat(abs)
				if statErr != nil {
					return errorResult(fmt.Sprintf("artifact_write %q: stat: %v", params.Path, statErr)), nil
				}
				mode = stat.Mode()
			}

			if err := validateArtifactContent(info, params.Path, data); err != nil {
				return errorResult(fmt.Sprintf("artifact_write %q: %v", params.Path, err)), nil
			}
			if params.Executable != nil && *params.Executable && isStructuredArtifact(info, params.Path) {
				return errorResult("executable is not allowed for structured artifact files"), nil
			}

			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				return errorResult(fmt.Sprintf("artifact_write %q: mkdir: %v", params.Path, err)), nil
			}
			if err := os.WriteFile(abs, data, 0o644); err != nil {
				return errorResult(fmt.Sprintf("artifact_write %q: %v", params.Path, err)), nil
			}
			if params.Executable != nil {
				if *params.Executable {
					mode = 0o755
				} else {
					mode = 0o644
				}
			}
			if err := os.Chmod(abs, mode); err != nil {
				return errorResult(fmt.Sprintf("artifact_write %q: chmod: %v", params.Path, err)), nil
			}

			result := map[string]interface{}{
				"path":   params.Path,
				"sha256": sha256Hex(data),
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// validateArtifactContent runs the per-kind validation required before a
// write (D71 WP1). Only the files that actually carry structured content are
// validated; hooks/** and instructions.md get no validation beyond the
// generic size cap already applied by the caller.
func validateArtifactContent(info artifactPathInfo, relPath string, data []byte) error {
	if isStructuredArtifact(info, relPath) && !utf8.Valid(data) {
		return fmt.Errorf("content must be valid UTF-8")
	}
	switch info.Kind {
	case "skill":
		if filepath.Base(relPath) != "SKILL.md" {
			return nil // other files in the skill dir (scripts, resources, ...)
		}
		return validateSkillArtifact(info.Name, data)
	case "agent":
		return validateAgentArtifact(data)
	case "mcp":
		if _, err := provisioning.ParseMCPServerSpec(info.Name, data); err != nil {
			return err
		}
		return nil
	case "template":
		_, _, _, err := validateTemplateArtifact(data)
		return err
	default: // hook, instructions
		return nil
	}
}

func isStructuredArtifact(info artifactPathInfo, relPath string) bool {
	return (info.Kind == "skill" && filepath.Base(relPath) == "SKILL.md") || info.Kind == "agent" || info.Kind == "mcp" || info.Kind == "template" || info.Kind == "instructions" || (info.Kind == "hook" && filepath.Base(relPath) == "hook.json")
}

var templateVariablePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// validateTemplateText accepts only complete {{identifier}} placeholders. A
// colon form such as {{repo:key}} is rejected: it belongs to D75's client-side
// provisioning expansion, while these variables are rendered once server-side.
func validateTemplateText(text string) ([]string, error) {
	vars := map[string]bool{}
	for pos := 0; pos < len(text); {
		open := strings.Index(text[pos:], "{{")
		close := strings.Index(text[pos:], "}}")
		if open == -1 && close == -1 {
			break
		}
		if close >= 0 && (open == -1 || close < open) {
			return nil, fmt.Errorf("malformed template placeholder")
		}
		start := pos + open + 2
		endRel := strings.Index(text[start:], "}}")
		if endRel < 0 {
			return nil, fmt.Errorf("malformed template placeholder")
		}
		name := text[start : start+endRel]
		if !templateVariablePattern.MatchString(name) {
			return nil, fmt.Errorf("invalid template variable %q (use {{identifier}}; colon placeholders are not allowed)", name)
		}
		vars[name] = true
		pos = start + endRel + 2
	}
	out := make([]string, 0, len(vars))
	for name := range vars {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// validateTemplateArtifact parses a template and validates the deliberately
// small, logic-free grammar. It is also used by concept_new for templates that
// may have arrived out of band through Git.
func validateTemplateArtifact(data []byte) (*okf.Frontmatter, string, []string, error) {
	if !utf8.Valid(data) {
		return nil, "", nil, fmt.Errorf("content must be valid UTF-8")
	}
	fmRaw, body, hasFM := okf.SplitFrontmatter(string(data))
	if !hasFM {
		return nil, "", nil, fmt.Errorf("frontmatter is required")
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		return nil, "", nil, fmt.Errorf("parse frontmatter: %w", err)
	}
	if err := validateTemplateFrontmatterRaw(fmRaw); err != nil {
		return nil, "", nil, err
	}
	if strings.TrimSpace(fm.Type()) == "" {
		return nil, "", nil, fmt.Errorf("frontmatter 'type' must be a non-empty literal string")
	}
	vars := map[string]bool{}
	addVars := func(text string) error {
		found, err := validateTemplateText(text)
		if err != nil {
			return err
		}
		for _, name := range found {
			vars[name] = true
		}
		return nil
	}
	for _, key := range fm.Keys() {
		if err := addVars(key); err != nil {
			return nil, "", nil, fmt.Errorf("frontmatter key %q: %w", key, err)
		}
		if strings.Contains(key, "{{") || strings.Contains(key, "}}") {
			return nil, "", nil, fmt.Errorf("template variables are not allowed in frontmatter keys")
		}
		value, _ := fm.Get(key)
		switch v := value.(type) {
		case string:
			if err := addVars(v); err != nil {
				return nil, "", nil, fmt.Errorf("frontmatter %q: %w", key, err)
			}
		case []string:
			for _, item := range v {
				if err := addVars(item); err != nil {
					return nil, "", nil, fmt.Errorf("frontmatter %q: %w", key, err)
				}
			}
		}
	}
	if typeVars, err := validateTemplateText(fm.Type()); err != nil || len(typeVars) > 0 {
		return nil, "", nil, fmt.Errorf("frontmatter 'type' must be a non-empty literal string")
	}
	if err := addVars(body); err != nil {
		return nil, "", nil, fmt.Errorf("body: %w", err)
	}
	out := make([]string, 0, len(vars))
	for name := range vars {
		out = append(out, name)
	}
	sort.Strings(out)
	return fm, body, out, nil
}

// validateTemplateFrontmatterRaw closes the permissive parser's raw-line gap:
// a placeholder in a line it preserves as a comment/raw entry could otherwise
// evade variable accounting. Block-list items remain valid only immediately
// after a `key:` line, matching ParseFrontmatter's supported YAML subset.
func validateTemplateFrontmatterRaw(raw string) error {
	listOpen := false
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			if strings.Contains(line, "{{") || strings.Contains(line, "}}") {
				return fmt.Errorf("template variables are not allowed in frontmatter comments")
			}
			listOpen = false
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			if listOpen {
				continue
			}
			if strings.Contains(line, "{{") || strings.Contains(line, "}}") {
				return fmt.Errorf("template placeholder in unrecognized frontmatter line %q", trimmed)
			}
			continue
		}
		colon := strings.Index(line, ":")
		if colon < 0 {
			if strings.Contains(line, "{{") || strings.Contains(line, "}}") {
				return fmt.Errorf("template placeholder in unrecognized frontmatter line %q", trimmed)
			}
			listOpen = false
			continue
		}
		listOpen = strings.TrimSpace(line[colon+1:]) == ""
	}
	return nil
}

// validateSkillArtifact parses a candidate skills/<slug>/SKILL.md content and
// applies the same validation as the provisioning scan: the frontmatter name
// must equal the containing directory (slug) and skill.Validate must report
// no blocking issue (warnings, e.g. body too long, do not reject the write).
func validateSkillArtifact(slug string, data []byte) error {
	fmRaw, body, hasFM := okf.SplitFrontmatter(string(data))
	s := &skill.Skill{Body: body, DirPath: "skills/" + slug}
	if hasFM {
		fm, err := okf.ParseFrontmatter(fmRaw)
		if err != nil {
			return fmt.Errorf("parse frontmatter: %w", err)
		}
		if v, ok := fm.Get("name"); ok {
			if str, ok := v.(string); ok {
				s.Name = str
			}
		}
		if v, ok := fm.Get("description"); ok {
			if str, ok := v.(string); ok {
				s.Description = str
			}
		}
	}
	if s.Name != slug {
		return fmt.Errorf("SKILL.md frontmatter name %q must match the directory name %q", s.Name, slug)
	}
	for _, issue := range skill.Validate(s) {
		if !issue.Warning {
			return fmt.Errorf("%s", issue.Message)
		}
	}
	return nil
}

// validateAgentArtifact parses a candidate agents/<slug>.md content and
// requires a frontmatter with non-empty name and description (same
// requirements as the KB agent scan feeding provisioning/sync).
func validateAgentArtifact(data []byte) error {
	fmRaw, _, hasFM := okf.SplitFrontmatter(string(data))
	if !hasFM {
		return fmt.Errorf("frontmatter is required (name, description)")
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		return fmt.Errorf("parse frontmatter: %w", err)
	}
	name, _ := fm.Get("name")
	nameStr, _ := name.(string)
	if strings.TrimSpace(nameStr) == "" {
		return fmt.Errorf("frontmatter 'name' is required")
	}
	desc, _ := fm.Get("description")
	descStr, _ := desc.(string)
	if strings.TrimSpace(descStr) == "" {
		return fmt.Errorf("frontmatter 'description' is required")
	}
	return nil
}

// --- artifact_delete ---

func toolArtifactDelete(k *kb.KB) Tool {
	return Tool{
		Name: "artifact_delete",
		Description: "Deletes a KB-root artifact file (skills/<slug>/**, agents/<slug>.md, hooks/**, " +
			"mcp/<slug>.json, instructions.md, templates/<slug>.md), and its now-empty containing artifact " +
			"directory for skills/hooks. if_match (sha256 of the current content, from " +
			"artifact_read/artifact_list) is required.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["path", "if_match"],
			"properties": {
				"path": {
					"type": "string",
					"description": "Path relative to the KB root"
				},
				"if_match": {
					"type": "string",
					"description": "Expected sha256 of the current content"
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Path    string `json:"path"`
				IfMatch string `json:"if_match"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			info, err := classifyArtifactPath(params.Path)
			if err != nil {
				return errorResult(err.Error()), nil
			}
			if params.IfMatch == "" {
				return errorResult("'if_match' is required"), nil
			}
			if err := rejectArtifactSymlinks(k.Root, params.Path); err != nil {
				return errorResult(err.Error()), nil
			}

			abs, err := k.ResolveRootPath(params.Path)
			if err != nil {
				return errorResult(err.Error()), nil
			}

			existing, readErr := os.ReadFile(abs)
			if readErr != nil {
				if os.IsNotExist(readErr) {
					return errorResult(fmt.Sprintf("not_found: %s", params.Path)), nil
				}
				return errorResult(fmt.Sprintf("artifact_delete %q: %v", params.Path, readErr)), nil
			}
			if sha256Hex(existing) != params.IfMatch {
				return errorResult(fmt.Sprintf("stale_write: %s content-hash mismatch", params.Path)), nil
			}

			if err := os.Remove(abs); err != nil {
				return errorResult(fmt.Sprintf("artifact_delete %q: %v", params.Path, err)), nil
			}

			// Clean up the now-empty containing directory, bounded to the
			// artifact's own directory (skills/<slug>, hooks/<name>) — never
			// touches the shared skills/, hooks/, agents/, mcp/ parents.
			if boundary := artifactDirBoundary(k.Root, info); boundary != "" {
				removeEmptyDirsUpTo(filepath.Dir(abs), boundary)
			}

			result := map[string]interface{}{"path": params.Path, "status": "deleted"}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// artifactDirBoundary returns the artifact's own directory for kinds that
// group multiple files under one directory (skill, hook) — the upper bound
// removeEmptyDirsUpTo will clean up to (inclusive) if left empty by a delete.
// Single-file kinds (agent, mcp, instructions) return "": nothing to clean,
// their containing directory (agents/, mcp/, KB root) is shared.
func artifactDirBoundary(kbRoot string, info artifactPathInfo) string {
	switch info.Kind {
	case "skill":
		return filepath.Join(kbRoot, "skills", info.Name)
	case "hook":
		return filepath.Join(kbRoot, "hooks", info.Name)
	default:
		return ""
	}
}

// removeEmptyDirsUpTo removes leafDir and each empty parent up to and
// including boundary, stopping at the first non-empty directory. Best-effort:
// any error (permission, concurrent write) just stops the cleanup.
func removeEmptyDirsUpTo(leafDir, boundary string) {
	dir := leafDir
	for {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) > 0 {
			return
		}
		if err := os.Remove(dir); err != nil {
			return
		}
		if dir == boundary {
			return
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

// artifactNotifyWrap wraps a write/delete artifact Tool so that, after a
// successful call whose "path" argument falls under skills/, the server
// fires "notifications/skills/list_changed" — the same signal skill_install
// sends (D71 WP2), so a client whose skills catalog is stale (e.g. a skill
// self-edited via artifact_write) gets a chance to refresh it.
func artifactNotifyWrap(s *Server, t Tool) Tool {
	orig := t
	t.Handler = func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		res, err := orig.Handler(ctx, args)
		if err == nil && !res.IsError {
			var params struct {
				Path string `json:"path"`
			}
			if jsonErr := json.Unmarshal(args, &params); jsonErr == nil {
				clean := filepath.ToSlash(filepath.Clean(params.Path))
				if strings.HasPrefix(clean, "skills/") {
					_ = s.Notify("notifications/skills/list_changed", map[string]any{})
				}
			}
		}
		return res, err
	}
	return t
}
