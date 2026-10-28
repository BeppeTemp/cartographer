package provisioning

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// With an artifact waiting for approval, a second sync of the same manifest
// rewrites nothing: no file's mtime moves and Apply reports no write (#1326).
func TestApply_PendingApprovalSecondRunWritesNothing(t *testing.T) {
	for _, p := range approvalProviders {
		base := t.TempDir()
		m := Manifest{Revision: "r1", Artifacts: []Artifact{
			{Kind: "instructions", Name: "kb-a", Source: "kb:kb-a", ContentHash: "i1",
				Files: []ArtifactFile{{Path: "instructions.md", Content: []byte("Use kb-a.\n")}}},
			{Kind: "skill", Name: "s1", Source: "kb:kb-a", ContentHash: "s1",
				Files: []ArtifactFile{{Path: "SKILL.md", Content: []byte("---\nname: s1\ndescription: d\n---\nbody\n")}}},
			{Kind: "agent", Name: "a1", Source: "kb:kb-a", ContentHash: "a1",
				Files: []ArtifactFile{{Path: "a1.md", Content: []byte("---\nname: a1\ndescription: d\n---\nbody\n")}}},
			{Kind: "agent", Name: "a2", Source: "kb:kb-a", ContentHash: "a2",
				Files: []ArtifactFile{{Path: "a2.md", Content: []byte("---\nname: a2\ndescription: d\nhooks:\n  Stop: []\n---\nbody\n")}}},
			hookArtifact("h1", "#!/bin/sh\nexit 0\n"),
		}}
		opts := ApplyOptions{Provider: p, BaseDir: base, SkipLockWrite: true}
		first, err := Apply(m, opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(first.NeedsApproval) < 1 {
			t.Fatalf("%s: want the hook pending, got %+v", p, first.NeedsApproval)
		}
		old := time.Now().Add(-time.Hour)
		snap := map[string]os.FileInfo{}
		_ = filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err == nil && info.Mode().IsRegular() {
				_ = os.Chtimes(path, old, old)
				snap[path], _ = os.Stat(path)
			}
			return nil
		})
		opts.Lock = first.NewLock
		second, err := Apply(m, opts)
		if err != nil {
			t.Fatal(err)
		}
		if len(second.Written) != 0 {
			t.Errorf("%s: second run reports writes: %+v", p, second.Written)
		}
		for path := range snap {
			info, _ := os.Stat(path)
			if !info.ModTime().Equal(old) {
				t.Errorf("%s: %s rewritten", p, path)
			}
		}
	}
}
