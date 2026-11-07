package plugin

import "testing"

func TestToolNameTurnsHyphensIntoUnderscores(t *testing.T) {
	for _, c := range []struct{ plugin, tool, want string }{
		{"example-forks", "echo", "example_forks_echo"},
		{"acme", "do-it", "acme_do_it"},
		{"acme", "x", "acme_x"},
	} {
		if got := ToolName(c.plugin, c.tool); got != c.want {
			t.Errorf("ToolName(%q, %q) = %q, want %q", c.plugin, c.tool, got, c.want)
		}
	}
}
