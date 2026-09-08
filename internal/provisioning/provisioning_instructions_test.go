package provisioning_test

// Tests for the "instructions" kind (D56): the "imprinting" artifact generated for
// each KB (BuildManifest) and its materialization as a managed block, as a
// group, in the user's global instructions files (Apply). See also
// provisioning_test.go (kind "skill") and provisioning_agent_hook_test.go (kind
// "agent"/"hook").

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// makeKBWithArchives creates a minimal KB (just data/<archive>/<pages>.md, no
// data/index.md — BuildManifest doesn't require a KB "opened" via kb.Open) with
// the given archives and pages.
func makeKBWithArchives(t *testing.T, archives map[string][]string) string {
	t.Helper()
	root := t.TempDir()
	for archive, pages := range archives {
		dir := filepath.Join(root, "data", archive)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, page := range pages {
			if err := os.WriteFile(filepath.Join(dir, page), []byte("# "+page+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return root
}

// findInstructionsArtifact looks for the kind=="instructions" artifact with the
// given Name in m, failing the test if absent.
func findInstructionsArtifact(t *testing.T, m provisioning.Manifest, kbName string) provisioning.Artifact {
	t.Helper()
	for _, a := range m.Artifacts {
		if a.Kind == "instructions" && a.Name == kbName {
			return a
		}
	}
	t.Fatalf("no instructions artifact for KB %q: %+v", kbName, m.Artifacts)
	return provisioning.Artifact{}
}

// instructionsArtifact manually builds an already-signed Artifact of kind
// "instructions", with content and ContentHash (direct sha256, only to
// differentiate the artifacts in tests — Apply doesn't recompute the hash, it
// trusts the one provided, like every other kind).
func instructionsArtifact(name, content string) provisioning.Artifact {
	h := sha256.Sum256([]byte(content))
	return provisioning.Artifact{
		Kind:        "instructions",
		Name:        name,
		Source:      "kb:" + name,
		ContentHash: fmt.Sprintf("%x", h),
		Signed:      true,
		Files:       []provisioning.ArtifactFile{{Path: "instructions.md", Content: []byte(content)}},
	}
}

// --- Generation (BuildManifest) ---

func TestBuildManifest_Instructions_Determinismo(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{
		"entities": {"router.md", "switch.md"},
		"topics":   {"networking.md"},
	})

	m1, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest 1: %v", err)
	}
	m2, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest 2: %v", err)
	}

	a1 := findInstructionsArtifact(t, m1, "homelab")
	a2 := findInstructionsArtifact(t, m2, "homelab")
	if a1.ContentHash == "" {
		t.Fatal("empty instructions ContentHash")
	}
	if a1.ContentHash != a2.ContentHash {
		t.Error("instructions ContentHash not deterministic for the same KB structure")
	}
	if len(a1.Files) != 1 || a1.Files[0].Path != "instructions.md" {
		t.Fatalf("unexpected instructions Files: %+v", a1.Files)
	}
	if a1.Source != "kb:homelab" {
		t.Errorf("expected Source kb:homelab, got %q", a1.Source)
	}

	// A new page in an existing archive does NOT change the content (no
	// counts, D65): the hash stays stable and the block doesn't need re-syncing.
	os.WriteFile(filepath.Join(kbRoot, "data", "entities", "new.md"), []byte("# new\n"), 0o644)
	m3, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest 3: %v", err)
	}
	a3 := findInstructionsArtifact(t, m3, "homelab")
	if a3.ContentHash != a1.ContentHash {
		t.Error("instructions ContentHash: a new page in an existing archive must not change the hash (D65)")
	}

	// A new archive, on the other hand, changes the listed structure: the hash must change.
	os.MkdirAll(filepath.Join(kbRoot, "data", "incidents"), 0o755)
	os.WriteFile(filepath.Join(kbRoot, "data", "incidents", "i1.md"), []byte("# i1\n"), 0o644)
	m4, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest 4: %v", err)
	}
	a4 := findInstructionsArtifact(t, m4, "homelab")
	if a4.ContentHash == a1.ContentHash {
		t.Error("instructions ContentHash: a new archive must change the hash")
	}
}

func TestBuildManifest_Instructions_ArchiviElencatiEdEsclusioneInfra(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{
		"entities": {"a.md", "b.md"},
		"topics":   {"c.md"},
		// defensive: if for some reason data/ contained a subdirectory
		// named like an infrastructure directory, it must not show up as an
		// archive (see kbArchives).
		"skills": {"bogus.md"},
	})
	// Real infrastructure directories, siblings of data/ — the ones from kb.Init.
	for _, d := range []string{"skills", "agents", "hooks"} {
		os.MkdirAll(filepath.Join(kbRoot, d), 0o755)
	}

	m, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	a := findInstructionsArtifact(t, m, "homelab")
	content := string(a.Files[0].Content)

	if !strings.Contains(content, "entities/") {
		t.Errorf("instructions content doesn't list the entities archive:\n%s", content)
	}
	if !strings.Contains(content, "topics/") {
		t.Errorf("instructions content doesn't list the topics archive:\n%s", content)
	}
	if strings.Contains(content, "(2 pagine)") || strings.Contains(content, "(1 pagine)") {
		t.Errorf("instructions content must not report page counts (D65):\n%s", content)
	}
	if strings.Contains(content, "skills/") {
		t.Errorf("instructions content must not list data/skills as an archive (infrastructure directory):\n%s", content)
	}
	if !strings.Contains(content, "homelab") {
		t.Errorf("instructions content doesn't mention the KB name:\n%s", content)
	}
	for _, tool := range []string{"search", "atlas_overview", "concept_read", "concept_write", "log_append"} {
		if !strings.Contains(content, tool) {
			t.Errorf("instructions content doesn't mention tool %q:\n%s", tool, content)
		}
	}
}

