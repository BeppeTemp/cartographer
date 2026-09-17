// Package skill implements loading and validation of SKILL.md files (agentskills.io format).
// Skills live under skills/ in the KB root, one directory per skill, named
// exactly as the skill's frontmatter `name` (D191).
package skill

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Skill represents a parsed SKILL.md file.
type Skill struct {
	Name        string // from frontmatter
	Description string // from frontmatter (keyword-rich, activation trigger)
	License     string // optional
	Version     string // semver, optional
	Body        string // markdown body (< 500 lines / < 5000 tokens guideline)
	DirPath     string // directory path relative to KB root (e.g. "skills/myns--myskill")
	ServiceRef  string // optional link to a Service concept

	// Frontmatter is the raw YAML block between the --- delimiters, and
	// FrontmatterErr is why the OKF reader could not parse it (empty when it
	// could). Both are carried so Validate can judge the frontmatter itself
	// rather than only its extracted fields: the OKF reader is lenient by
	// design and accepts blocks that no client can load, and the field values
	// it produces in that case look perfectly ordinary (D212).
	//
	// A caller that leaves them empty gets no frontmatter check, which is the
	// right default for a Skill assembled in memory rather than parsed.
	Frontmatter    string
	FrontmatterErr string
}

// CatalogEntry is a compact representation for progressive disclosure (~100 tokens).
type CatalogEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version,omitempty"`
	Path        string `json:"path"`
}

// Issue represents a validation issue for a skill. Rule names the check that
// failed, so a caller can attribute a refusal without parsing the message.
type Issue struct {
	Path    string
	Rule    string
	Message string
	Warning bool // true = warning, false = error
}

// skillNameRe is the intersection of what the supported clients accept:
// lowercase [a-z0-9] segments separated by single "-", no leading or trailing
// "-", and no "--" (OpenCode rejects it outright). Adopting the strictest
// client rule rather than the most permissive is the point of D191 — a skill we
// accept and a client silently ignores is a no-op in the agent's catalogue, and
// that failure is invisible from here.
var skillNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// maxSkillNameLen and maxSkillDescriptionLen are the client-side limits a skill
// must respect to be catalogued. The name limit is an error (the client refuses
// the skill); the description limit is a warning, because a long description
// degrades selection without breaking anything, and turning an existing KB's
// descriptions into hard failures would block syncs that work today.
const (
	maxSkillNameLen        = 64
	maxSkillDescriptionLen = 1024
)

// LoadSkill reads and parses a SKILL.md file from the given directory path.
// dirPath is the full absolute path to the skill directory.
// maxSkillBodyLines bounds a SKILL.md body: a skill is instructions an agent
// loads into context, so its length is a budget rather than a style choice.
const maxSkillBodyLines = 500

func LoadSkill(dirPath string) (*Skill, error) {
	skillPath := filepath.Join(dirPath, "SKILL.md")
	data, err := os.ReadFile(skillPath)
	if err != nil {
		return nil, fmt.Errorf("skill: read %s: %w", skillPath, err)
	}

	fmRaw, body, _ := okf.SplitFrontmatter(string(data))

	s := &Skill{
		Body:    body,
		DirPath: dirPath,
	}

	s.Frontmatter = fmRaw
	if fmRaw != "" {
		fm, err := okf.ParseFrontmatter(fmRaw)
		if err != nil {
			// Recorded, not discarded. Swallowing it produced a Skill with an
			// empty Name and Description and no error to the caller, so the
			// reason surfaced later as "name is required" — a true statement
			// about a file whose real problem was that nothing could read it
			// (D212).
			s.FrontmatterErr = err.Error()
		} else {
			s.Name = getString(fm, "name")
			s.Description = getString(fm, "description")
			s.License = getString(fm, "license")
			s.ServiceRef = getString(fm, "service_ref")

			// version: prefer metadata.version (nested stored flat), then version
			s.Version = getString(fm, "metadata.version")
			if s.Version == "" {
				s.Version = getString(fm, "version")
			}
		}
	}

	return s, nil
}

