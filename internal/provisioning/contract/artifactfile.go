package contract

import (
	"fmt"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/skill"
	"gopkg.in/yaml.v3"
)

// The two validators below are the single authority (D191) for a candidate
// skill or agent file: the MCP write channel (internal/mcpserver) and the
// plugin reader (internal/plugin) both call them, so neither can drift from the
// other.

// ValidateSkillFile parses a candidate skills/<slug>/SKILL.md content and
// applies the same validation as the provisioning scan: the frontmatter name
// must equal the containing directory (slug) and skill.Validate must report
// no blocking issue (warnings, e.g. body too long, do not reject the write).
func ValidateSkillFile(slug string, data []byte) error {
	fmRaw, body, hasFM := okf.SplitFrontmatter(string(data))
	s := &skill.Skill{Body: body, DirPath: "skills/" + slug}
	if hasFM {
		// The raw block travels with the Skill: skill.Validate re-reads it with
		// a strict parser, because the OKF reader accepts frontmatter that no
		// client can load and its extracted fields look ordinary (D212).
		s.Frontmatter = fmRaw
		fm, err := okf.ParseFrontmatter(fmRaw)
		if err != nil {
			s.FrontmatterErr = err.Error()
		} else {
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
	}
	// skill.Validate is the single authority (D191): the name rules, the
	// name/directory equality and the description requirement all live there,
	// so the MCP write channel, the git scan and the plugin reader cannot drift
	// apart. slug is the directory the caller read the file from; the caller
	// has already checked its path shape.
	if err := skill.FirstError(skill.Validate(s)); err != nil {
		return fmt.Errorf("%s", err.Message)
	}
	if hasFM {
		if fm, err := okf.ParseFrontmatter(fmRaw); err == nil {
			return ValidateFrontmatterRequires("skill", slug, fm)
		}
	}
	return nil
}

// ValidateAgentFile parses a candidate agents/<slug>.md content and
// requires a frontmatter with non-empty name and description (same
// requirements as the KB agent scan feeding provisioning/sync) and a name equal
// to the file name (D283): Claude registers the agent under the frontmatter
// name, the other clients under the file name, so a mismatch would give one
// agent two names.
func ValidateAgentFile(slug string, data []byte) error {
	fmRaw, _, hasFM := okf.SplitFrontmatter(string(data))
	if !hasFM {
		return fmt.Errorf("frontmatter is required (name, description)")
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		return fmt.Errorf("parse frontmatter: %w", err)
	}
	// The OKF reader is lenient (D8): it reads an unquoted value containing ": "
	// that gopkg.in/yaml.v3 rejects, and the approval gate parses with yaml.v3
	// and holds what it cannot parse, so an agent that passed only the first
	// would be held at every sync with no reason given (D212 for agents).
	if err := ValidateFrontmatterYAML(data); err != nil {
		return fmt.Errorf("the frontmatter is not valid YAML, so sync holds the agent for approval "+
			"and a client may skip it: %v (line numbers are relative to the frontmatter block). "+
			"The usual cause is an unquoted value containing \": \" — quote the value", err)
	}
	name, _ := fm.Get("name")
	nameStr, _ := name.(string)
	if strings.TrimSpace(nameStr) == "" {
		return fmt.Errorf("frontmatter 'name' is required")
	}
	if strings.TrimSpace(nameStr) != slug {
		return fmt.Errorf("frontmatter 'name' (%q) must equal the file name %q", strings.TrimSpace(nameStr), slug)
	}
	desc, _ := fm.Get("description")
	descStr, _ := desc.(string)
	if strings.TrimSpace(descStr) == "" {
		return fmt.Errorf("frontmatter 'description' is required")
	}
	// A `providers:` block is copied verbatim into native agent files (D291):
	// a typo in a client name or a reserved key would otherwise drop the
	// author's restriction without a word, so it is rejected here.
	if err := ValidateAgentProviders(fm); err != nil {
		return err
	}
	if err := ValidateAgentClients(fm); err != nil {
		return err
	}
	return ValidateFrontmatterRequires("agent", slug, fm)
}

// FrontmatterYAML cuts the frontmatter block out of a skill's or agent's file
// content and parses it with gopkg.in/yaml.v3, the parser the approval gate and
// the clients use. It returns a nil node and a nil error when the content has no
// frontmatter, and an error for a block that opens and never closes or whose
// YAML does not parse. It is the one extraction both ValidateFrontmatterYAML and
// the approval gate (provisioning.FrontmatterDeclaresCommands) use, so the write
// check and the gate cannot disagree about what parses.
func FrontmatterYAML(content []byte) (*yaml.Node, error) {
	text := strings.TrimPrefix(string(content), "\ufeff")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	// A delimiter starts at column 0: an indented "---" is text inside a block
	// scalar, and taking it for the close would hide the keys after it.
	if start >= len(lines) || strings.TrimRight(lines[start], " \t") != "---" {
		return nil, nil // no frontmatter
	}
	end := -1
	for i := start + 1; i < len(lines); i++ {
		if t := strings.TrimRight(lines[i], " \t"); t == "---" || t == "..." {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, fmt.Errorf("the frontmatter block is never closed")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(lines[start+1:end], "\n")), &doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// ValidateFrontmatterYAML reports why the frontmatter of content is not valid
// YAML for gopkg.in/yaml.v3, or nil when there is none or it parses.
func ValidateFrontmatterYAML(content []byte) error {
	_, err := FrontmatterYAML(content)
	return err
}