func TestBuildManifest_Instructions_NessunArchivio(t *testing.T) {
	kbRoot := t.TempDir()
	os.MkdirAll(filepath.Join(kbRoot, "data"), 0o755)

	m, err := provisioning.BuildManifest(nil, map[string]string{"vuota": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	a := findInstructionsArtifact(t, m, "vuota")
	if !strings.Contains(string(a.Files[0].Content), "No archives") {
		t.Errorf("expected an explicit message for a KB with no archives: %s", a.Files[0].Content)
	}
}

// --- Auto-generated agent section and curated instructions.md (D61) ---

func TestBuildManifest_Instructions_SezioneAgent(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	agentsDir := filepath.Join(kbRoot, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write order reversed compared to the expected alphabetical order, to
	// verify that the section is sorted by name regardless.
	writeFile(t, filepath.Join(agentsDir, "zorro.md"),
		"---\nname: zorro\ndescription: Defends the KB from intruders\n---\nzorro prompt.\n")
	writeFile(t, filepath.Join(agentsDir, "anonimo.md"), "No frontmatter here.\n")

	m, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	content := string(findInstructionsArtifact(t, m, "homelab").Files[0].Content)

	// Since D154 the server-generated block carries NO subagent sentence: it
	// cannot know which provider will receive the block, and a provider whose
	// "agent" cell is unsupported receives none of them. The sentence is emitted
	// per provider by Apply, from what that client can actually install — see
	// TestApply_InstructionsSubagentSentence*.
	if strings.Contains(content, "Subagents installed") {
		t.Errorf("the server-generated block must not claim installed subagents (D154):\n%s", content)
	}
	if strings.Contains(content, "Defends the KB from intruders") {
		t.Errorf("agent descriptions must not be duplicated in the instructions (D65):\n%s", content)
	}
}

func TestBuildManifest_Instructions_NessunaSezioneSenzaAgentNeCurato(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})

	m, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	content := string(findInstructionsArtifact(t, m, "homelab").Files[0].Content)

	if strings.Contains(content, "Subagents installed") {
		t.Errorf("no agent in the KB: the agent section must not appear:\n%s", content)
	}
	// Compact form post-D65: header with inline archives + three lines of
	// operational instructions, no other section.
	want := "The \"homelab\" KB is served via MCP by the \"cartographer\" server. Archives: entities/.\n\n" +
		"Operational instructions:\n" +
		"- consult it autonomously when you need historical or architectural context: `search` (keyword) or `atlas_overview` to orient yourself, `concept_read` to read;\n" +
		"- write or update a page with `concept_write` when you discover something relevant; close relevant sessions with `log_append`;\n" +
		"- every write is a git commit, revertible.\n"
	if content != want {
		t.Errorf("output changed with no agent/instructions.md:\ngot:\n%s\nwant:\n%s", content, want)
	}
}

func TestBuildManifest_Instructions_CuratoSenzaFrontmatter(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	writeFile(t, filepath.Join(kbRoot, "instructions.md"), "Rule: bulk reads -> explorer.\n")

	m, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	content := string(findInstructionsArtifact(t, m, "homelab").Files[0].Content)

	if !strings.Contains(content, "Rule: bulk reads -> explorer.") {
		t.Errorf("instructions.md body not included:\n%s", content)
	}
	autoIdx := strings.Index(content, "every write is a git commit")
	curatedIdx := strings.Index(content, "Rule: bulk reads")
	if autoIdx == -1 || curatedIdx == -1 || curatedIdx < autoIdx {
		t.Errorf("the curated part must come after the auto-generated part:\n%s", content)
	}
}

func TestBuildManifest_Instructions_CuratoConFrontmatter(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	writeFile(t, filepath.Join(kbRoot, "instructions.md"),
		"---\ntitle: orchestration\n---\nDelegate mechanical work to OpenCode.\n")

	m, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	content := string(findInstructionsArtifact(t, m, "homelab").Files[0].Content)

	if strings.Contains(content, "title: orchestration") {
		t.Errorf("instructions.md's frontmatter must not appear in the generated content:\n%s", content)
	}
	if !strings.Contains(content, "Delegate mechanical work to OpenCode.") {
		t.Errorf("instructions.md body (after frontmatter) not included:\n%s", content)
	}
}

func TestBuildManifest_Instructions_HashStabileConDescriptionAgent(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	agentsDir := filepath.Join(kbRoot, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	agentPath := filepath.Join(agentsDir, "reviewer.md")
	writeFile(t, agentPath, "---\ndescription: First version\n---\nPrompt.\n")

	m1, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest 1: %v", err)
	}
	h1 := findInstructionsArtifact(t, m1, "homelab").ContentHash

	// The instructions list only names (D65): changing the description doesn't
	// change the block. (The updated description still travels with the
	// "agent" kind artifact, which has its own ContentHash.)
	writeFile(t, agentPath, "---\ndescription: Second version\n---\nPrompt.\n")
	m2, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest 2: %v", err)
	}
	h2 := findInstructionsArtifact(t, m2, "homelab").ContentHash

	if h1 != h2 {
		t.Error("the instructions ContentHash must not change when only an agent's description changes (D65)")
	}

	// An extra agent, on the other hand, changes the list of names: the hash must change.
	writeFile(t, filepath.Join(agentsDir, "nuovo.md"), "---\ndescription: X\n---\nPrompt.\n")
	m3, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest 3: %v", err)
	}
	h3 := findInstructionsArtifact(t, m3, "homelab").ContentHash
	// Since D154 the block no longer names agents, so its hash is independent of
	// the agent set — deliberately: the sentence naming installed subagents is
	// per-provider and emitted client-side. What guarantees the block is rewritten
	// when the agent set changes is Apply's trigger, which now fires on an "agent"
	// artifact too (see TestApply_InstructionsRewrittenWhenAgentSetChanges).
	if h3 != h1 {
		t.Error("the instructions ContentHash must not depend on the agent set (D154)")
	}
}

func TestBuildManifest_Instructions_HashCambiaConCurato(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	instrPath := filepath.Join(kbRoot, "instructions.md")
	writeFile(t, instrPath, "Version one.\n")

	m1, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest 1: %v", err)
	}
	h1 := findInstructionsArtifact(t, m1, "homelab").ContentHash

	writeFile(t, instrPath, "Version two.\n")
	m2, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest 2: %v", err)
	}
	h2 := findInstructionsArtifact(t, m2, "homelab").ContentHash

	if h1 == h2 {
		t.Error("the instructions ContentHash must change when instructions.md's content changes")
	}
}

// writeFile writes content to the given path, creating the parent directories if
// needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// --- Managed block: creation, append, rewrite, removal, idempotency ---

