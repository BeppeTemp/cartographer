package main

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func TestCheckUnboundResiduesPluginSource(t *testing.T) {
	dir := t.TempDir()
	lockFile := provisioning.LockFile{Providers: map[string]provisioning.Lock{"claude": {Provider: "claude", Managed: []provisioning.ManagedFile{
		{Kind: "skill", Name: "live", Path: "a", ContentHash: "h", Source: "plugin:team-forks"},
		{Kind: "skill", Name: "orphan", Path: "b", ContentHash: "h", Source: "plugin:removed"},
		{Kind: "skill", Name: "kb-gone", Path: "c", ContentHash: "h", Source: "kb:gone"},
		{Kind: "skill", Name: "kb-bound", Path: "d", ContentHash: "h", Source: "kb:bound"},
	}}}}
	plugins := map[string]clientconfig.PluginEntry{"team-forks": {Source: "/x"}}
	for _, explicit := range []bool{true, false} {
		cfg := &clientconfig.Config{Agents: []string{"claude"}, KnownKBs: []string{"bound"}, Plugins: plugins}
		if explicit {
			cfg.Clients = map[string]clientconfig.ClientBinding{"claude": {KBs: []string{"bound"}}}
		}
		var msgs []string
		for _, f := range checkUnboundResidues(dir, cfg, []string{"claude"}, lockFile) {
			msgs = append(msgs, f.Message)
		}
		joined := strings.Join(msgs, "\n")
		// A configured plugin is never a residue; a removed one is, for every provider.
		if strings.Contains(joined, "skill/live") {
			t.Errorf("explicit=%v: a configured plugin was reported:\n%s", explicit, joined)
		}
		if !strings.Contains(joined, "skill/orphan comes from plugin removed, which is no longer installed") {
			t.Errorf("explicit=%v: the removed plugin was not reported:\n%s", explicit, joined)
		}
		// The KB behaviour is unchanged: only an explicit binding reports an unbound KB.
		if got := strings.Contains(joined, "kb:gone"); got != explicit {
			t.Errorf("explicit=%v: kb:gone reported = %v:\n%s", explicit, got, joined)
		}
		if strings.Contains(joined, "kb:bound") {
			t.Errorf("explicit=%v: a bound KB was reported:\n%s", explicit, joined)
		}
	}
}

func TestPluginSkillSurvivesKBArtifactsOptOut(t *testing.T) {
	cfg := &clientconfig.Config{}
	cfg.SetKBArtifacts("kb-a", false)
	out := dropKBArtifacts(cfg, []provisioning.Artifact{
		{Kind: "skill", Name: "s", Source: "kb:kb-a"},
		{Kind: "skill", Name: "p", Source: "plugin:team-forks"},
		{Kind: "hook", Name: "h", Source: "plugin:team-forks"},
	})
	if len(out) != 2 || out[0].Source != "plugin:team-forks" || out[1].Source != "plugin:team-forks" {
		t.Errorf("got %+v", out)
	}
}

func TestSnapshotTrustOfAPluginArtifactIsPlugin(t *testing.T) {
	got := snapshotArtifacts([]provisioning.Artifact{
		{Kind: "hook", Name: "h", Source: "plugin:team-forks", ContentHash: "x"},
		{Kind: "skill", Name: "s", Source: "plugin:team-forks", ContentHash: "x"},
	}, &clientconfig.Config{})
	for _, a := range got {
		if a.Trust != "plugin" {
			t.Errorf("%s/%s trust = %q, want plugin", a.Kind, a.Name, a.Trust)
		}
	}
}
