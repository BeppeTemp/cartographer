package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// kbFullPayload is the sync_pull answer of a KB with a skill, a subagent and
// the generated instructions artifact (routing paragraph + skills line).
func kbFullPayload(t *testing.T, kb, skill string) string {
	t.Helper()
	type file struct{ path, content string }
	mk := func(kind, name string, files ...file) map[string]any {
		pf := make([]provisioning.ArtifactFile, 0, len(files))
		jf := make([]map[string]any, 0, len(files))
		for _, f := range files {
			pf = append(pf, provisioning.ArtifactFile{Path: f.path, Content: []byte(f.content)})
			jf = append(jf, map[string]any{"path": f.path, "content_b64": base64.StdEncoding.EncodeToString([]byte(f.content))})
		}
		return map[string]any{"kind": kind, "name": name, "source": "kb:" + kb, "content_hash": provisioning.ContentHashFiles(pf), "files": jf}
	}
	instr := "The \"" + kb + "\" KB is served via MCP by the \"cartographer\" server. Maps: entities/.\n\nSkills: " + skill + ".\n"
	body, err := json.Marshal(map[string]any{
		"revision": "rev-" + kb,
		"artifacts": []map[string]any{
			mk("skill", skill, file{"SKILL.md", "# " + skill + "\n"}),
			mk("agent", "agent-"+kb, file{"agent-" + kb + ".md", "# agent\n"}),
			mk("instructions", kb, file{"instructions.md", instr}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// syncOnce runs the real manifest build + materialize for claude under dir.
func syncOnce(t *testing.T, cfg *clientconfig.Config, dir string) {
	t.Helper()
	ms, err := manifestsForProviders(cfg, cfg.Agents)
	if err != nil {
		t.Fatalf("manifestsForProviders: %v", err)
	}
	if _, err := materializeForProviders(ms, globalProjections(cfg.Agents, dir), dir, "", false, false, portabilityOptions{}, nil); err != nil {
		t.Fatalf("materialize: %v", err)
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// TestKBArtifactsOptOutPrunesAndRestores (#1408): artifacts: false removes what
// was installed through the lockfile-driven prune, drops the skills line and the
// subagent sentence from the block, keeps the routing sentence, is idempotent,
// and flipping back restores everything.
func TestKBArtifactsOptOutPrunesAndRestores(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": "", "kb-b": ""}, "sync_pull", func(kb string) string {
		return kbFullPayload(t, kb, "skill-"+kb)
	})
	defer srv.Close()
	dir := t.TempDir()
	setHome(t, dir)
	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", KnownKBs: []string{"kb-a", "kb-b"}, Agents: []string{"claude"}}

	skillA := filepath.Join(dir, ".claude", "skills", "skill-kb-a", "SKILL.md")
	skillB := filepath.Join(dir, ".claude", "skills", "skill-kb-b", "SKILL.md")
	agentA := filepath.Join(dir, ".claude", "agents", "agent-kb-a.md")
	block := func() string {
		b, err := os.ReadFile(filepath.Join(dir, ".claude", "CLAUDE.md"))
		if err != nil {
			t.Fatalf("block: %v", err)
		}
		return string(b)
	}

	syncOnce(t, cfg, dir)
	if !exists(skillA) || !exists(agentA) || !strings.Contains(block(), "Skills: skill-kb-a.") || !strings.Contains(block(), "Subagents installed by the \"kb-a\" KB") {
		t.Fatalf("baseline not installed:\n%s", block())
	}

	cfg.SetKBArtifacts("kb-a", false)
	syncOnce(t, cfg, dir)
	if exists(skillA) || exists(agentA) {
		t.Error("kb-a artifacts survived the opt-out")
	}
	if !exists(skillB) {
		t.Error("kb-b was touched")
	}
	got := block()
	for _, want := range []string{"The \"kb-a\" KB is served via MCP", "Skills: skill-kb-b."} {
		if !strings.Contains(got, want) {
			t.Errorf("block lost %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"Skills: skill-kb-a.", "Subagents installed by the \"kb-a\" KB"} {
		if strings.Contains(got, bad) {
			t.Errorf("block still has %q:\n%s", bad, got)
		}
	}

	// A second sync on the opted-out state changes nothing.
	syncOnce(t, cfg, dir)
	if again := block(); again != got {
		t.Errorf("second sync rewrote the block:\n%s\n---\n%s", got, again)
	}

	cfg.SetKBArtifacts("kb-a", true)
	syncOnce(t, cfg, dir)
	if !exists(skillA) || !exists(agentA) || !strings.Contains(block(), "Skills: skill-kb-a.") {
		t.Errorf("flip back did not restore:\n%s", block())
	}
}

// TestKBArtifactsOptOutAvoidsCollision: the filter runs before the strict
// merge, so an opted-out KB's skill no longer collides with another KB's.
func TestKBArtifactsOptOutAvoidsCollision(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": "", "kb-b": ""}, "sync_pull", func(kb string) string {
		return kbFullPayload(t, kb, "shared")
	})
	defer srv.Close()
	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", KnownKBs: []string{"kb-a", "kb-b"}, Agents: []string{"claude"}}
	if _, err := manifestsForProviders(cfg, cfg.Agents); err == nil {
		t.Fatal("expected a collision without the option")
	}
	cfg.SetKBArtifacts("kb-a", false)
	ms, err := manifestsForProviders(cfg, cfg.Agents)
	if err != nil {
		t.Fatalf("opted-out KB still collides: %v", err)
	}
	var kinds []string
	for _, a := range ms["claude"].Artifacts {
		if a.Source == "kb:kb-a" {
			kinds = append(kinds, a.Kind)
		}
	}
	if strings.Join(kinds, ",") != "instructions" {
		t.Errorf("kb-a keeps %v, want only its instructions", kinds)
	}
}

func TestDropKBArtifactsKeepsBundleAndOtherKBs(t *testing.T) {
	cfg := &clientconfig.Config{}
	cfg.SetKBArtifacts("kb-a", false)
	in := []provisioning.Artifact{
		{Kind: "skill", Name: "s", Source: "kb:kb-a"},
		{Kind: "agent", Name: "a", Source: "kb:kb-a"},
		{Kind: "hook", Name: "h", Source: "kb:kb-a"},
		{Kind: "mcp", Name: "m", Source: "kb:kb-a"},
		{Kind: "instructions", Name: "kb-a", Source: "kb:kb-a"},
		{Kind: "skill", Name: "s", Source: "kb:kb-b"},
		{Kind: "skill", Name: "b", Source: "bundle"},
	}
	out := dropKBArtifacts(cfg, in)
	var got []string
	for _, a := range out {
		got = append(got, a.Source+"/"+a.Kind)
	}
	if want := "kb:kb-a/instructions,kb:kb-b/skill,bundle/skill"; strings.Join(got, ",") != want {
		t.Errorf("got %v, want %s", got, want)
	}
}

// TestKBArtifactsOptOutInAWorkspaceProjection: the option narrows a workspace
// projection too (derived or explicit): the KB's instructions stay, its skill
// and agent are gone and its skills line is stripped.
func TestKBArtifactsOptOutInAWorkspaceProjection(t *testing.T) {
	srv := prefixAwareMCPServer(t, map[string]string{"kb-a": "", "kb-b": ""}, "sync_pull", func(kb string) string {
		return kbFullPayload(t, kb, "skill-"+kb)
	})
	defer srv.Close()
	ws := t.TempDir()
	cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", KnownKBs: []string{"kb-a", "kb-b"}, Agents: []string{"claude"}}
	cfg.SetKBArtifacts("kb-a", false)
	p := syncProjection{Provider: "claude", Workspace: ws, BaseDir: ws, Scope: provisioning.ScopeProject, KBs: []string{"kb-a", "kb-b"}}
	ms, _, err := buildProjectionManifests(cfg, []syncProjection{p}, false, nil)
	if err != nil {
		t.Fatalf("buildProjectionManifests: %v", err)
	}
	var got []string
	for _, a := range ms[projectionKey(p)].Artifacts {
		got = append(got, a.Source+"/"+a.Kind)
		if a.Source == "kb:kb-a" && a.Kind == "instructions" && strings.Contains(string(a.Files[0].Content), "Skills:") {
			t.Errorf("skills line kept: %q", a.Files[0].Content)
		}
	}
	for _, bad := range []string{"kb:kb-a/skill", "kb:kb-a/agent"} {
		if strings.Contains(strings.Join(got, ","), bad) {
			t.Errorf("%s survived: %v", bad, got)
		}
	}
	if !strings.Contains(strings.Join(got, ","), "kb:kb-a/instructions") || !strings.Contains(strings.Join(got, ","), "kb:kb-b/skill") {
		t.Errorf("manifest = %v", got)
	}
}

// TestStatusExplainsArtifactsOff: a KB whose skills are missing by setting says
// so in status, so it does not look like a bug.
func TestStatusExplainsArtifactsOff(t *testing.T) {
	out := withStdout(t, func() {
		printBindingLine(providerStatus{Name: "claude", BindingOrigin: "default", BoundKBs: []string{"kb-a", "kb-b"}, ArtifactsOff: []string{"kb-a"}})
	})
	if !strings.Contains(out, "kb-a artifacts: off (kb_options)") || strings.Contains(out, "kb-b artifacts") {
		t.Errorf("status line: %q", out)
	}
}
