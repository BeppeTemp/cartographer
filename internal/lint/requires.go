package lint

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// checkRequires reports every `requires` declaration the artifact write gate
// would refuse (requires_invalid), reading the same carriers and calling the
// same provisioning.ValidateRequires: a skill's SKILL.md frontmatter, an
// agent's frontmatter, a hook's hook.json, plus the two places the key has no
// meaning, instructions.md and an mcp/*.json descriptor. A KB written before
// the gate, or edited by git, surfaces its broken declarations here instead of
// having them skipped silently at sync. A hook with a bad `requires` still
// registers, so the message says the declaration is what is wrong.
func checkRequires(k *kb.KB, files []artifactText) []Finding {
	var findings []Finding
	add := func(rel, msg string) {
		findings = append(findings, newFinding("requires_invalid", Finding{Path: rel, Message: msg}))
	}
	for _, f := range files {
		switch {
		case strings.HasPrefix(f.rel, "skills/") && strings.HasSuffix(f.rel, "/SKILL.md") && strings.Count(f.rel, "/") == 2:
			name := strings.Split(f.rel, "/")[1]
			if err := frontmatterRequires("skill", name, f.content); err != nil {
				add(f.rel, fmt.Sprintf("skill %q: invalid requires: %v", name, err))
			}
		case strings.HasPrefix(f.rel, "agents/") && strings.Count(f.rel, "/") == 1:
			name := strings.TrimSuffix(strings.TrimPrefix(f.rel, "agents/"), ".md")
			if err := frontmatterRequires("agent", name, f.content); err != nil {
				add(f.rel, fmt.Sprintf("agent %q: invalid requires: %v", name, err))
			}
		case f.rel == "instructions.md":
			if fmRaw, _, ok := okf.SplitFrontmatter(f.content); ok {
				if fm, err := okf.ParseFrontmatter(fmRaw); err == nil {
					if _, has := fm.Get(provisioning.RequiresKey); has {
						add(f.rel, fmt.Sprintf("instructions.md cannot declare %q: it applies to skills, agents and hooks only", provisioning.RequiresKey))
					}
				}
			}
		}
	}
	findings = append(findings, hookRequiresFindings(k)...)
	findings = append(findings, mcpRequiresFindings(k)...)
	return findings
}

// frontmatterRequires validates the `requires` of a skill or agent text. A
// missing or unparseable frontmatter is not this check's business: skill_invalid
// and the agent checks own it.
func frontmatterRequires(kind, name, content string) error {
	fmRaw, _, ok := okf.SplitFrontmatter(content)
	if !ok {
		return nil
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		return nil
	}
	return provisioning.ValidateFrontmatterRequires(kind, name, fm)
}

func hookRequiresFindings(k *kb.KB) []Finding {
	entries, err := os.ReadDir(filepath.Join(k.Root, "hooks"))
	if err != nil {
		return nil
	}
	var findings []Finding
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rel := "hooks/" + e.Name() + "/hook.json"
		data, err := os.ReadFile(filepath.Join(k.Root, filepath.FromSlash(rel)))
		if err != nil {
			continue // hook_invalid reports a missing hook.json
		}
		if err := provisioning.ValidateHookRequires(e.Name(), data); err != nil {
			// Malformed JSON is hook_invalid's finding, not a second one here.
			if strings.HasPrefix(err.Error(), "invalid JSON") {
				continue
			}
			findings = append(findings, newFinding("requires_invalid", Finding{
				Path:    rel,
				Message: fmt.Sprintf("hook %q: invalid requires, ignored at sync (the hook is still registered): %v", e.Name(), err),
			}))
		}
	}
	return findings
}

func mcpRequiresFindings(k *kb.KB) []Finding {
	entries, err := os.ReadDir(filepath.Join(k.Root, "mcp"))
	if err != nil {
		return nil
	}
	var findings []Finding
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		rel := "mcp/" + e.Name()
		data, err := os.ReadFile(filepath.Join(k.Root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		if mcpHasRequires(data) {
			findings = append(findings, newFinding("requires_invalid", Finding{
				Path:    rel,
				Message: fmt.Sprintf("mcp descriptor %s cannot declare %q: it applies to skills, agents and hooks only", e.Name(), provisioning.RequiresKey),
			}))
		}
	}
	return findings
}

// checkDegradedNameCollision reports an agent agents/X.md next to a skill
// skills/X/: on a client without subagents the agent is degraded to a skill of
// the same name, and the real skill wins, so the agent never reaches it. The
// finding sits on the agent path.
func checkDegradedNameCollision(k *kb.KB) []Finding {
	entries, err := os.ReadDir(filepath.Join(k.Root, "agents"))
	if err != nil {
		return nil
	}
	var findings []Finding
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		fi, err := os.Stat(filepath.Join(k.Root, "skills", name))
		if err != nil || !fi.IsDir() {
			continue
		}
		findings = append(findings, newFinding("degraded_name_collision", Finding{
			Path:    "agents/" + e.Name(),
			Message: fmt.Sprintf("agent %q and skill %q share a name: on a client without subagents the skill wins and the agent is not degraded — rename one of them", name, name),
		}))
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Path < findings[j].Path })
	return findings
}

func mcpHasRequires(data []byte) bool {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil {
		return false
	}
	_, ok := raw[provisioning.RequiresKey]
	return ok
}
