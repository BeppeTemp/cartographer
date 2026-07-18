package provisioning_test

// Tests for provisioning extended to kinds "agent" and "hook" (D48). See
// provisioning_test.go for the pre-existing tests on kind "skill".

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// --- BuildManifest: agent/hook from KBs ---

func TestBuildManifest_AgentKB(t *testing.T) {
	kbRoot := t.TempDir()
	agentsDir := filepath.Join(kbRoot, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("---\nname: reviewer\n---\nBody.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := provisioning.BuildManifest(nil, map[string]string{"mia-kb": kbRoot}, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	var found *provisioning.Artifact
	for i, a := range m.Artifacts {
		if a.Kind == "agent" && a.Name == "reviewer" {
			found = &m.Artifacts[i]
		}
	}
	if found == nil {
		t.Fatalf("BuildManifest: expected agent/reviewer artifact, got %+v", m.Artifacts)
	}
	if found.Source != "kb:mia-kb" {
		t.Errorf("Source: expected kb:mia-kb, got %q", found.Source)
	}
	if found.ContentHash == "" {
		t.Error("empty ContentHash for agent")
	}
}

func TestBuildManifest_AgentHash_Determinismo(t *testing.T) {
	kbRoot := t.TempDir()
	agentsDir := filepath.Join(kbRoot, "agents")
	os.MkdirAll(agentsDir, 0o755)
	os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("Body v1.\n"), 0o644)

	m1, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, false)
	if err != nil {
		t.Fatalf("BuildManifest 1: %v", err)
	}
	m2, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, false)
	if err != nil {
		t.Fatalf("BuildManifest 2: %v", err)
	}
	if m1.Artifacts[0].ContentHash != m2.Artifacts[0].ContentHash {
		t.Error("non-deterministic agent hash")
	}

	os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("Body v2.\n"), 0o644)
	m3, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, false)
	if err != nil {
		t.Fatalf("BuildManifest 3: %v", err)
	}
	if m1.Artifacts[0].ContentHash == m3.Artifacts[0].ContentHash {
		t.Error("agent hash: different content must produce a different hash")
	}
}

func TestBuildManifest_HookKB(t *testing.T) {
	kbRoot := t.TempDir()
	hookDir := filepath.Join(kbRoot, "hooks", "notify-on-commit")
	if err := os.MkdirAll(hookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(hookDir, "hook.json"), []byte(`{"event":"PostToolUse","matcher":"concept_write","command":"./notify.sh"}`), 0o644)
	os.WriteFile(filepath.Join(hookDir, "notify.sh"), []byte("#!/bin/sh\necho ok\n"), 0o755)

	m, err := provisioning.BuildManifest(nil, map[string]string{"mia-kb": kbRoot}, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	var found *provisioning.Artifact
	for i, a := range m.Artifacts {
		if a.Kind == "hook" && a.Name == "notify-on-commit" {
			found = &m.Artifacts[i]
		}
	}
	if found == nil {
		t.Fatalf("BuildManifest: expected hook/notify-on-commit artifact, got %+v", m.Artifacts)
	}
	if found.ContentHash == "" {
		t.Error("empty ContentHash for hook")
	}
}

func TestBuildManifest_HookHash_MultiFile_OrdineIndipendente(t *testing.T) {
	// The hook's aggregate hash (multi-file dir) must be the same hash used
	// for skills: ContentHashDirOS, already tested for independence
	// from iteration order — here we only verify that it's reused
	// (same hash for identical content, different hash for different content).
	kbRoot1 := t.TempDir()
	hookDir1 := filepath.Join(kbRoot1, "hooks", "h")
	os.MkdirAll(hookDir1, 0o755)
	os.WriteFile(filepath.Join(hookDir1, "hook.json"), []byte(`{"a":1}`), 0o644)
	os.WriteFile(filepath.Join(hookDir1, "run.sh"), []byte("echo a"), 0o644)

	kbRoot2 := t.TempDir()
	hookDir2 := filepath.Join(kbRoot2, "hooks", "h")
	os.MkdirAll(hookDir2, 0o755)
	os.WriteFile(filepath.Join(hookDir2, "hook.json"), []byte(`{"a":1}`), 0o644)
	os.WriteFile(filepath.Join(hookDir2, "run.sh"), []byte("echo a"), 0o644)

	h1, err := provisioning.ContentHashDirOS(hookDir1)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := provisioning.ContentHashDirOS(hookDir2)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Errorf("multi-file hook hash: identical content must produce the same hash: %q != %q", h1, h2)
	}

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot1}, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	var hookHash string
	for _, a := range m.Artifacts {
		if a.Kind == "hook" {
			hookHash = a.ContentHash
		}
	}
	if hookHash != h1 {
		t.Errorf("BuildManifest hook hash: expected %q (ContentHashDirOS), got %q — not reusing the existing hashing function", h1, hookHash)
	}
}