func TestApply_Instructions_CreaFileNuovo(t *testing.T) {
	baseDir := t.TempDir()
	a := instructionsArtifact("homelab", "Test content for homelab.\n")
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	path := filepath.Join(baseDir, ".claude", "CLAUDE.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("CLAUDE.md not created: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "cartographer:instructions:begin") || !strings.Contains(content, "cartographer:instructions:end") {
		t.Errorf("block markers missing: %s", content)
	}
	if !strings.Contains(content, "Test content for homelab.") {
		t.Errorf("instructions content missing from the block: %s", content)
	}
	if len(res.Written) != 1 || res.Written[0].Kind != "instructions" || res.Written[0].Path != filepath.Join(".claude", "CLAUDE.md") {
		t.Errorf("unexpected Written: %+v", res.Written)
	}
	if len(res.NewLock.Managed) != 1 || res.NewLock.Managed[0].Kind != "instructions" {
		t.Errorf("unexpected Lock.Managed: %+v", res.NewLock.Managed)
	}
}

func TestApply_Instructions_TuttiIQuattroProvider(t *testing.T) {
	cases := []struct {
		provider configurator.Provider
		wantPath string
	}{
		{configurator.ProviderClaudeCode, filepath.Join(".claude", "CLAUDE.md")},
		{configurator.ProviderOpenCode, filepath.Join(".config", "opencode", "AGENTS.md")},
		{configurator.ProviderCodex, filepath.Join(".codex", "AGENTS.md")},
		{configurator.ProviderKiro, filepath.Join(".kiro", "steering", "cartographer.md")},
	}
	for _, c := range cases {
		baseDir := t.TempDir()
		a := instructionsArtifact("homelab", "Content for "+string(c.provider)+".\n")
		m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

		res, err := provisioning.Apply(m, provisioning.ApplyOptions{
			Provider: c.provider,
			BaseDir:  baseDir,
			Lock:     provisioning.Lock{},
		})
		if err != nil {
			t.Fatalf("%s: Apply: %v", c.provider, err)
		}
		if len(res.Written) != 1 || res.Written[0].Path != c.wantPath {
			t.Errorf("%s: expected Written path %q, got %+v", c.provider, c.wantPath, res.Written)
		}
		if _, err := os.Stat(filepath.Join(baseDir, c.wantPath)); err != nil {
			t.Errorf("%s: file not found at %s: %v", c.provider, c.wantPath, err)
		}
	}
}

func TestApply_Instructions_AppendAFileUtenteEsistente(t *testing.T) {
	baseDir := t.TempDir()
	claudeDir := filepath.Join(baseDir, ".claude")
	os.MkdirAll(claudeDir, 0o755)
	userContent := "# My instructions\n\nThese are notes hand-written by the user.\n"
	claudePath := filepath.Join(claudeDir, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	a := instructionsArtifact("homelab", "Generated section.\n")
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	_, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	// The pre-existing user content must remain, byte-for-byte, the file's
	// prefix: the append doesn't touch it, it only adds the block after.
	if !strings.HasPrefix(content, userContent) {
		t.Errorf("the original user content is no longer the file's byte-for-byte prefix:\nexpected prefix:\n%q\ngot:\n%q", userContent, content)
	}
	if !strings.Contains(content, "Generated section.") {
		t.Errorf("generated block not appended: %s", content)
	}
}

func TestApply_Instructions_RiscritturaTraMarkerEsistenti(t *testing.T) {
	baseDir := t.TempDir()
	claudeDir := filepath.Join(baseDir, ".claude")
	os.MkdirAll(claudeDir, 0o755)
	userContent := "# User notes\n"
	claudePath := filepath.Join(claudeDir, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	a1 := instructionsArtifact("homelab", "Version one.\n")
	m1 := provisioning.MergeArtifacts([]provisioning.Artifact{a1})
	res1, err := provisioning.Apply(m1, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply 1: %v", err)
	}

	a2 := instructionsArtifact("homelab", "Version two, updated.\n")
	m2 := provisioning.MergeArtifacts([]provisioning.Artifact{a2})
	_, err = provisioning.Apply(m2, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: res1.NewLock,
	})
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}

	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.HasPrefix(content, userContent) {
		t.Errorf("user content outside the markers must not change: %q", content)
	}
	if strings.Contains(content, "Version one.") {
		t.Errorf("the old block should have been replaced, not concatenated: %s", content)
	}
	if !strings.Contains(content, "Version two, updated.") {
		t.Errorf("the new block is not present: %s", content)
	}
	if n := strings.Count(content, "cartographer:instructions:begin"); n != 1 {
		t.Errorf("expected 1 occurrence of the begin marker (single block rewritten), found %d", n)
	}
}

func TestApply_Instructions_Idempotente(t *testing.T) {
	baseDir := t.TempDir()
	a := instructionsArtifact("homelab", "Stable content.\n")
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	res1, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply 1: %v", err)
	}
	path := filepath.Join(baseDir, ".claude", "CLAUDE.md")
	data1, _ := os.ReadFile(path)

	res2, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: res1.NewLock,
	})
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	data2, _ := os.ReadFile(path)

	if string(data1) != string(data2) {
		t.Errorf("applying twice on the same revision changed the file:\nbefore: %q\nafter: %q", data1, data2)
	}
	if len(res2.Written) != 0 {
		t.Errorf("second Apply (in-sync): expected no Written, got %+v", res2.Written)
	}
}

func TestApply_Instructions_NonFirmato_NeedsApproval(t *testing.T) {
	baseDir := t.TempDir()
	a := instructionsArtifact("homelab", "Content.\n")
	a.Signed = false
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a})

	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.NeedsApproval) != 1 || res.NeedsApproval[0].Kind != "instructions" {
		t.Errorf("expected 1 instructions artifact in NeedsApproval, got %+v", res.NeedsApproval)
	}
	if len(res.Written) != 0 {
		t.Errorf("Written must stay empty for an unsigned artifact: %+v", res.Written)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".claude", "CLAUDE.md")); !os.IsNotExist(err) {
		t.Errorf("no file must be written for an unsigned instructions artifact")
	}
}

// --- Block removal: the user file survives, the block-only file doesn't ---

