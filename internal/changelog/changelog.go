// Package changelog groups the newest section of a release-please changelog by
// area, and checks the PR body lines that section is built from (D865).
//
// release-please groups by commit type only: one long list of Features and one
// of Bug Fixes. Regroup rewrites the newest version section as one heading per
// area, from the scope of each entry; WrappedLines is the CI check on the body
// lines that become entries.
package changelog

import (
	"regexp"
	"slices"
	"strings"
)

// An area is one heading of the regrouped changelog. Scopes[0] is the canonical
// scope a PR title uses; the rest are synonyms already on main.
type area struct {
	Name    string
	Scopes  []string
	Dropped bool // development entries are not changes a user installs
}

// areas is the output order. General (unscoped entries and unknown scopes) is
// last and has no scopes of its own.
var areas = []area{
	{Name: "Atlas", Scopes: []string{"atlas", "web", "webui", "ui"}},
	{Name: "MCP tools", Scopes: []string{"mcp", "mcpserver", "search", "kb_status"}},
	{Name: "Lint & repair", Scopes: []string{"lint", "repair", "doctor"}},
	{Name: "Client & CLI", Scopes: []string{"client", "cli", "sync", "provisioning", "skills", "skill", "hooks", "hook", "configurator", "tui"}},
	{Name: "Server & KB", Scopes: []string{"kb", "server", "serve", "service", "config", "gitx", "git", "import", "auth"}},
	{Name: "Development", Scopes: []string{"dev", "demo", "deploy", "ci"}, Dropped: true},
	{Name: "General"},
}

// titleOnlyScopes are valid in a PR title but are not an area: docs, deps and
// the release PR's own chore(main) never reach the changelog.
var titleOnlyScopes = []string{"docs", "deps", "main"}

// CanonicalScopes is the list a PR title may use: the first scope of each area,
// every development scope, and the title-only ones. The pr-title job in
// .github/workflows/pr-title.yml carries the same list (a test compares them).
func CanonicalScopes() []string {
	var out []string
	for _, a := range areas {
		switch {
		case a.Dropped:
			out = append(out, a.Scopes...)
		case len(a.Scopes) > 0:
			out = append(out, a.Scopes[0])
		}
	}
	return append(out, titleOnlyScopes...)
}

var scopeArea = func() map[string]int {
	m := map[string]int{}
	for i, a := range areas {
		for _, s := range a.Scopes {
			m[s] = i
		}
	}
	return m
}()

var (
	versionHeading = regexp.MustCompile(`^## (\[|v?\d+\.\d+\.\d+)`)
	scopedEntry    = regexp.MustCompile(`^\* \*\*([^*:\s]+):\*\* `)
)

const (
	featuresHeading = "### Features"
	fixesHeading    = "### Bug Fixes"
)

// Regroup rewrites the first version section of a release-please changelog (or
// of a release PR body) so its Features and Bug Fixes are grouped by area.
//
// Anything it does not regroup is copied byte for byte: the text outside the
// section, the Migration note, the breaking changes, every section other than
// Features and Bug Fixes. When in doubt (no version section, no Features or Bug
// Fixes, an entry list it cannot parse) it returns text unchanged.
//
// It MUST be idempotent: the release PR job runs it after every push to main,
// and a PR body or CHANGELOG.md it already grouped goes through it again. A
// section that already has an area heading is returned as it is.
func Regroup(text string) string {
	lines := strings.Split(text, "\n")
	start, end := versionSection(lines)
	if start < 0 {
		return text
	}
	section, ok := regroupSection(lines[start:end])
	if !ok {
		return text
	}
	return strings.Join(slices.Concat(lines[:start], section, lines[end:]), "\n")
}

// versionSection returns the line range of the first version section: from its
// `## [x.y.z]` heading to the next `## ` heading or to the `---` that opens the
// release PR footer, whichever comes first. start is -1 when there is none.
func versionSection(lines []string) (start, end int) {
	start = -1
	for i, l := range lines {
		if versionHeading.MatchString(l) {
			start = i
			break
		}
	}
	if start < 0 {
		return -1, -1
	}
	end = len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") || strings.TrimRight(lines[i], "\r") == "---" {
			end = i
			break
		}
	}
	return start, end
}

func isBlank(l string) bool { return strings.TrimSpace(l) == "" }