func TestBuildManifest_NoAgentsHooksDir_ZeroArtefatti(t *testing.T) {
	// Backward compat: a KB with no agents/ or hooks/ must not fail nor emit
	// artifacts of those kinds.
	kbRoot := t.TempDir()
	os.MkdirAll(filepath.Join(kbRoot, "skills"), 0o755)

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	for _, a := range m.Artifacts {
		if a.Kind == "agent" || a.Kind == "hook" {
			t.Errorf("expected no agent/hook artifact without the corresponding dirs, found %+v", a)
		}
	}
}

// --- destDir for kind x provider (via Apply, destDir is not exported) ---

func TestApply_DestDir_Matrix(t *testing.T) {
	cases := []struct {
		kind         string
		provider     configurator.Provider
		materializes bool
		wantSuffix   string // expected suffix of the written path (only if materializes)
	}{
		{"skill", configurator.ProviderClaudeCode, true, filepath.Join(".claude", "skills", "art", "SKILL.md")},
		{"skill", configurator.ProviderOpenCode, true, filepath.Join(".opencode", "skills", "art", "SKILL.md")},
		{"skill", configurator.ProviderCodex, true, filepath.Join(".codex", "skills", "art", "SKILL.md")},
		{"skill", configurator.ProviderKiro, true, filepath.Join(".kiro", "skills", "art", "SKILL.md")},
		{"agent", configurator.ProviderClaudeCode, true, filepath.Join(".claude", "agents", "art.md")},
		{"agent", configurator.ProviderOpenCode, true, filepath.Join(".opencode", "agent", "art.md")},
		{"agent", configurator.ProviderCodex, true, filepath.Join(".codex", "agents", "art.toml")},
		{"agent", configurator.ProviderKiro, false, ""},
		{"hook", configurator.ProviderClaudeCode, true, filepath.Join(".claude", "hooks", "art", "hook.json")},
		{"hook", configurator.ProviderOpenCode, true, filepath.Join(".opencode", "hooks", "art", "hook.json")},
		{"hook", configurator.ProviderCodex, true, filepath.Join(".codex", "hooks", "art", "hook.json")},
		{"hook", configurator.ProviderKiro, false, ""},
	}

	for _, c := range cases {
		var mainFile string
		switch c.kind {
		case "skill":
			mainFile = "SKILL.md"
		case "agent":
			mainFile = "art.md"
		case "hook":
			mainFile = "hook.json"
		}

		a := provisioning.Artifact{
			Kind: c.kind, Name: "art", Source: "kb:x", ContentHash: "h", Signed: true,
			Files: []provisioning.ArtifactFile{{Path: mainFile, Content: []byte("x")}},
		}
		m := provisioning.MergeArtifacts([]provisioning.Artifact{a})
		baseDir := t.TempDir()
		res, err := provisioning.Apply(m, provisioning.ApplyOptions{
			Provider: c.provider,
			BaseDir:  baseDir,
			Lock:     provisioning.Lock{},
		})
		if err != nil {
			t.Fatalf("%s/%s: Apply: %v", c.kind, c.provider, err)
		}

		if c.materializes {
			if len(res.NeedsApproval) != 0 {
				t.Errorf("%s/%s: expected materialized, got NeedsApproval: %v", c.kind, c.provider, res.NeedsApproval)
			}
			if len(res.Written) != 1 {
				t.Fatalf("%s/%s: expected 1 file written, got %d", c.kind, c.provider, len(res.Written))
			}
			if res.Written[0].Path != c.wantSuffix {
				t.Errorf("%s/%s: expected path %q, got %q", c.kind, c.provider, c.wantSuffix, res.Written[0].Path)
			}
			if _, err := os.Stat(filepath.Join(baseDir, c.wantSuffix)); err != nil {
				t.Errorf("%s/%s: file not found on disk: %v", c.kind, c.provider, err)
			}
		} else {
			if len(res.Written) != 0 {
				t.Errorf("%s/%s: expected Unsupported, got Written: %v", c.kind, c.provider, res.Written)
			}
			// Kind with no destination for the provider → Unsupported, not
			// NeedsApproval: no approval would unblock it.
			if len(res.Unsupported) != 1 {
				t.Errorf("%s/%s: expected 1 artifact in Unsupported, got %d", c.kind, c.provider, len(res.Unsupported))
			}
			if len(res.NeedsApproval) != 0 {
				t.Errorf("%s/%s: NeedsApproval must stay empty for unsupported kinds: %v", c.kind, c.provider, res.NeedsApproval)
			}
		}
	}
}

