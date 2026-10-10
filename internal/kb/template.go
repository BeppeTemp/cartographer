package kb

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// Template metadata (D352): a template is a closed page schema. Its own
// frontmatter declares it with flat dotted keys under TemplateMetaPrefix (the
// frontmatter parser keeps a nested block opaque, and map contracts already use
// flat dotted keys); concept_new strips every one of them before rendering.
const (
	TemplateMetaPrefix = "x-template."

	TemplateMetaDescription     = "x-template.description"
	TemplateMetaRequiredFields  = "x-template.required_fields"
	TemplateMetaOptionalFields  = "x-template.optional_fields"
	TemplateMetaFieldValues     = "x-template.field_values."
	TemplateMetaOptionalSection = "x-template.optional_sections"
	TemplateMetaSectionAliases  = "x-template.section_aliases."
	TemplateMetaOpenSections    = "x-template.open_sections"

	// ShapeField is the page frontmatter field naming the template the page
	// follows. Not a tool parameter (the concept-write tools' own `template`
	// argument is: that name is reserved, D289).
	ShapeField = "shape"
)

// templateSlugPattern is the slug of a template file: lowercase, hyphenated,
// the same form as an artifact name (mcpserver.artifactSlugPattern, pinned equal
// by a test).
var templateSlugPattern = regexp.MustCompile(`^[a-z0-9]+(-{1,2}[a-z0-9]+)*$`)

// ValidTemplateSlug reports whether s can name a template: it is also the only
// form ever used to build a path under templates/, so a `shape` holding a "/"
// or ".." never reaches the filesystem.
func ValidTemplateSlug(s string) bool { return templateSlugPattern.MatchString(s) }

// TemplateInfo is one template as the checks see it.
type TemplateInfo struct {
	Slug        string
	Type        string
	Title       string
	Description string
	// Sections are the template's H2 headings in order; OptionalSections is
	// the subset a page may omit. Required = Sections minus OptionalSections.
	Sections         []string
	OptionalSections []string
	// Aliases maps a canonical H2 to the alternative names a page may use.
	Aliases        map[string][]string
	RequiredFields []string
	OptionalFields []string
	FieldValues    map[string][]string
	// OpenSections: a page may carry H2 headings the template does not name.
	OpenSections bool
}

// RequiredSections returns Sections without the optional ones, in order.
func (t TemplateInfo) RequiredSections() []string {
	opt := map[string]bool{}
	for _, s := range t.OptionalSections {
		opt[strings.ToLower(strings.TrimSpace(s))] = true
	}
	var out []string
	for _, s := range t.Sections {
		if !opt[strings.ToLower(strings.TrimSpace(s))] {
			out = append(out, s)
		}
	}
	return out
}

// TemplateCatalog is the KB's templates by slug.
type TemplateCatalog map[string]TemplateInfo

// Slugs returns the catalogue's slugs, sorted.
func (c TemplateCatalog) Slugs() []string {
	out := make([]string, 0, len(c))
	for s := range c {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// TemplateCatalog reads every templates/<slug>.md (regular files only,
// symlinks skipped) into a catalogue. An unreadable or frontmatter-less file is
// skipped: validation of a template is artifact_write's job, and a template
// that cannot be parsed simply does not bind. Templates stay outside the
// concept walk, search and the graph (D109).
func (kb *KB) TemplateCatalog() TemplateCatalog {
	dir := filepath.Join(kb.Root, "templates")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := TemplateCatalog{}
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		slug := strings.TrimSuffix(e.Name(), ".md")
		if !ValidTemplateSlug(slug) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if info, ok := ParseTemplate(slug, string(data)); ok {
			out[slug] = info
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ParseTemplate reads one template file. ok is false when it has no parseable
// frontmatter.
func ParseTemplate(slug, content string) (TemplateInfo, bool) {
	raw, body, has := okf.SplitFrontmatter(content)
	if !has {
		return TemplateInfo{}, false
	}
	fm, err := okf.ParseFrontmatter(raw)
	if err != nil {
		return TemplateInfo{}, false
	}
	info := TemplateInfo{Slug: slug, Type: strings.TrimSpace(fm.Type()), Sections: templateH2(body)}
	if strings.Contains(info.Type, "{{") {
		info.Type = ""
	}
	if v, ok := fm.Get("title"); ok {
		info.Title, _ = v.(string)
	}
	list := func(key string) []string {
		v, _ := fm.Get(key)
		l, _ := v.([]string)
		return l
	}
	if v, ok := fm.Get(TemplateMetaDescription); ok {
		info.Description, _ = v.(string)
	}
	info.RequiredFields = list(TemplateMetaRequiredFields)
	info.OptionalFields = list(TemplateMetaOptionalFields)
	info.OptionalSections = list(TemplateMetaOptionalSection)
	if v, ok := fm.Get(TemplateMetaOpenSections); ok {
		s, _ := v.(string)
		info.OpenSections = s == "true"
	}
	for _, key := range fm.Keys() {
		switch {
		case strings.HasPrefix(key, TemplateMetaFieldValues):
			if info.FieldValues == nil {
				info.FieldValues = map[string][]string{}
			}
			info.FieldValues[strings.TrimPrefix(key, TemplateMetaFieldValues)] = list(key)
		case strings.HasPrefix(key, TemplateMetaSectionAliases):
			if info.Aliases == nil {
				info.Aliases = map[string][]string{}
			}
			info.Aliases[strings.TrimPrefix(key, TemplateMetaSectionAliases)] = list(key)
		}
	}
	return info, true
}

// Resolve picks the template a page follows (D352): (a) its `shape` when it
// names an existing template; (b) else the map's default_template when the
// page's type equals that template's type; (c) else the type default
// templates/<lowercased type>.md; (d) else none. An unresolvable shape falls
// back to (b)-(c), so a typo does not hide a page's missing sections.
// contract may be nil.
func (c TemplateCatalog) Resolve(shape, pageType string, contract *MapContract) (TemplateInfo, bool) {
	if ValidTemplateSlug(shape) {
		if t, ok := c[shape]; ok {
			return t, true
		}
	}
	pageType = strings.TrimSpace(pageType)
	if contract != nil && contract.DefaultTemplate != "" {
		if t, ok := c[contract.DefaultTemplate]; ok && pageType != "" && strings.EqualFold(t.Type, pageType) {
			return t, true
		}
	}
	if slug := strings.ToLower(pageType); ValidTemplateSlug(slug) {
		if t, ok := c[slug]; ok {
			return t, true
		}
	}
	return TemplateInfo{}, false
}

// HasTemplateKeys reports whether the contract sets any of the template keys:
// the checks of D352 cost nothing, and read no template, for a map that sets
// none.
func (c MapContract) HasTemplateKeys() bool {
	return c.TemplateSections || len(c.Templates) > 0 || c.DefaultTemplate != "" || c.RequireTemplate
}

// StrictTemplates reports whether every page of the map must follow one of its
// templates (D352, D371): a map that declares templates or a default template
// is strict unless it says require_template: false; require_template: true
// makes any map strict.
func (c MapContract) StrictTemplates() bool {
	if c.RequireTemplateOff {
		return false
	}
	return c.RequireTemplate || len(c.Templates) > 0 || c.DefaultTemplate != ""
}