func TestApply_Instructions_RimozioneBlocco_UtenteSopravvive(t *testing.T) {
	baseDir := t.TempDir()
	claudeDir := filepath.Join(baseDir, ".claude")
	os.MkdirAll(claudeDir, 0o755)
	userContent := "# Permanent user notes\n"
	claudePath := filepath.Join(claudeDir, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	a := instructionsArtifact("homelab", "Section to remove.\n")
	m1 := provisioning.MergeArtifacts([]provisioning.Artifact{a})
	res1, err := provisioning.Apply(m1, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply 1: %v", err)
	}

	// The KB disappears from the manifest (e.g. the server disconnects it).
	m2 := provisioning.Manifest{Revision: "empty"}
	res2, err := provisioning.Apply(m2, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: res1.NewLock,
	})
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}

	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("the user file must not be deleted: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "Section to remove") {
		t.Errorf("the block should have been removed: %s", content)
	}
	// The user content survives: the separator newline normalization
	// (§appendBlock) doesn't guarantee a byte-exact roundtrip on removal, only
	// that the user's text remains present — whitespace-insensitive comparison.
	if strings.TrimSpace(content) != strings.TrimSpace(userContent) {
		t.Errorf("the user content must stay intact after the block is removed: expected %q, got %q", userContent, content)
	}
	found := false
	for _, mf := range res2.Pruned {
		if mf.Kind == "instructions" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an instructions ManagedFile in Pruned: %+v", res2.Pruned)
	}
	if len(res2.NewLock.Managed) != 0 {
		t.Errorf("Lock.Managed must be empty after removing the only KB: %+v", res2.NewLock.Managed)
	}
}

func TestApply_Instructions_RimozioneBlocco_FileSoloBlocco_Rimosso(t *testing.T) {
	baseDir := t.TempDir() // no pre-existing CLAUDE.md: Apply creates it from scratch, block only.
	a := instructionsArtifact("homelab", "Content.\n")
	m1 := provisioning.MergeArtifacts([]provisioning.Artifact{a})
	res1, err := provisioning.Apply(m1, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply 1: %v", err)
	}

	path := filepath.Join(baseDir, ".claude", "CLAUDE.md")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("CLAUDE.md not created: %v", err)
	}

	m2 := provisioning.Manifest{Revision: "empty"}
	if _, err := provisioning.Apply(m2, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: res1.NewLock,
	}); err != nil {
		t.Fatalf("Apply 2: %v", err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the block-only file should have been removed entirely, got err=%v", err)
	}
}

// --- Group: several KBs in a single block, sorted by Name ---

func TestApply_Instructions_GruppoDueKB(t *testing.T) {
	baseDir := t.TempDir()
	aZ := instructionsArtifact("zeta", "Zeta section.\n")
	aA := instructionsArtifact("alfa", "Alfa section.\n")
	m := provisioning.MergeArtifacts([]provisioning.Artifact{aZ, aA})

	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	path := filepath.Join(baseDir, ".claude", "CLAUDE.md")
	data, _ := os.ReadFile(path)
	content := string(data)

	idxAlfa := strings.Index(content, "Alfa section.")
	idxZeta := strings.Index(content, "Zeta section.")
	if idxAlfa == -1 || idxZeta == -1 {
		t.Fatalf("missing sections: %s", content)
	}
	if idxAlfa > idxZeta {
		t.Errorf("sections must be sorted by Name (alfa before zeta): %s", content)
	}
	if strings.Count(content, "cartographer:instructions:begin") != 1 {
		t.Errorf("expected a single shared block for both KBs: %s", content)
	}
	if len(res.NewLock.Managed) != 2 {
		t.Errorf("expected 1 ManagedFile per instructions artifact (2 KBs), got %d: %+v", len(res.NewLock.Managed), res.NewLock.Managed)
	}
}

func TestApply_Instructions_GruppoRimozioneUnaKB(t *testing.T) {
	baseDir := t.TempDir()
	aZ := instructionsArtifact("zeta", "Zeta section.\n")
	aA := instructionsArtifact("alfa", "Alfa section.\n")
	m1 := provisioning.MergeArtifacts([]provisioning.Artifact{aZ, aA})
	res1, err := provisioning.Apply(m1, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply 1: %v", err)
	}

	// "zeta" disappears from the manifest (e.g. one of the two KBs disconnects).
	m2 := provisioning.MergeArtifacts([]provisioning.Artifact{aA})
	res2, err := provisioning.Apply(m2, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: res1.NewLock,
	})
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}

	path := filepath.Join(baseDir, ".claude", "CLAUDE.md")
	data, _ := os.ReadFile(path)
	content := string(data)
	if strings.Contains(content, "Zeta section.") {
		t.Errorf("the removed KB's section must no longer appear in the rebuilt block: %s", content)
	}
	if !strings.Contains(content, "Alfa section.") {
		t.Errorf("the remaining KB's section must continue to appear: %s", content)
	}
	if len(res2.NewLock.Managed) != 1 || res2.NewLock.Managed[0].Name != "alfa" {
		t.Errorf("expected 1 remaining ManagedFile (alfa), got %+v", res2.NewLock.Managed)
	}
}

// --- KindCounts with instructions ---

func TestKindCounts_Instructions(t *testing.T) {
	m := provisioning.Manifest{
		Revision: "r1",
		Artifacts: []provisioning.Artifact{
			{Kind: "instructions", Name: "alfa", ContentHash: "h1", Signed: true},
			{Kind: "instructions", Name: "zeta", ContentHash: "h2", Signed: true},
		},
	}
	lock := provisioning.Lock{
		Managed: []provisioning.ManagedFile{
			{Kind: "instructions", Name: "alfa", Path: filepath.Join(".claude", "CLAUDE.md"), ContentHash: "h1"},
		},
	}
	counts := provisioning.KindCounts(m, lock)
	if c := counts["instructions"]; c.Total != 2 || c.Installed != 1 {
		t.Errorf("instructions: expected Total=2 Installed=1, got %+v", c)
	}
}

// --- Prune via `cartographer disconnect` (direct PruneManaged, several entries same Path) ---

func TestPruneManaged_Instructions_DuplicatiStessoPath(t *testing.T) {
	baseDir := t.TempDir()
	claudeDir := filepath.Join(baseDir, ".claude")
	os.MkdirAll(claudeDir, 0o755)
	userContent := "# User notes\n"
	claudePath := filepath.Join(claudeDir, "CLAUDE.md")
	if err := os.WriteFile(claudePath, []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	a1 := instructionsArtifact("alfa", "Alfa section.\n")
	a2 := instructionsArtifact("zeta", "Zeta section.\n")
	m := provisioning.MergeArtifacts([]provisioning.Artifact{a1, a2})
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: provisioning.Lock{},
	})
	if err != nil {
		t.Fatalf("Apply (setup): %v", err)
	}
	if len(res.NewLock.Managed) != 2 {
		t.Fatalf("setup: expected 2 managed instructions (same Path), got %+v", res.NewLock.Managed)
	}

	// `cartographer disconnect`: prune of the provider's entire managed set — two
	// "instructions" ManagedFile entries pointing at the same physical Path.
	pruned, err := provisioning.PruneManaged(res.NewLock.Managed, baseDir, false)
	if err != nil {
		t.Fatalf("PruneManaged: %v", err)
	}
	if len(pruned) != 2 {
		t.Errorf("expected 2 pruned ManagedFile entries (one per artifact, even with a single physical file), got %d", len(pruned))
	}

	data, err := os.ReadFile(claudePath)
	if err != nil {
		t.Fatalf("the user file must not be deleted by the prune: %v", err)
	}
	content := string(data)
	if strings.Contains(content, "Alfa section.") || strings.Contains(content, "Zeta section.") {
		t.Errorf("the block should have been removed (once, idempotently): %s", content)
	}
	if strings.TrimSpace(content) != strings.TrimSpace(userContent) {
		t.Errorf("unexpected user content after the prune: %q", content)
	}
}

