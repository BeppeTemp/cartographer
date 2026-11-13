package provisioning

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

const kiroOnlyAgent = "---\nname: x\ndescription: Kiro only\nclients: [kiro]\n---\nBody.\n"
const everywhereAgent = "---\nname: x\ndescription: Everywhere\n---\nBody.\n"

func clientsAgentArt(src string) contract.Artifact {
	a := agentArt("x", src)
	a.Clients = contract.AgentClientsOf([]byte(src))
	return a
}

// A repository that tracks .claude/agents/x.md and a KB agent of the same name
// for Kiro only: Claude's projection owns nothing there, so the hygiene check
// does not refuse it; without the allow-list it does.
func TestWorkspaceHygiene_ClientsAllowListDoesNotShadowTrackedAgent(t *testing.T) {
	withTracked(t, map[string]bool{".claude/agents/x.md": true})
	dir := t.TempDir()
	owned := func(src string) []string {
		pm, _ := ProjectManifest(manifestOf(clientsAgentArt(src)), configurator.ProviderClaudeCode, contract.ScopeProject, contract.ProjectOptions{})
		paths, err := ProjectOwnedArtifactPaths(configurator.ProviderClaudeCode, pm, Lock{})
		if err != nil {
			t.Fatal(err)
		}
		return paths
	}
	if err := CheckWorkspaceHygiene("claude", dir, owned(kiroOnlyAgent), nil); err != nil {
		t.Fatalf("a Kiro-only agent blocked Claude: %v", err)
	}
	err := CheckWorkspaceHygiene("claude", dir, owned(everywhereAgent), nil)
	var collision *TrackedCollisionError
	if !errors.As(err, &collision) {
		t.Fatalf("default agent over a tracked file: err = %v, want TrackedCollisionError", err)
	}
}

// Narrowing `clients:` removes what an earlier sync wrote for that client.
func TestApply_ClientsNarrowedPrunesTheAgent(t *testing.T) {
	base := t.TempDir()
	kb := t.TempDir()
	if err := os.MkdirAll(filepath.Join(kb, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(src string) contract.Manifest {
		if err := os.WriteFile(filepath.Join(kb, "agents", "x.md"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		m, err := contract.BuildManifest(nil, map[string]string{"kb-a": kb}, contract.BuildOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	opts := ApplyOptions{KBRoots: map[string]string{"kb-a": kb}, Provider: configurator.ProviderCodex, BaseDir: base}
	res, err := Apply(write(everywhereAgent), opts)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(base, ".codex", "agents", "x.toml")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("setup: %v", err)
	}
	opts.Lock = res.NewLock
	if _, err := Apply(write(kiroOnlyAgent), opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the agent survived the narrowing: %v", err)
	}
}

func TestBuildManifest_ClientsDerivedAndBadListReported(t *testing.T) {
	kb := t.TempDir()
	if err := os.MkdirAll(filepath.Join(kb, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := "---\nname: bad\ndescription: d\nclients: [claud]\n---\nb\n"
	for name, src := range map[string]string{"x": kiroOnlyAgent, "bad": bad} {
		if err := os.WriteFile(filepath.Join(kb, "agents", name+".md"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m, err := contract.BuildManifest(nil, map[string]string{"kb-a": kb}, contract.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range m.Artifacts {
		switch a.Name {
		case "x":
			if len(a.Clients) != 1 || a.Clients[0] != "kiro" {
				t.Errorf("x.Clients = %v", a.Clients)
			}
		case "bad":
			if a.Clients != nil {
				t.Errorf("an invalid list must leave the default (every client): %v", a.Clients)
			}
		}
	}
	if len(m.Issues) != 1 || !strings.Contains(m.Issues[0], contract.AgentFrontmatterIssuePrefix("kb-a", "bad")) {
		t.Errorf("issues = %v", m.Issues)
	}
}
