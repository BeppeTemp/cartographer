package mcpserver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestBundledSkillToolListsAreAdvertised: the tools a bundled skill lists in its
// "- Tools:" reference bullet are the ones its procedure calls, and a
// descriptor-bound client cannot call by name what tools/list omits (D318,
// D1191, D-new-ingest-setup-visible). A tool a skill may reach only by name
// is named on a line that says it is "not in `tools/list`".
func TestBundledSkillToolListsAreAdvertised(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "skillbundle", "bundled")
	skills, err := filepath.Glob(filepath.Join(root, "*", "SKILL.md"))
	if err != nil || len(skills) == 0 {
		t.Fatalf("no bundled SKILL.md under %s (err %v)", root, err)
	}
	// One-word tools (reindex) too: IsToolName drops what is not a tool.
	name := regexp.MustCompile("`([a-z]+(?:_[a-z]+)*)`")
	checked := 0
	for _, path := range skills {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		inBullet := false
		for _, line := range strings.Split(string(data), "\n") {
			switch {
			case strings.HasPrefix(line, "- Tools:"):
				inBullet = true
			case inBullet && strings.HasPrefix(line, "  "):
			default:
				inBullet = false
			}
			// A line that says the tool is not in tools/list warns the agent itself.
			if !inBullet || strings.Contains(line, "not in `tools/list`") {
				continue
			}
			for _, m := range name.FindAllStringSubmatch(line, -1) {
				if !IsToolName(m[1]) {
					continue
				}
				checked++
				if ToolAdvanced(m[1]) {
					t.Errorf("%s lists %q under \"- Tools:\", but tools/list hides it (advancedToolNames, visibility.go)", filepath.Base(filepath.Dir(path)), m[1])
				}
			}
		}
	}
	if checked < 10 {
		t.Fatalf("only %d tool names found in \"- Tools:\" bullets: the scan no longer matches the skills", checked)
	}
}