// instructionsContent returns the generated block for kbName in m.
func instructionsContent(t *testing.T, m provisioning.Manifest, kbName string) string {
	t.Helper()
	a := findInstructionsArtifact(t, m, kbName)
	if len(a.Files) != 1 {
		t.Fatalf("unexpected instructions Files: %+v", a.Files)
	}
	return string(a.Files[0].Content)
}

// D144: with a tool prefix configured the managed block must name the tools the
// agent can actually call, and without one it must stay byte-identical.
func TestBuildManifest_Instructions_ToolPrefix(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"router.md"}})

	plain, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest plain: %v", err)
	}
	plainContent := instructionsContent(t, plain, "homelab")

	// An empty map, and a map without this KB's key, are both "unprefixed".
	for name, opts := range map[string]provisioning.BuildOptions{
		"empty map":   {ToolPrefixes: map[string]string{}},
		"other KB":    {ToolPrefixes: map[string]string{"other": "other"}},
		"empty value": {ToolPrefixes: map[string]string{"homelab": ""}},
	} {
		m, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, opts)
		if err != nil {
			t.Fatalf("BuildManifest %s: %v", name, err)
		}
		if got := instructionsContent(t, m, "homelab"); got != plainContent {
			t.Errorf("%s: instructions changed for an unprefixed KB:\n%s", name, got)
		}
	}

	// Golden: the operational lines of an unprefixed block, verbatim.
	for _, want := range []string{
		"- consult it autonomously when you need historical or architectural context: `search` (keyword) or `atlas_overview` to orient yourself, `concept_read` to read;\n",
		"- write or update a page with `concept_write` when you discover something relevant; close relevant sessions with `log_append`;\n",
	} {
		if !strings.Contains(plainContent, want) {
			t.Errorf("unprefixed block missing %q:\n%s", want, plainContent)
		}
	}

	prefixed, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot},
		provisioning.BuildOptions{ToolPrefixes: map[string]string{"homelab": "homelab"}})
	if err != nil {
		t.Fatalf("BuildManifest prefixed: %v", err)
	}
	prefixedContent := instructionsContent(t, prefixed, "homelab")
	if prefixedContent == plainContent {
		t.Fatal("instructions identical with and without a tool prefix")
	}
	if findInstructionsArtifact(t, prefixed, "homelab").ContentHash == findInstructionsArtifact(t, plain, "homelab").ContentHash {
		t.Error("ContentHash identical with and without a tool prefix: the block would not re-materialize")
	}
	for _, base := range []string{"search", "atlas_overview", "concept_read", "concept_write", "log_append"} {
		if !strings.Contains(prefixedContent, "`homelab__"+base+"`") {
			t.Errorf("prefixed block does not name `homelab__%s`:\n%s", base, prefixedContent)
		}
		if strings.Contains(prefixedContent, "`"+base+"`") {
			t.Errorf("prefixed block still names the bare tool `%s`:\n%s", base, prefixedContent)
		}
	}
}

// --- D154: the block describes what THIS client received ---

func agentManifest(t *testing.T, kbRoot string, names ...string) provisioning.Manifest {
	t.Helper()
	agentsDir := filepath.Join(kbRoot, "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		writeFile(t, filepath.Join(agentsDir, n+".md"), "---\nname: "+n+"\ndescription: Agent "+n+".\n---\nPrompt.\n")
	}
	m, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	for i := range m.Artifacts {
		m.Artifacts[i].Signed = true
	}
	return m
}

func instructionsBody(t *testing.T, base string, provider configurator.Provider) string {
	t.Helper()
	rel := map[configurator.Provider]string{
		configurator.ProviderClaudeCode: filepath.Join(".claude", "CLAUDE.md"),
		configurator.ProviderKiro:       filepath.Join(".kiro", "steering", "cartographer.md"),
	}[provider]
	data, err := os.ReadFile(filepath.Join(base, rel))
	if err != nil {
		t.Fatalf("read instructions for %s: %v", provider, err)
	}
	return string(data)
}

// An agent reading its own steering was told to delegate to subagents that did
// not exist on that client: kiro's "agent" cell is unsupportedDest, so it
// receives none of them.
func TestApply_InstructionsSubagentSentenceReflectsThisClient(t *testing.T) {
	for _, tc := range []struct {
		provider  configurator.Provider
		wantNames bool
	}{
		{configurator.ProviderClaudeCode, true},
		{configurator.ProviderKiro, false},
	} {
		t.Run(string(tc.provider), func(t *testing.T) {
			kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
			m := agentManifest(t, kbRoot, "zorro", "anonimo")
			base := t.TempDir()
			if _, err := provisioning.Apply(m, provisioning.ApplyOptions{Provider: tc.provider, BaseDir: base, Lock: provisioning.Lock{}, KBRoots: map[string]string{"homelab": kbRoot}}); err != nil {
				t.Fatal(err)
			}
			body := instructionsBody(t, base, tc.provider)
			has := strings.Contains(body, "Subagents installed")
			if has != tc.wantNames {
				t.Errorf("%s: subagent sentence present = %v, want %v\n%s", tc.provider, has, tc.wantNames, body)
			}
			if tc.wantNames && !strings.Contains(body, "anonimo, zorro") {
				t.Errorf("%s: sentence does not name the installed agents in order:\n%s", tc.provider, body)
			}
		})
	}
}

