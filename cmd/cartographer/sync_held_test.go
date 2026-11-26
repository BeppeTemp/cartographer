package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// regionPayload is what a KB serves for the held tests: a skill and an
// instructions artifact whose text is rule.
func regionPayload(t *testing.T, kb, rule string) string {
	t.Helper()
	instr := []contract.ArtifactFile{{Path: "instructions.md", Content: []byte(rule + "\n")}}
	skill := []contract.ArtifactFile{{Path: "SKILL.md", Content: []byte("# skill-" + kb)}}
	return pulledHookPayload(t, kb,
		contract.Artifact{Kind: "skill", Name: "skill-" + kb, Source: "kb:" + kb, ContentHash: provisioning.ContentHashFiles(skill), Files: skill},
		contract.Artifact{Kind: "instructions", Name: kb, Source: "kb:" + kb, ContentHash: provisioning.ContentHashFiles(instr), Files: instr},
	)
}

func regionServer(t *testing.T, rules map[string]string) *followServer {
	t.Helper()
	srv := newFollowServer(t, "one", "two")
	srv.payload = func(kb string) string { return regionPayload(t, kb, rules[kb]) }
	return srv
}

func lockSources(t *testing.T, dir string) map[string]int {
	t.Helper()
	lf, err := provisioning.ReadLockFile(filepath.Join(dir, provisioning.LockFileName))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int{}
	for _, l := range lf.Providers {
		for _, mf := range l.Managed {
			out[mf.Source]++
		}
	}
	return out
}

func claudeMD(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, ".claude", "CLAUDE.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// AC-cad-19: a followed KB listed in skipped_kbs keeps its managed files and its
// lock entries after the sync, the provider is not skipped, and the report says
// held.
func TestHeldKBKeepsFilesAndLock(t *testing.T) {
	srv := regionServer(t, map[string]string{"one": "Rule one.", "two": "Rule two."})
	dir := followHome(t, srv, "all", "one", "two")
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	before := lockSources(t, dir)
	if before["kb:two"] == 0 {
		t.Fatalf("no lock entry of kb two: %v", before)
	}
	srv.set(func(f *followServer) { f.kbs, f.skipped = []string{"one"}, []string{"two"} })
	out, errOut, err := followSync(t, dir, syncOptions{})
	if err != nil {
		t.Fatalf("the provider was skipped: %v\n%s", err, errOut)
	}
	if !hasSkill(dir, "two") || !hasSkill(dir, "one") {
		t.Error("a held KB's files were removed")
	}
	if got := lockSources(t, dir); got["kb:two"] != before["kb:two"] || got["kb:one"] != before["kb:one"] {
		t.Errorf("lock entries changed: %v, was %v", got, before)
	}
	if !strings.Contains(out, "held two: the server could not mount it (mount failed: the path moved), its files stay") {
		t.Errorf("no held report:\n%s", out)
	}
	if strings.Contains(out, "two not mounted any more") {
		t.Errorf("a held KB was reported removed:\n%s", out)
	}
	if got := knownKBsOf(t, dir); got != "one,two" {
		t.Errorf("known_kbs = %q: the next sync must still know the held KB", got)
	}
	// Mounted again, it simply continues.
	srv.set(func(f *followServer) { f.kbs, f.skipped = []string{"one", "two"}, nil })
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil || !hasSkill(dir, "two") {
		t.Errorf("after the remount: %v", err)
	}
}

// AC-cad-20: a followed KB in neither kbs nor skipped_kbs is pruned: files,
// instructions region and MCP entry (the contrast with AC-cad-19).
func TestUnmountedKBIsPruned(t *testing.T) {
	srv := regionServer(t, map[string]string{"one": "Rule one.", "two": "Rule two."})
	dir := followHome(t, srv, "all", "one", "two")
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(claudeMD(t, dir), "cartographer:kb:two:begin") {
		t.Fatal("no region of kb two")
	}
	srv.set(func(f *followServer) { f.kbs = []string{"one"} })
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if hasSkill(dir, "two") || hasMCPEntry(t, dir, "cartographer-two") || lockSources(t, dir)["kb:two"] != 0 {
		t.Errorf("not pruned: skill=%v entry=%v lock=%v", hasSkill(dir, "two"), hasMCPEntry(t, dir, "cartographer-two"), lockSources(t, dir))
	}
	if md := claudeMD(t, dir); strings.Contains(md, "cartographer:kb:two") || !strings.Contains(md, "cartographer:kb:one:begin") {
		t.Errorf("the region of the unmounted KB stays or the other left:\n%s", md)
	}
}

// AC-cad-22: through a real sync on a non-routed server, the held KB's region
// stays in the provider's instructions block even when another KB's
// instructions change in the same run, and its per-KB MCP entry stays in the
// client's MCP file.
func TestHeldKBKeepsInstructionsRegionAndMCPEntry(t *testing.T) {
	rules := map[string]string{"one": "Rule one.", "two": "Rule two."}
	srv := regionServer(t, rules)
	dir := followHome(t, srv, "all", "one", "two")
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	region := func(md string) string {
		i := strings.Index(md, "<!-- cartographer:kb:two:begin -->")
		j := strings.Index(md, "<!-- cartographer:kb:two:end -->")
		if i < 0 || j < i {
			t.Fatalf("no region of kb two:\n%s", md)
		}
		return md[i : j+len("<!-- cartographer:kb:two:end -->")]
	}
	want := region(claudeMD(t, dir))

	// kb one's instructions change in the same run kb two is down.
	srv.set(func(f *followServer) {
		rules["one"] = "Rule one, changed."
		f.kbs, f.skipped = []string{"one"}, []string{"two"}
	})
	if _, errOut, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatalf("%v\n%s", err, errOut)
	}
	md := claudeMD(t, dir)
	if !strings.Contains(md, "Rule one, changed.") {
		t.Fatalf("the block was not rewritten for kb one:\n%s", md)
	}
	if got := region(md); got != want {
		t.Errorf("the held region changed:\n%q\nwas:\n%q", got, want)
	}
	if !hasMCPEntry(t, dir, "cartographer-two") {
		t.Error("the held KB's per-KB MCP entry was removed")
	}
	// And the file converges: a second run writes nothing.
	before := md
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	if claudeMD(t, dir) != before {
		t.Error("a second sync rewrote the block")
	}
	if lockSources(t, dir)["kb:two"] == 0 {
		t.Error("the held KB's lock entries were dropped")
	}
}

// The names a sync holds come from one function, and the manifest it pulls never
// holds one of them.
func TestHeldKBsIsOneFunction(t *testing.T) {
	skipped := []client.SkippedKB{{Name: "kb-b", Reason: "r1"}, {Name: "kb-x", Reason: "r2"}, {Name: "kb-z", Reason: "r3"}}
	got := heldKBs([]string{"kb-a", "kb-b", "kb-c"}, []string{"kb-a"}, skipped, []string{"kb-z", "kb-a"})
	var names []string
	for _, h := range got {
		names = append(names, h.Name+":"+h.Reason)
	}
	// kb-b (previous, absent, skipped) and kb-z (also, absent, skipped); kb-c is
	// gone (not skipped), kb-x was never followed, kb-a is mounted.
	if strings.Join(names, ",") != "kb-b:r1,kb-z:r3" {
		t.Errorf("held = %v", names)
	}
	if len(heldKBs([]string{"kb-b"}, nil, nil, nil)) != 0 {
		t.Error("without skipped_kbs nothing is held")
	}
}