// getString retrieves a string value from frontmatter, returning "" if absent or not a string.
func getString(fm *okf.Frontmatter, key string) string {
	v, ok := fm.Get(key)
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// LoadAllSkills scans the skills/ directory under kbRoot and loads all SKILL.md files.
// Returns skills and any errors encountered (non-fatal per skill).
func LoadAllSkills(kbRoot string) ([]Skill, []error) {
	skillsDir := filepath.Join(kbRoot, "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return nil, []error{fmt.Errorf("skill: list %s: %w", skillsDir, err)}
	}

	var skills []Skill
	var errs []error

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirPath := filepath.Join(skillsDir, e.Name())
		s, err := LoadSkill(dirPath)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		// Store DirPath relative to kbRoot, always slash-separated: it is a KB
		// path that travels to clients and appears in findings, not a path on
		// this host, and LoadAllFromFS below spells it the same way.
		rel, relErr := filepath.Rel(kbRoot, dirPath)
		if relErr == nil {
			s.DirPath = filepath.ToSlash(rel)
		}
		skills = append(skills, *s)
	}

	return skills, errs
}

// LoadAllFromFS scans the root directory in fsys and loads all SKILL.md files.
// Each subdirectory of root that contains SKILL.md is treated as a skill.
// Returns skills and any errors encountered (non-fatal per skill).
func LoadAllFromFS(fsys fs.FS, root string) ([]Skill, []error) {
	entries, err := fs.ReadDir(fsys, root)
	if err != nil {
		return nil, []error{fmt.Errorf("skill: list %s: %w", root, err)}
	}

	var skills []Skill
	var errs []error

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dirPath := root + "/" + e.Name()
		skillPath := dirPath + "/SKILL.md"
		data, readErr := fs.ReadFile(fsys, skillPath)
		if readErr != nil {
			errs = append(errs, fmt.Errorf("skill: read %s: %w", skillPath, readErr))
			continue
		}

		fmRaw, body, _ := okf.SplitFrontmatter(string(data))
		s := &Skill{
			Body:        body,
			DirPath:     dirPath,
			Frontmatter: fmRaw,
		}
		if fmRaw != "" {
			fm, parseErr := okf.ParseFrontmatter(fmRaw)
			if parseErr != nil {
				s.FrontmatterErr = parseErr.Error()
			} else {
				s.Name = getString(fm, "name")
				s.Description = getString(fm, "description")
				s.License = getString(fm, "license")
				s.ServiceRef = getString(fm, "service_ref")
				s.Version = getString(fm, "metadata.version")
				if s.Version == "" {
					s.Version = getString(fm, "version")
				}
			}
		}
		skills = append(skills, *s)
	}

	return skills, errs
}

// Catalog returns compact catalog entries for progressive disclosure.
func Catalog(skills []Skill) []CatalogEntry {
	entries := make([]CatalogEntry, len(skills))
	for i, s := range skills {
		entries[i] = CatalogEntry{
			Name:        s.Name,
			Description: s.Description,
			Version:     s.Version,
			Path:        s.DirPath,
		}
	}
	return entries
}