// The per-artifact "unsupported" line only appears on a run where the artifact
// enters the diff; afterwards the condition is invisible while the KB keeps
// declaring artifacts that are silently not installed.
func TestApply_WarnsEveryRunAboutUnsupportedKinds(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	m := agentManifest(t, kbRoot, "zorro")
	base := t.TempDir()

	first, err := provisioning.Apply(m, provisioning.ApplyOptions{Provider: configurator.ProviderKiro, BaseDir: base, Lock: provisioning.Lock{}, KBRoots: map[string]string{"homelab": kbRoot}})
	if err != nil {
		t.Fatal(err)
	}
	warned := func(r provisioning.AppliedResult) bool {
		for _, w := range r.Warnings {
			if strings.Contains(w, "cannot receive") && strings.Contains(w, "agent") {
				return true
			}
		}
		return false
	}
	if !warned(first) {
		t.Fatalf("first run did not warn: %v", first.Warnings)
	}
	// Second run: nothing in the diff, and the warning must still be there.
	second, err := provisioning.Apply(m, provisioning.ApplyOptions{Provider: configurator.ProviderKiro, BaseDir: base, Lock: first.NewLock, KBRoots: map[string]string{"homelab": kbRoot}})
	if err != nil {
		t.Fatal(err)
	}
	if !warned(second) {
		t.Errorf("the condition became invisible on the second run: %v", second.Warnings)
	}
}

// The block's content now depends on the agent set even though its hash does
// not, so the trigger has to reflect that dependency or the sentence goes stale.
func TestApply_InstructionsRewrittenWhenAgentSetChanges(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	m1 := agentManifest(t, kbRoot, "zorro")
	base := t.TempDir()
	first, err := provisioning.Apply(m1, provisioning.ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: base, Lock: provisioning.Lock{}, KBRoots: map[string]string{"homelab": kbRoot}})
	if err != nil {
		t.Fatal(err)
	}
	if body := instructionsBody(t, base, configurator.ProviderClaudeCode); strings.Contains(body, "nuovo") {
		t.Fatalf("unexpected agent in the first block:\n%s", body)
	}

	m2 := agentManifest(t, kbRoot, "zorro", "nuovo")
	if _, err := provisioning.Apply(m2, provisioning.ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: base, Lock: first.NewLock, KBRoots: map[string]string{"homelab": kbRoot}}); err != nil {
		t.Fatal(err)
	}
	body := instructionsBody(t, base, configurator.ProviderClaudeCode)
	if !strings.Contains(body, "nuovo") {
		t.Errorf("adding an agent did not rewrite the block, so the sentence is stale:\n%s", body)
	}
}

// A KB whose instructions.md is written in the team's working language yielded a
// steering file that switched language twice, with the generated English first —
// the worst position, since it sets the expected output language.
func TestGenerateKBInstructions_PreambleNoneDirective(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	writeFile(t, filepath.Join(kbRoot, "instructions.md"),
		"<!-- cartographer: preamble: none -->\nRegola: leggi prima l'indice.\n")

	m, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	content := string(findInstructionsArtifact(t, m, "homelab").Files[0].Content)

	if strings.Contains(content, "Operational instructions:") {
		t.Errorf("preamble: none did not suppress the generated bullets:\n%s", content)
	}
	if strings.Contains(content, "cartographer: preamble: none") {
		t.Errorf("the directive itself leaked into the block:\n%s", content)
	}
	// The routing sentence is generated state, not prose: it always stays.
	if !strings.Contains(content, `The "homelab" KB is served via MCP`) {
		t.Errorf("the routing sentence must stay:\n%s", content)
	}
	if !strings.Contains(content, "Regola: leggi prima l'indice.") {
		t.Errorf("the curated content is missing:\n%s", content)
	}
}

// A KB must be able to document the directive without triggering it.
func TestGenerateKBInstructions_DirectiveNotOnFirstLineIsContent(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	writeFile(t, filepath.Join(kbRoot, "instructions.md"),
		"How to opt out of the preamble:\n<!-- cartographer: preamble: none -->\n")

	m, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	content := string(findInstructionsArtifact(t, m, "homelab").Files[0].Content)
	if !strings.Contains(content, "Operational instructions:") {
		t.Errorf("a directive that is not on the first line must be content:\n%s", content)
	}
}

// --- D182: per-KB attribution and binding order ---
//
// Every KB's instructions.md used to be concatenated into one managed block
// with nothing marking where one KB's voice ends and the next begins. These
// tests cover the fix: each KB's snippet wrapped in named delimiters, a scope
// sentence attributing its curated directives, and section order following an
// explicit binding instead of always the alphabet.

// buildSignedManifest builds a manifest from kbRoots and marks every artifact
// Signed, the same shortcut agentManifest already uses so Apply doesn't need
// a real signer in these tests.
func buildSignedManifest(t *testing.T, kbRoots map[string]string) provisioning.Manifest {
	t.Helper()
	m, err := provisioning.BuildManifest(nil, kbRoots, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	for i := range m.Artifacts {
		m.Artifacts[i].Signed = true
	}
	return m
}

// kbSection returns the substring strictly between KB name's begin/end
// markers, using the LAST occurrence of the end marker — so a curated body
// that contains a forged marker line still yields the real, complete region.
// Fails the test if either marker is missing or out of order.
func kbSection(t *testing.T, content, name string) string {
	t.Helper()
	begin := "<!-- cartographer:kb:" + name + ":begin -->"
	end := "<!-- cartographer:kb:" + name + ":end -->"
	bi := strings.Index(content, begin)
	ei := strings.LastIndex(content, end)
	if bi == -1 || ei == -1 || ei < bi {
		t.Fatalf("markers for KB %q not found or out of order in:\n%s", name, content)
	}
	return content[bi+len(begin) : ei]
}

// 1. Two KBs, two attributed regions, alphabetical by default, each KB's
// markdown preserved byte-for-byte inside its own region.
func TestApply_Instructions_DueKB_RegioniAttribuite(t *testing.T) {
	alfaRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	zetaRoot := makeKBWithArchives(t, map[string][]string{"entities": {"z.md"}})
	writeFile(t, filepath.Join(alfaRoot, "instructions.md"), "## Alfa heading\n\nAlfa rule one.\n")
	writeFile(t, filepath.Join(zetaRoot, "instructions.md"), "## Zeta heading\n\nZeta rule one.\n")

	m := buildSignedManifest(t, map[string]string{"alfa": alfaRoot, "zeta": zetaRoot})
	baseDir := t.TempDir()
	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: provisioning.Lock{},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(baseDir, ".claude", "CLAUDE.md"))
	content := string(data)

	idxAlfaBegin := strings.Index(content, "<!-- cartographer:kb:alfa:begin -->")
	idxZetaBegin := strings.Index(content, "<!-- cartographer:kb:zeta:begin -->")
	if idxAlfaBegin == -1 || idxZetaBegin == -1 {
		t.Fatalf("missing per-KB begin markers:\n%s", content)
	}
	if idxAlfaBegin > idxZetaBegin {
		t.Errorf("expected alfa's region before zeta's (alphabetical default):\n%s", content)
	}

	alfaBody := kbSection(t, content, "alfa")
	if !strings.Contains(alfaBody, "## Alfa heading\n\nAlfa rule one.") {
		t.Errorf("alfa's curated markdown not preserved verbatim inside its region:\n%s", alfaBody)
	}
	zetaBody := kbSection(t, content, "zeta")
	if !strings.Contains(zetaBody, "## Zeta heading\n\nZeta rule one.") {
		t.Errorf("zeta's curated markdown not preserved verbatim inside its region:\n%s", zetaBody)
	}
	if strings.Contains(alfaBody, "Zeta rule") || strings.Contains(zetaBody, "Alfa rule") {
		t.Errorf("regions bled into each other:\nalfa:\n%s\nzeta:\n%s", alfaBody, zetaBody)
	}
}

