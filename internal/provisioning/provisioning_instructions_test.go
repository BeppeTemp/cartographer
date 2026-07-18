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

	m1, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, false)
	if err != nil {
		t.Fatalf("BuildManifest 1: %v", err)
	}
	m2, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, false)
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
	m3, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, false)
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
	m4, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, false)
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

	m, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, false)
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

	m, err := provisioning.BuildManifest(nil, map[string]string{"vuota": kbRoot}, false)
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

	m, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, false)
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	content := string(findInstructionsArtifact(t, m, "homelab").Files[0].Content)

	// Names only, alphabetically sorted, no description (already in the
	// client's agent registry, D65).
	if !strings.Contains(content, "Subagents installed by this KB: anonimo, zorro —") {
		t.Errorf("agent section missing or not sorted by name:\n%s", content)
	}
	if strings.Contains(content, "Defends the KB from intruders") {
		t.Errorf("agent descriptions must not be duplicated in the instructions (D65):\n%s", content)
	}
}

func TestBuildManifest_Instructions_NessunaSezioneSenzaAgentNeCurato(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})

	m, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, false)
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
		"- consult it autonomously when you need historical or architectural context: `search` (keyword + semantic) or `atlas_overview` to orient yourself, `concept_read` to read;\n" +
		"- write or update a page with `concept_write` when you discover something relevant; close relevant sessions with `log_append`;\n" +
		"- every write is a git commit, revertible.\n"
	if content != want {
		t.Errorf("output changed with no agent/instructions.md:\ngot:\n%s\nwant:\n%s", content, want)
	}
}

func TestBuildManifest_Instructions_CuratoSenzaFrontmatter(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	writeFile(t, filepath.Join(kbRoot, "instructions.md"), "Rule: bulk reads -> explorer.\n")

	m, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, false)
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

	m, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, false)
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

	m1, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, false)
	if err != nil {
		t.Fatalf("BuildManifest 1: %v", err)
	}
	h1 := findInstructionsArtifact(t, m1, "homelab").ContentHash

	// The instructions list only names (D65): changing the description doesn't
	// change the block. (The updated description still travels with the
	// "agent" kind artifact, which has its own ContentHash.)
	writeFile(t, agentPath, "---\ndescription: Second version\n---\nPrompt.\n")
	m2, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, false)
	if err != nil {
		t.Fatalf("BuildManifest 2: %v", err)
	}
	h2 := findInstructionsArtifact(t, m2, "homelab").ContentHash

	if h1 != h2 {
		t.Error("the instructions ContentHash must not change when only an agent's description changes (D65)")
	}

	// An extra agent, on the other hand, changes the list of names: the hash must change.
	writeFile(t, filepath.Join(agentsDir, "nuovo.md"), "---\ndescription: X\n---\nPrompt.\n")
	m3, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, false)
	if err != nil {
		t.Fatalf("BuildManifest 3: %v", err)
	}
	h3 := findInstructionsArtifact(t, m3, "homelab").ContentHash
	if h3 == h1 {
		t.Error("the instructions ContentHash must change when an agent is added")
	}
}

func TestBuildManifest_Instructions_HashCambiaConCurato(t *testing.T) {
	kbRoot := makeKBWithArchives(t, map[string][]string{"entities": {"a.md"}})
	instrPath := filepath.Join(kbRoot, "instructions.md")
	writeFile(t, instrPath, "Version one.\n")

	m1, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, false)
	if err != nil {
		t.Fatalf("BuildManifest 1: %v", err)
	}
	h1 := findInstructionsArtifact(t, m1, "homelab").ContentHash

	writeFile(t, instrPath, "Version two.\n")
	m2, err := provisioning.BuildManifest(nil, map[string]string{"homelab": kbRoot}, false)
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