// --- Apply: materializing agent/hook from a real KB ---

func TestApply_MaterializzaAgent(t *testing.T) {
	kbRoot := t.TempDir()
	agentsDir := filepath.Join(kbRoot, "agents")
	os.MkdirAll(agentsDir, 0o755)
	os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("---\nname: reviewer\n---\nBody.\n"), 0o644)

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	agentPath := filepath.Join(baseDir, ".claude", "agents", "reviewer.md")
	data, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("Apply: agent not materialized at %s: %v", agentPath, err)
	}
	if string(data) != "---\nname: reviewer\n---\nBody.\n" {
		t.Errorf("Apply: unexpected agent content: %s", data)
	}
	// .claude/agents/reviewer.md must be a file, not a directory.
	fi, err := os.Stat(agentPath)
	if err != nil || fi.IsDir() {
		t.Errorf("Apply: agent must be a single file, not a directory")
	}
	// autoTrust=true also generates the KB's "instructions" artifact (D56,
	// always present, independent of agents/): filter on kind "agent", not on the
	// absolute length of Written.
	agentWritten := writtenOfKind(res.Written, "agent")
	if len(agentWritten) != 1 {
		t.Errorf("Apply: unexpected Written[agent]: %+v (full Written: %+v)", agentWritten, res.Written)
	}
}

// --- Agent on OpenCode: frontmatter translation (D55) ---

func TestApply_OpenCode_MaterializzaAgent_ConFrontmatter(t *testing.T) {
	baseDir := t.TempDir()
	src := "---\nname: reviewer\ndescription: Reviews the code\ntools: Read, Grep\nmodel: sonnet\n---\nReviewer system prompt.\n"

	a := provisioning.Artifact{
		Kind: "agent", Name: "reviewer", Source: "kb:x", ContentHash: "h1", Signed: true,
		Files: []provisioning.ArtifactFile{{Path: "reviewer.md", Content: []byte(src)}},
	}
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderOpenCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.NeedsApproval) != 0 || len(res.Unsupported) != 0 {
		t.Fatalf("Apply opencode agent: atteso materializzato, NeedsApproval=%v Unsupported=%v", res.NeedsApproval, res.Unsupported)
	}

	agentPath := filepath.Join(baseDir, ".opencode", "agent", "reviewer.md")
	data, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("agent not materialized at %s: %v", agentPath, err)
	}

	want := "---\ndescription: Reviews the code\nmode: subagent\n---\nReviewer system prompt.\n"
	if string(data) != want {
		t.Errorf("unexpected translated content:\n%q\nexpected:\n%q", data, want)
	}
	// Non-mappable Claude-only fields must not appear.
	for _, unwanted := range []string{"tools:", "model:", "name:"} {
		if strings.Contains(string(data), unwanted) {
			t.Errorf("the translated frontmatter must not contain %q: %s", unwanted, data)
		}
	}
}

func TestApply_OpenCode_MaterializzaAgent_SenzaFrontmatter(t *testing.T) {
	baseDir := t.TempDir()
	src := "Body only, no frontmatter.\n"

	a := provisioning.Artifact{
		Kind: "agent", Name: "plain", Source: "kb:x", ContentHash: "h1", Signed: true,
		Files: []provisioning.ArtifactFile{{Path: "plain.md", Content: []byte(src)}},
	}
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	_, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderOpenCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	agentPath := filepath.Join(baseDir, ".opencode", "agent", "plain.md")
	data, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("agent not materialized at %s: %v", agentPath, err)
	}

	want := "---\ndescription: plain\nmode: subagent\n---\n" + src
	if string(data) != want {
		t.Errorf("unexpected translated content (fallback with no frontmatter):\n%q\nexpected:\n%q", data, want)
	}
}