// 2. Marker counts unchanged: exactly one outer begin/end regardless of how
// many KBs contribute, and exactly one per-KB begin/end for each of them.
func TestApply_Instructions_ConteggioMarkerInvariato(t *testing.T) {
	roots := map[string]string{
		"alfa": makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}}),
		"beta": makeKBWithArchives(t, map[string][]string{"entities": {"b.md"}}),
		"zeta": makeKBWithArchives(t, map[string][]string{"entities": {"z.md"}}),
	}
	for name, root := range roots {
		writeFile(t, filepath.Join(root, "instructions.md"), "Rule for "+name+".\n")
	}
	m := buildSignedManifest(t, roots)
	baseDir := t.TempDir()
	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: provisioning.Lock{},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(baseDir, ".claude", "CLAUDE.md"))
	content := string(data)

	if n := strings.Count(content, "cartographer:instructions:begin"); n != 1 {
		t.Errorf("expected 1 outer begin marker regardless of KB count, got %d:\n%s", n, content)
	}
	if n := strings.Count(content, "cartographer:instructions:end"); n != 1 {
		t.Errorf("expected 1 outer end marker regardless of KB count, got %d:\n%s", n, content)
	}
	for name := range roots {
		if n := strings.Count(content, "<!-- cartographer:kb:"+name+":begin -->"); n != 1 {
			t.Errorf("expected exactly 1 begin marker for KB %q, got %d:\n%s", name, n, content)
		}
		if n := strings.Count(content, "<!-- cartographer:kb:"+name+":end -->"); n != 1 {
			t.Errorf("expected exactly 1 end marker for KB %q, got %d:\n%s", name, n, content)
		}
	}
}

// 3. Client-wide trailers (the subagent sentence, D154) stay outside every
// per-KB region: after the last KB's end marker, not inside it.
func TestApply_Instructions_TrailerFuoriDalleRegioni(t *testing.T) {
	alfaRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	zetaRoot := makeKBWithArchives(t, map[string][]string{"entities": {"z.md"}})
	writeFile(t, filepath.Join(alfaRoot, "agents", "helper.md"), "---\nname: helper\ndescription: Helper agent.\n---\nPrompt.\n")
	writeFile(t, filepath.Join(zetaRoot, "agents", "runner.md"), "---\nname: runner\ndescription: Runner agent.\n---\nPrompt.\n")

	m := buildSignedManifest(t, map[string]string{"alfa": alfaRoot, "zeta": zetaRoot})
	baseDir := t.TempDir()
	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: provisioning.Lock{},
		KBRoots: map[string]string{"alfa": alfaRoot, "zeta": zetaRoot},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(baseDir, ".claude", "CLAUDE.md"))
	content := string(data)

	idxSentence := strings.Index(content, "Subagents installed")
	if idxSentence == -1 {
		t.Fatalf("subagent sentence missing:\n%s", content)
	}
	idxAlfaEnd := strings.Index(content, "<!-- cartographer:kb:alfa:end -->")
	idxZetaEnd := strings.Index(content, "<!-- cartographer:kb:zeta:end -->")
	lastKBEnd := idxAlfaEnd
	if idxZetaEnd > lastKBEnd {
		lastKBEnd = idxZetaEnd
	}
	if idxSentence < lastKBEnd {
		t.Errorf("trailer must appear after the last KB's end marker, not inside a region:\n%s", content)
	}
	idxOuterEnd := strings.Index(content, "cartographer:instructions:end")
	if idxOuterEnd == -1 || idxSentence > idxOuterEnd {
		t.Errorf("trailer must still be inside the outer managed block:\n%s", content)
	}
}

// 4. The scope sentence is emitted only when there is curated content to
// scope; a KB without instructions.md keeps its routing line and bullets and
// gets no scope sentence.
func TestGenerateKBInstructions_ScopeSentenceSoloConCurato(t *testing.T) {
	withCurated := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	writeFile(t, filepath.Join(withCurated, "instructions.md"), "Some curated rule.\n")
	withoutCurated := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})

	m1, err := provisioning.BuildManifest(nil, map[string]string{"homelab": withCurated}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest (curated): %v", err)
	}
	content1 := instructionsContent(t, m1, "homelab")
	if !strings.Contains(content1, "govern work in its perimeter") {
		t.Errorf("expected the scope sentence when curated content exists:\n%s", content1)
	}

	m2, err := provisioning.BuildManifest(nil, map[string]string{"homelab": withoutCurated}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest (no curated): %v", err)
	}
	content2 := instructionsContent(t, m2, "homelab")
	if strings.Contains(content2, "govern work in its perimeter") {
		t.Errorf("scope sentence must not appear without curated content:\n%s", content2)
	}
	if !strings.Contains(content2, "Operational instructions:") {
		t.Errorf("routing line and bullets must still be present without curated content:\n%s", content2)
	}
}

