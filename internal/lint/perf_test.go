package lint

import (
	"fmt"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// The counters below pin what a lint run recomputes (the read-path performance
// plan, WP12 and WP13): each is constant per run, never per entry or per map.
// These tests swap package state, so they are not parallel.

// A run loads the skills once, and the built-in value families are folded once
// per process however many contracts and entries ask for them.
func TestPerf_RunLoadsSkillsOnce(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.Root, "skills/a/SKILL.md", skillMD("a", "A skill", "# A\n"))
	writeFile(t, k.DataRoot(), "m/_map.md", "---\ntype: Map\ntitle: M\n---\n")
	for i := 0; i < 5; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("m/c%d.md", i), "---\ntype: Note\ntitle: C\nstatus: open\n---\nbody\n")
	}
	loadSkillsCalls.Store(0)
	if _, err := Run(k, "", false); err != nil {
		t.Fatal(err)
	}
	if got := loadSkillsCalls.Load(); got != 1 {
		t.Fatalf("loadSkills ran %d times in one run, want 1", got)
	}
	if got := builtinBuilds.Load(); got != 1 {
		t.Fatalf("built-in families folded %d times, want 1", got)
	}
}

// A contract without synonyms shares the built-in index; one with synonyms
// extends a copy and leaves the shared index untouched.
func TestPerf_FamiliesForOverlayDoesNotLeak(t *testing.T) {
	base := familiesFor(nil)
	n := len(base)
	c := &kb.MapContract{ValueSynonyms: map[string][]string{"shipped": {"released"}}}
	if _, ok := familiesFor(c).member("released"); !ok {
		t.Fatal("contract synonym not indexed")
	}
	if _, ok := familiesFor(nil).member("released"); ok || len(familiesFor(nil)) != n {
		t.Fatal("the overlay leaked into the shared index")
	}
}

// artifact_unused reads the history once per HEAD: a second run starts no
// `git log`, a new commit invalidates the answer.
func TestPerf_ArtifactBirthsAreKeyedByHEAD(t *testing.T) {
	var runs int
	orig := logNameStatus
	logNameStatus = func(dir string, since time.Time) ([]gitx.CommitChanges, error) {
		runs++
		return orig(dir, since)
	}
	t.Cleanup(func() { logNameStatus = orig })

	k := tempKB(t)
	writeFile(t, k.Root, "skills/idle/SKILL.md", skillMD("idle", "Idle", "# I\n"))
	commitAt(t, k, agoDays(200), "add skill")
	births := func() map[string]time.Time { return artifactBirths(k) }

	first := births()
	if _, ok := first["skills/idle/SKILL.md"]; !ok || runs != 1 {
		t.Fatalf("first run: births %v, git log runs %d", first, runs)
	}
	if again := births(); runs != 1 || len(again) != len(first) {
		t.Fatalf("second run at the same HEAD: git log runs %d, want 1", runs)
	}
	writeFile(t, k.Root, "agents/new.md", "# new\n")
	commitAt(t, k, agoDays(1), "add agent")
	if got := births(); runs != 2 {
		t.Fatalf("after a commit: git log runs %d, want 2", runs)
	} else if _, ok := got["agents/new.md"]; !ok {
		t.Fatalf("the new commit is not in the births: %v", got)
	}
}

// A journal's entries share their title words by construction: none of them is
// compared, and a non-journal map of the same shape still reports its pair.
func TestPerf_DuplicateItemsSkipJournals(t *testing.T) {
	k := tempKB(t)
	writeFile(t, k.DataRoot(), "j/_map.md", "---\ntype: Map\ntitle: J\nkind: journal\n---\n")
	writeFile(t, k.DataRoot(), "n/_map.md", "---\ntype: Map\ntitle: N\n---\n")
	for i := 0; i < 60; i++ {
		writeFile(t, k.DataRoot(), fmt.Sprintf("j/e%d.md", i), fmt.Sprintf("---\ntype: Note\ntitle: Daily standup meeting notes entry %d\n---\n", i))
	}
	writeFile(t, k.DataRoot(), "n/a.md", "---\ntype: Note\ntitle: Backup restore procedure\n---\n")
	writeFile(t, k.DataRoot(), "n/b.md", "---\ntype: Note\ntitle: The restore of a backup procedure\n---\n")

	titlePairsCompared.Store(0)
	dups := itemsOf(review(t, k), ReviewDuplicate)
	if len(dups) != 1 || dups[0].Concepts[0] != "n/a" || dups[0].Concepts[1] != "n/b" {
		t.Fatalf("duplicate items = %+v, want only n/a|n/b", dups)
	}
	if got := titlePairsCompared.Load(); got > 1 {
		t.Fatalf("%d title pairs compared, the journal entries must not be", got)
	}
}