func TestApply_OpenCode_MaterializzaAgent_DescriptionQuotataConDuePunti(t *testing.T) {
	baseDir := t.TempDir()
	// Description quoted in the source (necessary because it contains ": ", which
	// would otherwise break single-line "key: value" parsing) — must
	// survive the parse→serialize round trip without breaking the resulting YAML,
	// and the multi-line body must pass through verbatim.
	src := "---\nname: tricky\ndescription: \"Does stuff: analysis, then writes a report\"\n---\nLine one.\nLine two.\n"

	a := provisioning.Artifact{
		Kind: "agent", Name: "tricky", Source: "kb:x", ContentHash: "h1", Signed: true,
		Files: []provisioning.ArtifactFile{{Path: "tricky.md", Content: []byte(src)}},
	}
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	_, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderOpenCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	agentPath := filepath.Join(baseDir, ".opencode", "agent", "tricky.md")
	data, err := os.ReadFile(agentPath)
	if err != nil {
		t.Fatalf("agent not materialized at %s: %v", agentPath, err)
	}

	// The translated file must remain a valid frontmatter: SplitFrontmatter+
	// ParseFrontmatter must recover the original (unquoted) description and
	// mode: subagent, with no errors.
	fmRaw, body, has := okf.SplitFrontmatter(string(data))
	if !has {
		t.Fatalf("output has no valid frontmatter: %s", data)
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		t.Fatalf("translated frontmatter not parsable: %v\n%s", err, data)
	}
	gotDesc, _ := fm.Get("description")
	wantDesc := "Does stuff: analysis, then writes a report"
	if gotDesc != wantDesc {
		t.Errorf("description after round-trip: expected %q, got %q", wantDesc, gotDesc)
	}
	mode, _ := fm.Get("mode")
	if mode != "subagent" {
		t.Errorf("mode: expected subagent, got %q", mode)
	}
	if body != "Line one.\nLine two.\n" {
		t.Errorf("unexpected multi-line body: %q", body)
	}
}

func TestApply_MaterializzaHook(t *testing.T) {
	kbRoot := t.TempDir()
	hookDir := filepath.Join(kbRoot, "hooks", "notify")
	os.MkdirAll(hookDir, 0o755)
	os.WriteFile(filepath.Join(hookDir, "hook.json"), []byte(`{"event":"PostToolUse"}`), 0o644)
	os.WriteFile(filepath.Join(hookDir, "run.sh"), []byte("#!/bin/sh\n"), 0o755)

	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, true)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir := t.TempDir()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for _, rel := range []string{"hook.json", "run.sh"} {
		p := filepath.Join(baseDir, ".claude", "hooks", "notify", rel)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("Apply: %s not materialized: %v", rel, err)
		}
	}
	// autoTrust=true also generates the KB's "instructions" artifact (D56,
	// always present, independent of hooks/): filter on kind "hook".
	hookWritten := writtenOfKind(res.Written, "hook")
	if len(hookWritten) != 2 {
		t.Errorf("Apply: expected 2 files written (hook.json + run.sh), got %d: %+v (full Written: %+v)", len(hookWritten), hookWritten, res.Written)
	}
	for _, w := range hookWritten {
		if w.Kind != "hook" || w.Name != "notify" {
			t.Errorf("Apply: unexpected ManagedFile: %+v", w)
		}
	}
}

// writtenOfKind filters a []provisioning.ManagedFile by Kind — used by tests that
// verify the count of files written for a specific kind when the manifest
// also contains other kinds (e.g. the "instructions" artifact, always present for
// every KB, D56).
func writtenOfKind(written []provisioning.ManagedFile, kind string) []provisioning.ManagedFile {
	var out []provisioning.ManagedFile
	for _, w := range written {
		if w.Kind == kind {
			out = append(out, w)
		}
	}
	return out
}