// 5. A KB that opts out of the generated bullets (preambleNoneRe) still gets
// delimiters and the scope sentence — the opt-out is about the operational
// bullets, not about attribution.
func TestApply_Instructions_OptOutPreambleMantieneAttribuzione(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	writeFile(t, filepath.Join(kbRoot, "instructions.md"),
		"<!-- cartographer: preamble: none -->\nOwn rule, no generated bullets.\n")

	m := buildSignedManifest(t, map[string]string{"homelab": kbRoot})
	baseDir := t.TempDir()
	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: provisioning.Lock{},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(baseDir, ".claude", "CLAUDE.md"))
	content := string(data)

	if !strings.Contains(content, "<!-- cartographer:kb:homelab:begin -->") || !strings.Contains(content, "<!-- cartographer:kb:homelab:end -->") {
		t.Errorf("delimiters missing for an opted-out KB:\n%s", content)
	}
	if !strings.Contains(content, "govern work in its perimeter") {
		t.Errorf("scope sentence missing for an opted-out KB:\n%s", content)
	}
	if strings.Contains(content, "Operational instructions:") {
		t.Errorf("opt-out must still suppress the generated bullets:\n%s", content)
	}
	if !strings.Contains(content, "Own rule, no generated bullets.") {
		t.Errorf("curated content missing:\n%s", content)
	}
}

// 6. Hostile curated content: a line mimicking a "cartographer:kb:" marker
// must not be able to forge a section boundary — nothing truncated, nothing
// split into the next KB's region.
func TestApply_Instructions_ContenutoOstile_NonSpezzaLaRegione(t *testing.T) {
	homelabRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	zetaRoot := makeKBWithArchives(t, map[string][]string{"entities": {"z.md"}})
	writeFile(t, filepath.Join(homelabRoot, "instructions.md"),
		"# Homelab notes\n\n<!-- cartographer:kb:homelab:end -->\n\nText after a forged end marker must survive.\n")
	writeFile(t, filepath.Join(zetaRoot, "instructions.md"), "Zeta rule.\n")

	m := buildSignedManifest(t, map[string]string{"homelab": homelabRoot, "zeta": zetaRoot})
	baseDir := t.TempDir()
	if _, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: provisioning.Lock{},
	}); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(baseDir, ".claude", "CLAUDE.md"))
	content := string(data)

	if !strings.Contains(content, "Text after a forged end marker must survive.") {
		t.Errorf("content after the hostile line was truncated:\n%s", content)
	}
	if !strings.Contains(content, "Zeta rule.") {
		t.Errorf("the following KB's section was corrupted by the hostile line:\n%s", content)
	}
	homelabBody := kbSection(t, content, "homelab")
	if !strings.Contains(homelabBody, "Text after a forged end marker must survive.") {
		t.Errorf("homelab's own (real) region does not contain the text past the forged marker:\n%s", homelabBody)
	}
}

// 7. Section order follows an explicit binding; no binding restores the
// alphabetical fallback; a pure reorder of the binding (same KB set) still
// rewrites the block, since neither ContentHash nor the KB set changed.
func TestApply_Instructions_OrdineDaBinding(t *testing.T) {
	baseDir := t.TempDir()
	aZ := instructionsArtifact("zeta", "Zeta section.\n")
	aA := instructionsArtifact("alfa", "Alfa section.\n")
	m := provisioning.MergeArtifacts([]provisioning.Artifact{aZ, aA})
	path := filepath.Join(baseDir, ".claude", "CLAUDE.md")

	// Explicit binding order: zeta before alfa.
	res1, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: provisioning.Lock{},
		KBOrder: []string{"zeta", "alfa"},
	})
	if err != nil {
		t.Fatalf("Apply 1: %v", err)
	}
	data1, _ := os.ReadFile(path)
	content1 := string(data1)
	if i, j := strings.Index(content1, "Zeta section."), strings.Index(content1, "Alfa section."); i == -1 || j == -1 || i > j {
		t.Fatalf("binding order not honoured (want zeta before alfa):\n%s", content1)
	}

	// Removing the binding restores alphabetical order — and an order change
	// must rewrite the block even though the KB set and content are unchanged.
	res2, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: res1.NewLock,
	})
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	if len(res2.Written) == 0 {
		t.Errorf("removing the binding (an order change) must rewrite the block, got no Written: %+v", res2)
	}
	data2, _ := os.ReadFile(path)
	content2 := string(data2)
	if i, j := strings.Index(content2, "Alfa section."), strings.Index(content2, "Zeta section."); i == -1 || j == -1 || i > j {
		t.Fatalf("removing the binding did not restore alphabetical order (want alfa before zeta):\n%s", content2)
	}

	// A pure reorder of the binding — same set, sequence back to zeta-then-alfa.
	res3, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: res2.NewLock,
		KBOrder: []string{"zeta", "alfa"},
	})
	if err != nil {
		t.Fatalf("Apply 3: %v", err)
	}
	if len(res3.Written) == 0 {
		t.Errorf("a pure reorder of the binding must rewrite the block, got no Written: %+v", res3)
	}
	data3, _ := os.ReadFile(path)
	content3 := string(data3)
	if i, j := strings.Index(content3, "Zeta section."), strings.Index(content3, "Alfa section."); i == -1 || j == -1 || i > j {
		t.Errorf("reordered binding not reflected in the file bytes:\n%s", content3)
	}
}

// 8. Idempotence extended to the multi-KB case: two consecutive applies with
// unchanged inputs (including the same explicit KBOrder) leave the file
// byte-identical and report nothing to write.
func TestApply_Instructions_IdempotenteMultiKB(t *testing.T) {
	baseDir := t.TempDir()
	aZ := instructionsArtifact("zeta", "Zeta stable.\n")
	aA := instructionsArtifact("alfa", "Alfa stable.\n")
	aB := instructionsArtifact("beta", "Beta stable.\n")
	m := provisioning.MergeArtifacts([]provisioning.Artifact{aZ, aA, aB})
	path := filepath.Join(baseDir, ".claude", "CLAUDE.md")

	res1, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: provisioning.Lock{},
		KBOrder: []string{"zeta", "alfa", "beta"},
	})
	if err != nil {
		t.Fatalf("Apply 1: %v", err)
	}
	data1, _ := os.ReadFile(path)

	res2, err := provisioning.Apply(m, provisioning.ApplyOptions{
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: res1.NewLock,
		KBOrder: []string{"zeta", "alfa", "beta"},
	})
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	data2, _ := os.ReadFile(path)

	if string(data1) != string(data2) {
		t.Errorf("applying twice with unchanged inputs (multi-KB) changed the file:\nbefore:\n%q\nafter:\n%q", data1, data2)
	}
	if len(res2.Written) != 0 {
		t.Errorf("second Apply (in-sync, multi-KB): expected no Written, got %+v", res2.Written)
	}
}
