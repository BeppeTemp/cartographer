package provisioning

// Tests for on-disk verification and healing (D139).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/execbit"
)

// driftKB builds a KB with one skill (script included), one agent and one hook,
// then applies it, returning the base dir and the resulting lock.
func driftKB(t *testing.T, opts ...func(*ApplyOptions)) (baseDir string, m Manifest, applied AppliedResult, kbRoot string) {
	t.Helper()
	kbRoot = t.TempDir()

	skillDir := filepath.Join(kbRoot, "skills", "runbooks")
	os.MkdirAll(filepath.Join(skillDir, "scripts"), 0o755)
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: runbooks\ndescription: d\n---\nBody.\n"), 0o644)
	os.WriteFile(filepath.Join(skillDir, "scripts", "run.sh"), []byte("#!/bin/sh\necho hi\n"), 0o755)

	os.MkdirAll(filepath.Join(kbRoot, "agents"), 0o755)
	os.WriteFile(filepath.Join(kbRoot, "agents", "reviewer.md"), []byte("---\nname: reviewer\n---\nBody.\n"), 0o644)

	hookDir := filepath.Join(kbRoot, "hooks", "greet")
	os.MkdirAll(hookDir, 0o755)
	os.WriteFile(filepath.Join(hookDir, "hook.json"), []byte(`{"event":"SessionStart","command":"./run.sh"}`+"\n"), 0o644)
	os.WriteFile(filepath.Join(hookDir, "run.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755)

	var err error
	m, err = BuildManifest(nil, map[string]string{"wiki": kbRoot}, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}

	baseDir = t.TempDir()
	applyOpts := ApplyOptions{ApprovedMCP: approveAll(m),
		KBRoots:  map[string]string{"wiki": kbRoot},
		Provider: configurator.ProviderClaudeCode,
		BaseDir:  baseDir,
		Lock:     Lock{},
	}
	for _, o := range opts {
		o(&applyOpts)
	}
	applied, err = Apply(m, applyOpts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return baseDir, m, applied, kbRoot
}

func findingFor(findings []DriftFinding, kind, name string) (DriftFinding, bool) {
	for _, f := range findings {
		if f.Kind == kind && f.Name == name {
			return f, true
		}
	}
	return DriftFinding{}, false
}

// The clean-install invariant: right after Apply, nothing diverges. It also
// covers the executable-mode normalization (a hook's hook.json is forced
// non-executable, its scripts executable) — hashing the source flags instead
// would report every hook as modified here.
func TestVerifyManaged_CleanInstallHasNoDrift(t *testing.T) {
	baseDir, _, applied, _ := driftKB(t)

	if findings := VerifyManaged(applied.NewLock, configurator.ProviderClaudeCode, baseDir); len(findings) != 0 {
		t.Fatalf("clean install reports drift: %+v", findings)
	}
}

func TestVerifyManaged_DetectsModifiedAndMissing(t *testing.T) {
	baseDir, _, applied, _ := driftKB(t)
	lock := applied.NewLock

	// One byte changed in a materialized skill.
	skillFile := filepath.Join(baseDir, ".claude", "skills", "runbooks", "SKILL.md")
	data, _ := os.ReadFile(skillFile)
	os.WriteFile(skillFile, append(data, []byte("local edit\n")...), 0o644)

	findings := VerifyManaged(lock, configurator.ProviderClaudeCode, baseDir)
	f, ok := findingFor(findings, "skill", "runbooks")
	if !ok || f.Reason != DriftModified {
		t.Fatalf("expected the skill to be modified, got %+v", findings)
	}
	if _, ok := findingFor(findings, "agent", "reviewer"); ok {
		t.Errorf("an untouched agent must not be reported: %+v", findings)
	}

	// An extra file inside the artifact's own directory is not the lockfile's:
	// foreign, information only, and not healable (#1502, was "modified" in D139).
	os.WriteFile(skillFile, data, 0o644)
	os.WriteFile(filepath.Join(baseDir, ".claude", "skills", "runbooks", "stray.md"), []byte("x\n"), 0o644)
	if f, ok := findingFor(VerifyManaged(lock, configurator.ProviderClaudeCode, baseDir), "skill", "runbooks"); !ok || f.Reason != DriftForeign || f.Healable() || !strings.Contains(f.Detail, "stray.md") {
		t.Errorf("an extra file in the artifact directory must be foreign and not healable, got %+v", f)
	}
	os.Remove(filepath.Join(baseDir, ".claude", "skills", "runbooks", "stray.md"))

	// A deleted agent file.
	os.Remove(filepath.Join(baseDir, ".claude", "agents", "reviewer.md"))
	if f, ok := findingFor(VerifyManaged(lock, configurator.ProviderClaudeCode, baseDir), "agent", "reviewer"); !ok || f.Reason != DriftMissing {
		t.Errorf("expected the agent to be missing, got %+v", f)
	}

	// A whole skill directory removed.
	os.RemoveAll(filepath.Join(baseDir, ".claude", "skills", "runbooks"))
	if f, ok := findingFor(VerifyManaged(lock, configurator.ProviderClaudeCode, baseDir), "skill", "runbooks"); !ok || f.Reason != DriftMissing {
		t.Errorf("expected the skill directory to be missing, got %+v", f)
	}
}

// A chmod alone is drift: the executable bit is part of the materialized hash.
func TestVerifyManaged_DetectsModeDrift(t *testing.T) {
	if !execbit.Supported {
		t.Skip("the assertion is that a chmod alone is drift; this filesystem has no execute bit to remove, so the artifact on disk is unchanged")
	}
	baseDir, _, applied, _ := driftKB(t)
	script := filepath.Join(baseDir, ".claude", "skills", "runbooks", "scripts", "run.sh")
	if err := os.Chmod(script, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if f, ok := findingFor(VerifyManaged(applied.NewLock, configurator.ProviderClaudeCode, baseDir), "skill", "runbooks"); !ok || f.Reason != DriftModified {
		t.Errorf("a lost executable bit must be drift, got %+v", f)
	}
}

// A pre-D138 lockfile has no materialized hash: unknown, never modified.
func TestVerifyManaged_NoMaterializedHashIsUnknown(t *testing.T) {
	baseDir, _, applied, _ := driftKB(t)
	lock := applied.NewLock
	for i := range lock.Managed {
		lock.Managed[i].MaterializedHash = ""
	}
	// Change the content: it must still not be reported as modified.
	skillFile := filepath.Join(baseDir, ".claude", "skills", "runbooks", "SKILL.md")
	os.WriteFile(skillFile, []byte("rewritten\n"), 0o644)

	for _, f := range VerifyManaged(lock, configurator.ProviderClaudeCode, baseDir) {
		if f.Reason != DriftUnknown {
			t.Errorf("expected only unknown findings, got %+v", f)
		}
		if f.Healable() {
			t.Errorf("an unknown finding must not be healable: %+v", f)
		}
	}
}

// Existence needs no recorded hash: a pre-D138 entry whose files are gone is
// missing, not unknown, and is healable — otherwise `status` reports in-sync
// on a skill that is not on disk and `sync` never restores it.
func TestVerifyManaged_NoMaterializedHashStillDetectsMissing(t *testing.T) {
	baseDir, _, applied, _ := driftKB(t)
	lock := applied.NewLock
	for i := range lock.Managed {
		lock.Managed[i].MaterializedHash = ""
	}
	os.RemoveAll(filepath.Join(baseDir, ".claude", "skills", "runbooks"))
	os.Remove(filepath.Join(baseDir, ".claude", "agents", "reviewer.md"))

	findings := VerifyManaged(lock, configurator.ProviderClaudeCode, baseDir)
	for _, want := range []struct{ kind, name string }{{"skill", "runbooks"}, {"agent", "reviewer"}} {
		f, ok := findingFor(findings, want.kind, want.name)
		if !ok || f.Reason != DriftMissing {
			t.Errorf("%s %q: expected missing without a hash, got %+v", want.kind, want.name, f)
		}
		if !f.Healable() {
			t.Errorf("%s %q: a missing artifact must be healable, got %+v", want.kind, want.name, f)
		}
	}
}

// mcp/instructions live inside a file shared with the user: presence only.
func TestVerifyManaged_SharedFileEntriesArePresenceChecked(t *testing.T) {
	kbRoot := t.TempDir()
	os.WriteFile(filepath.Join(kbRoot, "instructions.md"), []byte("Use this KB.\n"), 0o644)

	m, err := BuildManifest(nil, map[string]string{"wiki": kbRoot}, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	baseDir := t.TempDir()
	applied, err := Apply(m, ApplyOptions{ApprovedMCP: approveAll(m), KBRoots: map[string]string{"wiki": kbRoot},
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: Lock{}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if findings := VerifyManaged(applied.NewLock, configurator.ProviderClaudeCode, baseDir); len(findings) != 0 {
		t.Fatalf("clean install reports drift: %+v", findings)
	}

	// The user edits their own instructions file around the block: presence is
	// what matters, not content.
	claudeMd := filepath.Join(baseDir, ".claude", "CLAUDE.md")
	data, _ := os.ReadFile(claudeMd)
	os.WriteFile(claudeMd, append([]byte("My own notes.\n\n"), data...), 0o644)
	if findings := VerifyManaged(applied.NewLock, configurator.ProviderClaudeCode, baseDir); len(findings) != 0 {
		t.Fatalf("editing around the managed block must not be drift: %+v", findings)
	}

	// The block itself is gone: unregistered.
	os.WriteFile(claudeMd, []byte("My own notes only.\n"), 0o644)
	f, ok := findingFor(VerifyManaged(applied.NewLock, configurator.ProviderClaudeCode, baseDir), "instructions", "wiki")
	if !ok || f.Reason != DriftUnregistered {
		t.Fatalf("expected the instructions block to be unregistered, got %+v", f)
	}
}

func TestApply_HealsModifiedArtifact(t *testing.T) {
	baseDir, m, applied, kbRoot := driftKB(t)
	skillFile := filepath.Join(baseDir, ".claude", "skills", "runbooks", "SKILL.md")
	pristine, _ := os.ReadFile(skillFile)
	os.WriteFile(skillFile, []byte("locally rewritten\n"), 0o644)

	opts := ApplyOptions{ApprovedMCP: approveAll(m), KBRoots: map[string]string{"wiki": kbRoot},
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: applied.NewLock}

	// Dry run heals nothing.
	dry := opts
	dry.DryRun = true
	if _, err := Apply(m, dry); err != nil {
		t.Fatalf("Apply dry-run: %v", err)
	}
	if data, _ := os.ReadFile(skillFile); string(data) != "locally rewritten\n" {
		t.Errorf("dry-run wrote to disk: %q", data)
	}

	// --no-heal reports and leaves it alone.
	noHeal := opts
	noHeal.NoHeal = true
	res, err := Apply(m, noHeal)
	if err != nil {
		t.Fatalf("Apply no-heal: %v", err)
	}
	if len(res.Healed) != 0 {
		t.Errorf("NoHeal must not heal: %+v", res.Healed)
	}
	if f, ok := findingFor(res.Divergent, "skill", "runbooks"); !ok || f.Reason != DriftModified {
		t.Errorf("expected the skill in Divergent, got %+v", res.Divergent)
	}
	if data, _ := os.ReadFile(skillFile); string(data) != "locally rewritten\n" {
		t.Errorf("NoHeal wrote to disk: %q", data)
	}

	// The default restores it byte for byte and says so.
	res, err = Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply heal: %v", err)
	}
	data, _ := os.ReadFile(skillFile)
	if string(data) != string(pristine) {
		t.Errorf("skill not restored:\n%q\nwant\n%q", data, pristine)
	}
	found := false
	for _, mf := range res.Healed {
		if mf.Kind == "skill" && mf.Name == "runbooks" {
			found = true
		}
	}
	if !found {
		t.Errorf("restored artifact not reported in Healed: %+v", res.Healed)
	}
	// And the heal is complete: a following verification is clean.
	if findings := VerifyManaged(res.NewLock, configurator.ProviderClaudeCode, baseDir); len(findings) != 0 {
		t.Errorf("drift survives the heal: %+v", findings)
	}
}

func TestApply_HealsMissingAgentAndInstructionsBlock(t *testing.T) {
	kbRoot := t.TempDir()
	os.MkdirAll(filepath.Join(kbRoot, "agents"), 0o755)
	os.WriteFile(filepath.Join(kbRoot, "agents", "reviewer.md"), []byte("---\nname: reviewer\n---\nBody.\n"), 0o644)
	os.WriteFile(filepath.Join(kbRoot, "instructions.md"), []byte("Use this KB.\n"), 0o644)

	m, err := BuildManifest(nil, map[string]string{"wiki": kbRoot}, BuildOptions{})
	if err != nil {
		t.Fatalf("BuildManifest: %v", err)
	}
	baseDir := t.TempDir()
	opts := ApplyOptions{ApprovedMCP: approveAll(m), KBRoots: map[string]string{"wiki": kbRoot},
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: Lock{}}
	applied, err := Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	os.Remove(filepath.Join(baseDir, ".claude", "agents", "reviewer.md"))
	os.WriteFile(filepath.Join(baseDir, ".claude", "CLAUDE.md"), []byte("only my notes\n"), 0o644)

	opts.Lock = applied.NewLock
	res, err := Apply(m, opts)
	if err != nil {
		t.Fatalf("Apply heal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".claude", "agents", "reviewer.md")); err != nil {
		t.Errorf("deleted agent not restored: %v", err)
	}
	claude, _ := os.ReadFile(filepath.Join(baseDir, ".claude", "CLAUDE.md"))
	if !strings.Contains(string(claude), instructionsBlockBeginPrefix) {
		t.Errorf("instructions block not re-registered:\n%s", claude)
	}
	if !strings.Contains(string(claude), "only my notes") {
		t.Errorf("the user's own content was discarded:\n%s", claude)
	}
	if findings := VerifyManaged(res.NewLock, configurator.ProviderClaudeCode, baseDir); len(findings) != 0 {
		t.Errorf("drift survives the heal: %+v", findings)
	}
}

// #1502: a manifest from a server can carry an agent or hook name that cannot be
// a path segment. It costs that artifact only: the others are written and
// locked, and the bad one is reported (it used to abort Apply after some
// artifacts were already on disk without a lock entry).
func TestApply_BadArtifactNameCostsOnlyItself(t *testing.T) {
	_, m, _, kbRoot := driftKB(t)
	for _, bad := range []Artifact{
		{Kind: "agent", Name: "", Source: "kb:wiki", ContentHash: "x"},
		{Kind: "hook", Name: `a\b`, Source: "kb:wiki", ContentHash: "x"},
		{Kind: "agent", Name: "../escape", Source: "kb:wiki", ContentHash: "x"},
	} {
		m.Artifacts = append(m.Artifacts, bad)
	}
	baseDir := t.TempDir()
	res, err := Apply(m, ApplyOptions{ApprovedMCP: approveAll(m), KBRoots: map[string]string{"wiki": kbRoot},
		Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: Lock{}})
	if err != nil {
		t.Fatalf("Apply must not abort: %v", err)
	}
	if len(res.Refused) != 3 || !strings.Contains(strings.Join(res.Warnings, "\n"), "not a valid artifact name") {
		t.Errorf("refused %v, warnings %v", res.Refused, res.Warnings)
	}
	if _, ok := findingFor(VerifyManaged(res.NewLock, configurator.ProviderClaudeCode, baseDir), "skill", "runbooks"); ok {
		t.Error("the valid skill must be applied and clean")
	}
	if _, statErr := os.Stat(filepath.Join(baseDir, ".claude", "skills", "runbooks", "SKILL.md")); statErr != nil {
		t.Errorf("the valid skill was not written: %v", statErr)
	}
}

// BuildManifest leaves out a bad agent or hook name with an issue, as it does a
// skill (D191).
func TestBuildManifest_ExcludesBadAgentAndHookNames(t *testing.T) {
	kbRoot := t.TempDir()
	os.MkdirAll(filepath.Join(kbRoot, "agents"), 0o755)
	os.WriteFile(filepath.Join(kbRoot, "agents", ".md"), []byte("x\n"), 0o644)
	os.WriteFile(filepath.Join(kbRoot, "agents", "ok.md"), []byte("x\n"), 0o644)
	m, err := BuildManifest(nil, map[string]string{"wiki": kbRoot}, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range m.Artifacts {
		if a.Kind == "agent" {
			names = append(names, a.Name)
		}
	}
	if len(names) != 1 || names[0] != "ok" {
		t.Errorf("agents = %v, want only ok", names)
	}
	if !strings.Contains(strings.Join(m.Issues, "\n"), "not a valid artifact name") {
		t.Errorf("no issue recorded: %v", m.Issues)
	}
}

// #1502: a file the lock does not own (a Finder .DS_Store, a __pycache__) used
// to make the whole-directory re-hash differ from the recorded one for good:
// every run healed the artifact, reported it, and the next run did it again.
func TestVerifyManaged_ForeignFileDoesNotHealForever(t *testing.T) {
	baseDir, m, applied, kbRoot := driftKB(t)
	skillDir := filepath.Join(baseDir, ".claude", "skills", "runbooks")
	os.MkdirAll(filepath.Join(skillDir, "scripts", "__pycache__"), 0o755)
	os.WriteFile(filepath.Join(skillDir, ".DS_Store"), []byte("junk"), 0o644)
	os.WriteFile(filepath.Join(skillDir, "scripts", "__pycache__", "run.pyc"), []byte("junk"), 0o644)

	lock := applied.NewLock
	for i := 0; i < 3; i++ {
		res, err := Apply(m, ApplyOptions{ApprovedMCP: approveAll(m),
			KBRoots: map[string]string{"wiki": kbRoot}, Provider: configurator.ProviderClaudeCode, BaseDir: baseDir, Lock: lock})
		if err != nil {
			t.Fatalf("Apply %d: %v", i, err)
		}
		if len(res.Healed) != 0 || len(res.Written) != 0 {
			t.Fatalf("run %d healed or wrote for a foreign file: healed %v written %v", i, res.Healed, res.Written)
		}
		lock = res.NewLock
	}
	for _, f := range VerifyManaged(lock, configurator.ProviderClaudeCode, baseDir) {
		if f.Reason != DriftForeign || f.Healable() {
			t.Errorf("only an informational foreign finding is expected, got %+v", f)
		}
	}
	f, ok := findingFor(VerifyManaged(lock, configurator.ProviderClaudeCode, baseDir), "skill", "runbooks")
	if !ok || !strings.Contains(f.Detail, ".DS_Store") || !strings.Contains(f.Detail, "scripts/__pycache__/run.pyc") {
		t.Errorf("doctor must be able to list the foreign files: %+v", f)
	}

	// An owned file edited next to a foreign one is still drift.
	os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("edited\n"), 0o644)
	if f, ok := findingFor(VerifyManaged(lock, configurator.ProviderClaudeCode, baseDir), "skill", "runbooks"); !ok || f.Reason != DriftModified {
		t.Errorf("an owned file edited must stay modified, got %+v", f)
	}
}