func TestApply_Prune_AgentHook(t *testing.T) {
	baseDir := t.TempDir()

	// Obsolete agent (single file) + obsolete hook (dir).
	agentPath := filepath.Join(baseDir, ".claude", "agents", "old.md")
	os.MkdirAll(filepath.Dir(agentPath), 0o755)
	os.WriteFile(agentPath, []byte("old"), 0o644)

	hookPath := filepath.Join(baseDir, ".claude", "hooks", "old-hook", "hook.json")
	os.MkdirAll(filepath.Dir(hookPath), 0o755)
	os.WriteFile(hookPath, []byte("{}"), 0o644)

	m := provisioning.Manifest{Revision: "rev-new"}
	lock := provisioning.Lock{
		AppliedRevision: "rev-old",
		Provider:        "claude",
		Managed: []provisioning.ManagedFile{
			{Kind: "agent", Name: "old", Path: filepath.Join(".claude", "agents", "old.md"), ContentHash: "h1"},
			{Kind: "hook", Name: "old-hook", Path: filepath.Join(".claude", "hooks", "old-hook", "hook.json"), ContentHash: "h2"},
		},
	}

	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     lock,
	})
	if err != nil {
		t.Fatalf("Apply prune: %v", err)
	}
	if len(res.Pruned) != 2 {
		t.Errorf("Apply prune: expected 2 pruned files, got %d", len(res.Pruned))
	}
	if _, err := os.Stat(agentPath); !os.IsNotExist(err) {
		t.Error("Apply prune: obsolete agent not removed")
	}
	if _, err := os.Stat(hookPath); !os.IsNotExist(err) {
		t.Error("Apply prune: obsolete hook not removed")
	}
}

// --- Path traversal on agent/hook names (D40, extended to the new kinds) ---

func TestApply_RifiutaPathTraversal_AgentHook(t *testing.T) {
	baseDir := t.TempDir()

	cases := []provisioning.Artifact{
		{Kind: "agent", Name: "../evil", Source: "kb:x", ContentHash: "h1", Signed: true,
			Files: []provisioning.ArtifactFile{{Path: "evil.md", Content: []byte("x")}}},
		{Kind: "agent", Name: "/etc/evil", Source: "kb:x", ContentHash: "h2", Signed: true,
			Files: []provisioning.ArtifactFile{{Path: "evil.md", Content: []byte("x")}}},
		{Kind: "hook", Name: "../evil-hook", Source: "kb:x", ContentHash: "h3", Signed: true,
			Files: []provisioning.ArtifactFile{{Path: "hook.json", Content: []byte("x")}}},
	}
	for i, a := range cases {
		m := provisioning.MergeArtifacts([]provisioning.Artifact{a})
		_, err := provisioning.Apply(m, provisioning.ApplyOptions{
			Provider: configurator.ProviderClaudeCode,
			BaseDir:  baseDir,
			Lock:     provisioning.Lock{},
		})
		if err == nil {
			t.Errorf("case %d (kind=%s name=%q): Apply should have rejected the malicious name", i, a.Kind, a.Name)
		}
	}
	// No write outside baseDir.
	if _, err := os.Stat(filepath.Join(baseDir, "..", "evil.md")); err == nil {
		t.Fatal("file written outside baseDir")
	}
}

// --- ReadArtifactFiles for agent/hook ---

func TestReadArtifactFiles_Agent(t *testing.T) {
	kbRoot := t.TempDir()
	agentsDir := filepath.Join(kbRoot, "agents")
	os.MkdirAll(agentsDir, 0o755)
	os.WriteFile(filepath.Join(agentsDir, "reviewer.md"), []byte("Body.\n"), 0o644)

	a := provisioning.Artifact{Kind: "agent", Name: "reviewer", Source: "kb:homelab"}
	files, err := provisioning.ReadArtifactFiles(a, nil, map[string]string{"homelab": kbRoot})
	if err != nil {
		t.Fatalf("ReadArtifactFiles: %v", err)
	}
	if len(files) != 1 || files[0].Path != "reviewer.md" {
		t.Fatalf("ReadArtifactFiles: expected 1 reviewer.md file, got %+v", files)
	}
}

