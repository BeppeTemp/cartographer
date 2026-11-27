package provisioning

import (
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// InstructionsSection is one KB's region of the instructions block.
type InstructionsSection struct{ Name, Text string }

// InstructionsParts is an instructions block before it is rendered: what one
// provider's Apply builds, and what several providers sharing one file are
// merged from (#2449).
type InstructionsParts struct {
	Provider configurator.Provider
	Import   bool // the "@AGENTS.md" line a Claude Code CLAUDE.md starts with (D293)
	Sections []InstructionsSection
	Shared   string // the operational section (D1182)
	Paths    string // the "Local paths" section
	Env      string // client environment section, per client
	CLI      string // cli sentence, per client
	// WithheldKBs are the KBs this provider's block leaves out and Restricted
	// says it withholds this machine's paths or lineage KBs (the file is tracked
	// by git): the merge keeps the most restrictive sharer's choice.
	WithheldKBs []string
	Restricted  bool
}

// Render is the block body: sections, shared section, import first, paths, then
// the per-client text.
func (p InstructionsParts) Render() string {
	texts := make([]string, 0, len(p.Sections))
	for _, s := range p.Sections {
		texts = append(texts, s.Text)
	}
	body := strings.Join(texts, "\n\n")
	if p.Shared != "" {
		body += "\n\n" + p.Shared
	}
	if p.Import {
		body = "@AGENTS.md\n\n" + body
	}
	for _, extra := range []string{p.Paths, p.Env, p.CLI} {
		if extra != "" {
			body += "\n\n" + extra
		}
	}
	return body
}

// MergeInstructionsParts is the one block several providers write into the same
// file, in a fixed order of providers: the KB sections of all of them without
// duplicates (the first provider's order, then the new ones), each per-client
// text once, labelled by client when they differ. It keeps the most restrictive
// sharer's choice: a KB any sharer withholds is left out, the paths section is
// the restricted one's, and a restricted block carries no client text of a
// sharer that did not restrict (it may name a withheld KB).
func MergeInstructionsParts(all []InstructionsParts) InstructionsParts {
	var out InstructionsParts
	if len(all) == 0 {
		return out
	}
	all = append([]InstructionsParts(nil), all...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].Provider < all[j].Provider })
	out.Provider = all[0].Provider
	seen := map[string]bool{}
	for _, p := range all {
		out.Restricted = out.Restricted || p.Restricted
		for _, kb := range p.WithheldKBs {
			seen[kb] = true
		}
	}
	for _, p := range all {
		out.Import = out.Import || p.Import
		if out.Shared == "" {
			out.Shared = p.Shared
		}
		if out.Paths == "" || (p.Restricted && !restrictedPaths(all, out.Paths)) {
			out.Paths = p.Paths
		}
		for _, s := range p.Sections {
			if !seen[s.Name] {
				seen[s.Name] = true
				out.Sections = append(out.Sections, s)
			}
		}
	}
	keep := all
	if out.Restricted {
		keep = nil
		for _, p := range all {
			if p.Restricted {
				keep = append(keep, p)
			}
		}
	}
	out.Env = perClient(keep, len(all), func(p InstructionsParts) string { return p.Env })
	out.CLI = perClient(all, len(all), func(p InstructionsParts) string { return p.CLI })
	return out
}

// restrictedPaths reports whether paths is the paths section of a restricted sharer.
func restrictedPaths(all []InstructionsParts, paths string) bool {
	for _, p := range all {
		if p.Restricted && p.Paths == paths {
			return true
		}
	}
	return false
}

// perClient joins the distinct texts pick returns; a text every sharer has is
// kept as it is, any other gets a "For <clients>:" line.
func perClient(all []InstructionsParts, sharers int, pick func(InstructionsParts) string) string {
	var texts []string
	clients := map[string][]string{}
	for _, p := range all {
		t := pick(p)
		if t == "" {
			continue
		}
		if _, ok := clients[t]; !ok {
			texts = append(texts, t)
		}
		clients[t] = append(clients[t], string(p.Provider))
	}
	if len(texts) == 0 || (len(texts) == 1 && len(clients[texts[0]]) == sharers) {
		return strings.Join(texts, "")
	}
	for i, t := range texts {
		texts[i] = "For " + strings.Join(clients[t], ", ") + ":\n" + t
	}
	return strings.Join(texts, "\n\n")
}