// Validate is the single authority on whether a skill is well-formed (D191).
// Both channels that can introduce one call it: artifact_write over MCP, and
// BuildManifest for a skill that arrived through git. They used to disagree,
// and the git side was the weaker one — a malformed artifact it accepted broke
// the sync of the whole KB later, far from its cause.
//
// Errors (they break a channel or a client):
//   - the frontmatter is readable at all, and is valid YAML by the strict
//     rules a client applies (see strictFrontmatterYAML)
//   - name required, matching skillNameRe, at most maxSkillNameLen
//   - frontmatter name equal to the directory basename
//   - description required
//
// Warnings (they degrade quality, they break nothing):
//   - body over maxSkillBodyLines
//   - description over maxSkillDescriptionLen
func Validate(s *Skill) []Issue {
	var issues []Issue

	// First, because it explains the rest: a frontmatter nothing can read
	// produces an empty name and description, and reporting those instead is a
	// true answer to the wrong question.
	switch {
	case s.FrontmatterErr != "":
		issues = append(issues, Issue{
			Path: s.DirPath, Rule: "frontmatter_unparseable",
			Message: fmt.Sprintf("the frontmatter could not be read: %s", s.FrontmatterErr),
		})
	case s.Frontmatter != "":
		if err := strictFrontmatterYAML(s.Frontmatter); err != nil {
			issues = append(issues, Issue{
				Path: s.DirPath, Rule: "frontmatter_invalid_yaml",
				Message: fmt.Sprintf("the frontmatter is not valid YAML, so a client will skip this "+
					"skill without reporting anything: %s (line numbers are relative to the "+
					"frontmatter block). The usual cause is an unquoted value containing \": \" — "+
					"quote the value, or replace the colon with an em dash", err),
			})
		}
	}

	name := strings.TrimSpace(s.Name)
	switch {
	case name == "":
		issues = append(issues, Issue{
			Path: s.DirPath, Rule: "name_required",
			Message: "name is required",
		})
	default:
		if len(name) > maxSkillNameLen {
			issues = append(issues, Issue{
				Path: s.DirPath, Rule: "name_too_long",
				Message: fmt.Sprintf("name is %d characters, over the %d-character limit the clients enforce", len(name), maxSkillNameLen),
			})
		}
		if !skillNameRe.MatchString(name) {
			issues = append(issues, Issue{
				Path: s.DirPath, Rule: "name_invalid",
				Message: fmt.Sprintf("name %q must be lowercase alphanumeric segments joined by single hyphens (no uppercase, underscore, leading/trailing hyphen or \"--\")", name),
			})
		}
		// The manifest registers the artifact under the frontmatter name but
		// hashes and reads the directory: when they disagree, ReadArtifactFiles
		// looks for a path that does not exist and can take sync_pull down for
		// the entire KB.
		if base := filepath.Base(s.DirPath); base != "." && base != "/" && base != name {
			issues = append(issues, Issue{
				Path: s.DirPath, Rule: "name_directory_mismatch",
				Message: fmt.Sprintf("frontmatter name %q must match the directory name %q", name, base),
			})
		}
	}

	description := strings.TrimSpace(s.Description)
	if description == "" {
		issues = append(issues, Issue{
			Path: s.DirPath, Rule: "description_required",
			Message: "description is required",
		})
	} else if len(description) > maxSkillDescriptionLen {
		issues = append(issues, Issue{
			Path: s.DirPath, Rule: "description_too_long",
			Message: fmt.Sprintf("description is %d characters, over the %d-character guideline", len(description), maxSkillDescriptionLen),
			Warning: true,
		})
	}

	lineCount := len(strings.Split(s.Body, "\n"))
	if lineCount > maxSkillBodyLines {
		issues = append(issues, Issue{
			Path: s.DirPath, Rule: "body_too_long",
			// Lines only: the check counts lines, and a token count depends on
			// the tokenizer, so reporting the overage in one unit and the budget
			// in another was not actionable (D161).
			Message: fmt.Sprintf("body exceeds the %d-line limit (%d lines)", maxSkillBodyLines, lineCount),
			Warning: true,
		})
	}

	return issues
}

// strictFrontmatterYAML reports why a spec-compliant YAML parser would reject
// the frontmatter block, or nil if it would accept it.
//
// This is not a second opinion for its own sake. internal/okf is a lenient,
// stdlib-only reader (D8) and it accepts blocks that are not YAML — most
// commonly an unquoted plain scalar containing ": ", which it splits on the
// first colon and reads as the author intended. Every agent client parses the
// same file with gopkg.in/yaml.v3 or an equivalent, gets a syntax error, and
// drops the skill from its catalogue with no message anywhere. So the lenient
// reader is the *more* useful one for extracting fields, and the strict one is
// the only one that answers the question that matters: will a client load this
// (D212).
//
// The target is a mapping: frontmatter that parses as a list or a scalar has no
// fields to read and is rejected here rather than further down.
func strictFrontmatterYAML(raw string) error {
	var doc map[string]any
	return yaml.Unmarshal([]byte(raw), &doc)
}

// FirstError returns the first error-severity issue in issues, or nil when they
// are all warnings — the shape both channels need to decide whether to refuse.
func FirstError(issues []Issue) *Issue {
	for i := range issues {
		if !issues[i].Warning {
			return &issues[i]
		}
	}
	return nil
}
