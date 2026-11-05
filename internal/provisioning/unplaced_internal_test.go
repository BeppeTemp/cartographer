package provisioning

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func kbSkill(kb, name string) Artifact {
	return Artifact{Kind: "skill", Name: name, Source: "kb:" + kb, ContentHash: "h-" + name}
}

func kbAgent(kb, name string) Artifact {
	return Artifact{Kind: "agent", Name: name, Source: "kb:" + kb, ContentHash: "h-" + name}
}

func names(as []Artifact) string {
	var out []string
	for _, a := range as {
		out = append(out, a.Kind+"/"+a.Name)
	}
	return strings.Join(out, ",")
}

// fork-ext has no skill cell in the global scope and has one in the project scope.
func TestUnplacedArtifacts_KeyedOnTheMissingCell(t *testing.T) {
	registerTestVariant(t, testVariant(t, "fork-ext", testForkExt, "linux", nil))
	p := configurator.Provider("fork-ext")
	m := Manifest{Artifacts: []Artifact{
		kbSkill("kb-a", "zeta"), kbSkill("kb-a", "alpha"), kbAgent("kb-a", "scout"),
		{Kind: "skill", Name: "cartographer-cli", Source: "bundle", BuiltIn: true},
		{Kind: "hook", Name: "h", Source: "kb:kb-a"},
	}}
	if got := names(UnplacedArtifacts(m, p, ScopeGlobal)); got != "skill/zeta,skill/alpha,agent/scout" {
		t.Errorf("global = %s", got)
	}
	if got := UnplacedArtifacts(m, p, ScopeProject); got != nil {
		t.Errorf("a scope with a skill cell lists %s", names(got))
	}
	if got := UnplacedArtifacts(m, configurator.ProviderClaudeCode, ScopeGlobal); got != nil {
		t.Errorf("a built-in with a skill cell lists %s", names(got))
	}
}

func TestUnplacedSentence_SortedCappedAndPinnedToKBGuide(t *testing.T) {
	opts := ApplyOptions{Unplaced: []Artifact{kbSkill("kb-b", "b2"), kbSkill("kb-b", "b1"), kbAgent("kb-b", "scout"), kbSkill("kb-a", "a1")}}
	got := unplacedSentence(opts)
	want := "The \"kb-a\" KB's skills are not installed as files on this client: a1. Read one with " + ArtifactReadShape +
		" (pass `kb: \"kb-a\"` on a routed connection) and follow it; a step that needs a CLI, a script or a subagent does not apply.\n\n" +
		"The \"kb-b\" KB's skills are not installed as files on this client: b1, b2. Read one with " + ArtifactReadShape +
		" (pass `kb: \"kb-b\"` on a routed connection) and follow it; a step that needs a CLI, a script or a subagent does not apply.\n\n" +
		"The \"kb-b\" KB's agents are not installed as subagents: scout. Read `agents/<name>.md` the same way and follow it yourself."
	if got != want {
		t.Errorf("sentence:\n%s\nwant:\n%s", got, want)
	}
	var many []Artifact
	for i := 0; i < maxSkillNames+3; i++ {
		many = append(many, kbSkill("kb-a", fmt.Sprintf("s%03d", i)))
	}
	if s := unplacedSentence(ApplyOptions{Unplaced: many}); !strings.Contains(s, ", +3 more (kb_guide).") || strings.Contains(s, fmt.Sprintf("s%03d", maxSkillNames)) {
		t.Errorf("cap: %s", s)
	}
	if unplacedSentence(ApplyOptions{}) != "" {
		t.Error("nothing to list must give no sentence")
	}
}

func instructionsManifest(skills ...string) Manifest {
	files := []ArtifactFile{{Path: "instructions.md", Content: []byte("Hello.\n")}}
	m := Manifest{Artifacts: []Artifact{{Kind: "instructions", Name: "kb-a", Source: "kb:kb-a", Signed: true, Files: files, ContentHash: hashArtifactFiles(files)}}}
	m.Revision = computeRevision(m.Artifacts)
	return m
}

// A fork with no skill cell gets the sentence in its block; a changed skill set
// rewrites the block on the next Apply and a repeated one writes nothing.
func TestApply_InstructionsNameUnplacedSkills(t *testing.T) {
	registerTestVariant(t, testVariant(t, "fork-ext", testForkExt, "linux", nil))
	p := configurator.Provider("fork-ext")
	base := t.TempDir()
	m := instructionsManifest()
	apply := func(unplaced ...Artifact) AppliedResult {
		res, err := Apply(m, ApplyOptions{Provider: p, BaseDir: base, Lock: Lock{}, Unplaced: unplaced})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	read := func() string {
		b, err := os.ReadFile(filepath.Join(base, "Documents/Cline/Rules/cartographer.md"))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	first := apply(kbSkill("kb-a", "alpha"))
	if body := read(); !strings.Contains(body, "alpha") || !strings.Contains(body, "artifact_read") {
		t.Fatalf("block lacks the sentence:\n%s", body)
	}
	// Same inputs, same lock: nothing is rewritten.
	again, err := Apply(m, ApplyOptions{Provider: p, BaseDir: base, Lock: first.NewLock, Unplaced: []Artifact{kbSkill("kb-a", "alpha")}})
	if err != nil || again.NewLock.PathsSectionHash != first.NewLock.PathsSectionHash {
		t.Fatalf("a repeated apply moved the hash: %v", err)
	}
	// The skill set changed: the block follows.
	second, err := Apply(m, ApplyOptions{Provider: p, BaseDir: base, Lock: first.NewLock, Unplaced: []Artifact{kbSkill("kb-a", "alpha"), kbSkill("kb-a", "beta")}})
	if err != nil || second.NewLock.PathsSectionHash == first.NewLock.PathsSectionHash || !strings.Contains(read(), "alpha, beta") {
		t.Fatalf("a changed skill set did not rewrite the block (%v):\n%s", err, read())
	}
}

// A built-in has a skill cell: no sentence, and its recorded hash is the one it
// had before the sentence existed (the sentence folds in only when present).
func TestApply_NoUnplacedSentenceWhenTheScopeHasASkillCell(t *testing.T) {
	base := t.TempDir()
	m := instructionsManifest()
	if _, err := Apply(m, ApplyOptions{Provider: configurator.ProviderClaudeCode, BaseDir: base, Lock: Lock{}, Unplaced: UnplacedArtifacts(Manifest{Artifacts: []Artifact{kbSkill("kb-a", "alpha")}}, configurator.ProviderClaudeCode, ScopeGlobal)}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(base, ".claude/CLAUDE.md"))
	if strings.Contains(string(b), "not installed as files") {
		t.Errorf("sentence on a client with a skill cell:\n%s", b)
	}
}
