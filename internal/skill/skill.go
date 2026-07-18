// Package skill implements loading and validation of SKILL.md files (agentskills.io format).
// Skills live under skills/ in the KB root, one per directory named <namespace>--<skill-name>.
package skill

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
}

// CatalogEntry is a compact representation for progressive disclosure (~100 tokens).
type CatalogEntry struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version,omitempty"`
	Path        string `json:"path"`
}

// Issue represents a validation issue for a skill.
type Issue struct {
	Path    string
	Message string
	Warning bool // true = warning, false = error
}

// LoadSkill reads and parses a SKILL.md file from the given directory path.
// dirPath is the full absolute path to the skill directory.
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

	if fmRaw != "" {
		fm, err := okf.ParseFrontmatter(fmRaw)
		if err == nil {
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
		// Store DirPath relative to kbRoot.
		rel, relErr := filepath.Rel(kbRoot, dirPath)
		if relErr == nil {
			s.DirPath = rel
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
			Body:    body,
			DirPath: dirPath,
		}
		if fmRaw != "" {
			fm, parseErr := okf.ParseFrontmatter(fmRaw)
			if parseErr == nil {
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

// Validate checks a skill for common issues:
// - name is required
// - description is required
// - body exceeds 500 lines is a warning
// Returns a list of issues (empty = valid).
func Validate(s *Skill) []Issue {
	var issues []Issue

	if strings.TrimSpace(s.Name) == "" {
		issues = append(issues, Issue{
			Path:    s.DirPath,
			Message: "name is required",
			Warning: false,
		})
	}

	if strings.TrimSpace(s.Description) == "" {
		issues = append(issues, Issue{
			Path:    s.DirPath,
			Message: "description is required",
			Warning: false,
		})
	}

	lineCount := len(strings.Split(s.Body, "\n"))
	if lineCount > 500 {
		issues = append(issues, Issue{
			Path:    s.DirPath,
			Message: fmt.Sprintf("body exceeds 500 lines (%d lines); keep under 5000 tokens", lineCount),
			Warning: true,
		})
	}

	return issues
}
