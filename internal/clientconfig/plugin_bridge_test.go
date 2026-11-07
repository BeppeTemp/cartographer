package clientconfig

import "testing"

// The transport rule: a binding or an active plugin with tools selects the
// bridge; an access profile or a missing routed endpoint never does.
func TestUsesBridgeTruthTable(t *testing.T) {
	tools := map[string]PluginEntry{"team": {Source: "/p", Enabled: true, Approved: "a", Tools: []string{"team_x"}}}
	cases := []struct {
		name           string
		bound, profile bool
		plugins        map[string]PluginEntry
		routed         string
		bridge, only   bool
	}{
		{"neither", false, false, nil, "/r", false, false},
		{"binding", true, false, nil, "/r", true, false},
		{"plugin tools", false, false, tools, "/r", true, true},
		{"both", true, false, tools, "/r", true, false},
		{"access profile wins over binding", true, true, nil, "/r", false, false},
		{"access profile wins over plugin tools", false, true, tools, "/r", false, false},
		{"no routed endpoint", false, false, tools, "", false, false},
		{"plugin without tools", false, false, map[string]PluginEntry{"team": {Source: "/p", Enabled: true, Approved: "a"}}, "/r", false, false},
		{"disabled plugin", false, false, map[string]PluginEntry{"team": {Source: "/p", Approved: "a", Tools: []string{"team_x"}}}, "/r", false, false},
		{"pending plugin", false, false, map[string]PluginEntry{"team": {Source: "/p", Enabled: true, Tools: []string{"team_x"}}}, "/r", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{KnownKBs: []string{"kb-a"}, Plugins: c.plugins}
			if c.bound {
				if err := cfg.BindWorkspace("claude", t.TempDir(), []string{"kb-a"}); err != nil {
					t.Fatal(err)
				}
			}
			if c.profile {
				cfg.Clients = map[string]ClientBinding{"claude": {Access: AccessReadMin}}
			}
			if got := cfg.UsesBridge("claude", c.routed); got != c.bridge {
				t.Errorf("UsesBridge = %v, want %v", got, c.bridge)
			}
			if got := cfg.PluginOnlyBridge("claude", c.routed); got != c.only {
				t.Errorf("PluginOnlyBridge = %v, want %v", got, c.only)
			}
		})
	}
}