// regroupSection regroups one version section. ok is false when the section
// must be left alone.
func regroupSection(sec []string) (out []string, ok bool) {
	// The blank lines that end the section are kept as they are.
	trailing := 0
	for trailing < len(sec) && isBlank(sec[len(sec)-1-trailing]) {
		trailing++
	}
	body := sec[:len(sec)-trailing]

	// head is the version heading and what precedes the first `### `; each block
	// runs from a `### ` heading to the next one.
	var head []string
	var blocks [][]string
	for _, l := range body {
		switch {
		case strings.HasPrefix(l, "### "):
			blocks = append(blocks, []string{l})
		case len(blocks) == 0:
			head = append(head, l)
		default:
			blocks[len(blocks)-1] = append(blocks[len(blocks)-1], l)
		}
	}

	first := -1
	for i, b := range blocks {
		h := strings.TrimRight(b[0], "\r")
		if isAreaHeading(h) {
			return nil, false // already grouped
		}
		if (h == featuresHeading || h == fixesHeading) && first < 0 {
			first = i
		}
	}
	if first < 0 {
		return nil, false
	}

	features := make([][]entry, len(areas))
	fixes := make([][]entry, len(areas))
	for _, b := range blocks {
		var into [][]entry
		switch strings.TrimRight(b[0], "\r") {
		case featuresHeading:
			into = features
		case fixesHeading:
			into = fixes
		default:
			continue
		}
		entries, ok := parseEntries(b[1:])
		if !ok {
			return nil, false
		}
		for _, e := range entries {
			i, line := e.place()
			if areas[i].Dropped {
				continue
			}
			into[i] = append(into[i], line)
		}
	}

	out = append(out, head...)
	for i, b := range blocks {
		h := strings.TrimRight(b[0], "\r")
		switch {
		case i == first:
			out = append(out, renderAreas(features, fixes)...)
		case h == featuresHeading || h == fixesHeading:
			// folded into the area blocks above
		default:
			out = append(out, b...)
		}
	}
	for len(out) > 0 && isBlank(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	return append(out, sec[len(sec)-trailing:]...), true
}

func isAreaHeading(h string) bool {
	for _, a := range areas {
		if !a.Dropped && h == "### "+a.Name {
			return true
		}
	}
	return false
}

// An entry is a `* ` line and the lines that continue it directly below.
type entry []string

// parseEntries reads the lines under a Features or Bug Fixes heading. ok is
// false when there is something that is not an entry, which the regrouper would
// lose.
func parseEntries(lines []string) (entries []entry, ok bool) {
	var cur entry
	prevBlank := true
	for _, l := range lines {
		if isBlank(l) {
			prevBlank = true
			continue
		}
		switch {
		case strings.HasPrefix(l, "* "):
			if cur != nil {
				entries = append(entries, cur)
			}
			cur = entry{l}
		case cur != nil && !prevBlank:
			cur = append(cur, l)
		default:
			return nil, false
		}
		prevBlank = false
	}
	if cur != nil {
		entries = append(entries, cur)
	}
	return entries, true
}

// place returns the area of an entry and the entry as that area shows it: the
// `**scope:** ` prefix dropped, since the heading says it, except in General.
func (e entry) place() (int, entry) {
	general := len(areas) - 1
	m := scopedEntry.FindStringSubmatch(e[0])
	if m == nil {
		return general, e
	}
	i, known := scopeArea[strings.ToLower(m[1])]
	if !known {
		return general, e
	}
	out := append(entry{"* " + e[0][len(m[0]):]}, e[1:]...)
	return i, out
}

// renderAreas writes one block per non-empty area, with the two blank lines
// before a `###` heading that release-please writes.
func renderAreas(features, fixes [][]entry) []string {
	var out []string
	for i, a := range areas {
		if a.Dropped || len(features[i])+len(fixes[i]) == 0 {
			continue
		}
		out = append(out, "### "+a.Name, "")
		for _, g := range []struct {
			title   string
			entries []entry
		}{{"#### Features", features[i]}, {"#### Bug Fixes", fixes[i]}} {
			if len(g.entries) == 0 {
				continue
			}
			out = append(out, g.title, "")
			for _, e := range g.entries {
				out = append(out, e...)
			}
			out = append(out, "")
		}
		out = append(out, "")
	}
	return out
}
