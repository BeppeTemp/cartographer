package contract

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

func TestParseAgentClients(t *testing.T) {
	cases := []struct {
		name, fm string
		want     []string
		errSub   string
	}{
		{"absent", "name: x\n", nil, ""},
		{"flow list", "clients: [claude, kiro]\n", []string{"claude", "kiro"}, ""},
		{"block list", "clients:\n  - claude\n  - codex\n", []string{"claude", "codex"}, ""},
		{"case and duplicates", "clients: [Kiro, kiro, CLAUDE]\n", []string{"kiro", "claude"}, ""},
		{"unknown", "clients: [claud]\n", nil, `unknown client "claud"`},
		{"empty", "clients: []\n", nil, "is empty"},
		{"scalar", "clients: kiro\n", nil, "must be a list"},
	}
	for _, c := range cases {
		fm, err := okf.ParseFrontmatter(c.fm)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, err := ParseAgentClients(fm)
		if c.errSub != "" {
			if err == nil || !strings.Contains(err.Error(), c.errSub) || !strings.Contains(err.Error(), "supported:") {
				t.Errorf("%s: err = %v, want %q naming the supported clients", c.name, err, c.errSub)
			}
			if verr := ValidateAgentClients(fm); verr == nil {
				t.Errorf("%s: ValidateAgentClients accepted it", c.name)
			}
			continue
		}
		if err != nil || strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: got %v, %v want %v", c.name, got, err, c.want)
		}
	}
}

func TestValidateAgentFile_RefusesUnknownClient(t *testing.T) {
	src := "---\nname: x\ndescription: d\nclients: [claud]\n---\nb\n"
	if err := ValidateAgentFile("x", []byte(src)); err == nil || !strings.Contains(err.Error(), "clients") {
		t.Fatalf("clients: [claud] accepted: %v", err)
	}
}

func TestAgentSkipReason(t *testing.T) {
	a := Artifact{Kind: "agent", Name: "x", Clients: []string{"kiro"}}
	if r := AgentSkipReason(a, configurator.ProviderClaudeCode); r != "clients" {
		t.Errorf("claude: %q", r)
	}
	if r := AgentSkipReason(a, configurator.ProviderKiro); r != "" {
		t.Errorf("kiro: %q", r)
	}
	a.Clients = nil
	if AgentSkipped(a, configurator.ProviderClaudeCode) {
		t.Error("no allow-list must skip nothing")
	}
}
