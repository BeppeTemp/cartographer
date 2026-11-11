package mcpserver

import (
	"fmt"
	"slices"
	"strings"
)

// A preset is data of the server, not of a KB: a plugin pinned in the server
// config supplies a map contract and its templates, and preset_list hands them
// to a writer, who applies them with map_create and artifact_write under its
// own scopes. Nothing here reads or writes KB content.

// PresetCreate is the map_create arguments (minus name) a preset carries.
type PresetCreate struct {
	Title           string                         `json:"title" yaml:"title"`
	Kind            string                         `json:"kind" yaml:"kind"`
	ConceptTypes    []string                       `json:"concept_types,omitempty" yaml:"concept_types"`
	RequiredFields  map[string][]string            `json:"required_fields,omitempty" yaml:"required_fields"`
	FieldValues     map[string]map[string][]string `json:"field_values,omitempty" yaml:"field_values"`
	ForbiddenFields []string                       `json:"forbidden_fields,omitempty" yaml:"forbidden_fields"`
	Templates       []string                       `json:"templates,omitempty" yaml:"templates"`
	DefaultTemplate string                         `json:"default_template,omitempty" yaml:"default_template"`
}

// ValidateTemplate judges a template file as artifact_write does.
func ValidateTemplate(data []byte) error {
	_, _, _, err := validateTemplateArtifact(data)
	return err
}

// CheckPresetCreate judges a preset's contract as map_create would, and checks
// that every template it names is one of slugs (the preset's own templates).
func CheckPresetCreate(c PresetCreate, slugs []string) error {
	if strings.TrimSpace(c.Title) == "" || strings.ContainsAny(c.Title, "\n\r") {
		return fmt.Errorf("title must be one non-empty line")
	}
	if c.Kind != "map" && c.Kind != "journal" {
		return fmt.Errorf("kind must be \"map\" or \"journal\", got %q", c.Kind)
	}
	for _, t := range c.ConceptTypes {
		if strings.TrimSpace(t) == "" || strings.ContainsAny(t, ",[]\n\r") {
			return fmt.Errorf("concept type %q must be non-empty and contain no ',', '[' or ']'", t)
		}
	}
	for _, msg := range []string{validateContractParams(c.RequiredFields), validateFieldValueParams(c.FieldValues, c.ForbiddenFields), validateTemplateParams(c.Templates, c.DefaultTemplate)} {
		if msg != "" {
			return fmt.Errorf("%s", msg)
		}
	}
	for _, s := range c.Templates {
		if !slices.Contains(slugs, s) {
			return fmt.Errorf("templates names %q, which is not a template of this preset", s)
		}
	}
	return nil
}