func TestReadArtifactFiles_Hook(t *testing.T) {
	kbRoot := t.TempDir()
	hookDir := filepath.Join(kbRoot, "hooks", "notify")
	os.MkdirAll(hookDir, 0o755)
	os.WriteFile(filepath.Join(hookDir, "hook.json"), []byte("{}"), 0o644)
	os.WriteFile(filepath.Join(hookDir, "run.sh"), []byte("echo"), 0o644)

	a := provisioning.Artifact{Kind: "hook", Name: "notify", Source: "kb:homelab"}
	files, err := provisioning.ReadArtifactFiles(a, nil, map[string]string{"homelab": kbRoot})
	if err != nil {
		t.Fatalf("ReadArtifactFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("ReadArtifactFiles: expected 2 files, got %+v", files)
	}
}

// --- KindCounts ---

func TestKindCounts(t *testing.T) {
	m := provisioning.Manifest{
		Revision: "rev1",
		Artifacts: []provisioning.Artifact{
			{Kind: "skill", Name: "s1", ContentHash: "h1", Signed: true},
			{Kind: "skill", Name: "s2", ContentHash: "h2", Signed: true},
			{Kind: "agent", Name: "a1", ContentHash: "h3", Signed: true},
			{Kind: "hook", Name: "hk1", ContentHash: "h4", Signed: true},
		},
	}
	lock := provisioning.Lock{
		Managed: []provisioning.ManagedFile{
			{Kind: "skill", Name: "s1", Path: "x", ContentHash: "h1"},         // installed, up to date
			{Kind: "skill", Name: "s2", Path: "y", ContentHash: "stale-hash"}, // installed but stale
			{Kind: "agent", Name: "a1", Path: "z", ContentHash: "h3"},         // installed
			// hook hk1 not installed yet.
		},
	}

	counts := provisioning.KindCounts(m, lock)
	if c := counts["skill"]; c.Total != 2 || c.Installed != 1 {
		t.Errorf("skill: expected Total=2 Installed=1, got %+v", c)
	}
	if c := counts["agent"]; c.Total != 1 || c.Installed != 1 {
		t.Errorf("agent: expected Total=1 Installed=1, got %+v", c)
	}
	if c := counts["hook"]; c.Total != 1 || c.Installed != 0 {
		t.Errorf("hook: expected Total=1 Installed=0, got %+v", c)
	}
}

// --- FilterForProvider + honest InSync ---

func TestFilterForProvider(t *testing.T) {
	m := provisioning.MergeArtifacts([]provisioning.Artifact{
		{Kind: "skill", Name: "s1", Source: "bundle", ContentHash: "h1", Signed: true},
		{Kind: "agent", Name: "a1", Source: "kb:x", ContentHash: "h2", Signed: true},
		{Kind: "hook", Name: "k1", Source: "kb:x", ContentHash: "h3", Signed: true},
	})

	claude := provisioning.FilterForProvider(m, configurator.ProviderClaudeCode)
	if len(claude.Artifacts) != 3 {
		t.Errorf("claude: expected 3 artifacts, got %d", len(claude.Artifacts))
	}
	// opencode materializes skill, agent and hook (D55/D59, D59 adds hook via a
	// generated plugin) — all three kinds have a known destination.
	oc := provisioning.FilterForProvider(m, configurator.ProviderOpenCode)
	if len(oc.Artifacts) != 3 {
		t.Errorf("opencode: expected 3 artifacts (skill+agent+hook), got %+v", oc.Artifacts)
	}
	for _, a := range oc.Artifacts {
		if a.Kind != "skill" && a.Kind != "agent" && a.Kind != "hook" {
			t.Errorf("opencode: unexpected kind %+v", a)
		}
	}
	if oc.Revision != m.Revision {
		t.Errorf("the revision must not change with the filter")
	}
}

func TestComputeDiff_InSyncRequiresNoChanges(t *testing.T) {
	m := provisioning.MergeArtifacts([]provisioning.Artifact{
		{Kind: "skill", Name: "s1", Source: "kb:x", ContentHash: "h1", Signed: false},
	})
	// Lock with the same applied revision but no managed files (real case:
	// Apply with an unsigned artifact → NeedsApproval, revision still marked).
	lock := provisioning.Lock{AppliedRevision: m.Revision}
	d := provisioning.ComputeDiff(m, lock)
	if d.InSync {
		t.Errorf("InSync=true with %d Added: the revision alone is not enough", len(d.Added))
	}
	if len(d.Added) != 1 {
		t.Errorf("expected 1 Added, got %d", len(d.Added))
	}
}
