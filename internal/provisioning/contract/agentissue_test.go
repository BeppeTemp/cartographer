package contract

import "testing"

// The text is matched by clients of other versions against the issues of a
// server they were not built with (status recognises an agent held for an
// unparsable frontmatter by it, #1984): changing it silently turns every
// older client's "held" line back into a false "not in the catalogue". Change
// the literal only together with a fallback that still recognises the old one.
func TestAgentFrontmatterIssuePrefix_IsStable(t *testing.T) {
	got := AgentFrontmatterIssuePrefix("kb-a", "bad-agent")
	want := `KB "kb-a": agent "bad-agent": its frontmatter is not valid YAML`
	if got != want {
		t.Errorf("AgentFrontmatterIssuePrefix = %q, want %q", got, want)
	}
}
