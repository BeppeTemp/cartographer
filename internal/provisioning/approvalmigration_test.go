package provisioning

import "testing"

func TestInstalledBeforeApprovals(t *testing.T) {
	hook := func(hash string) Artifact {
		return Artifact{Kind: "hook", Name: "guard", Source: "kb:kb-a", ContentHash: hash, Files: []ArtifactFile{{Path: "hook.json", Content: []byte("{}")}}}
	}
	lock := func(hash, source string) Lock {
		return Lock{Managed: []ManagedFile{{Kind: "hook", Name: "guard", ContentHash: hash, Source: source}}}
	}
	approved := map[string]string{"kb:kb-a\x00hook/guard": "whatever"}
	cases := []struct {
		name string
		a    Artifact
		lock Lock
		opts ApplyOptions
		want bool
	}{
		{"same hash", hook("h"), lock("h", "kb:kb-a"), ApplyOptions{}, true},
		{"hash differs", hook("new"), lock("old", "kb:kb-a"), ApplyOptions{}, false},
		{"not in the lock", hook("h"), Lock{}, ApplyOptions{}, false},
		{"other source", hook("h"), lock("h", "kb:kb-b"), ApplyOptions{}, false},
		{"an approval is recorded, stale or not", hook("h"), lock("h", "kb:kb-a"), ApplyOptions{ApprovedMCP: approved}, false},
		{"mcp is never migrated", Artifact{Kind: "mcp", Name: "guard", Source: "kb:kb-a", ContentHash: "h"}, Lock{Managed: []ManagedFile{{Kind: "mcp", Name: "guard", ContentHash: "h", Source: "kb:kb-a"}}}, ApplyOptions{}, false},
		{"plain skill needs no approval", Artifact{Kind: "skill", Name: "guard", Source: "kb:kb-a", ContentHash: "h", Files: []ArtifactFile{{Path: "SKILL.md", Content: []byte("---\nname: guard\n---\n")}}}, Lock{Managed: []ManagedFile{{Kind: "skill", Name: "guard", ContentHash: "h", Source: "kb:kb-a"}}}, ApplyOptions{}, false},
	}
	for _, tc := range cases {
		if got := InstalledBeforeApprovals(tc.a, tc.lock, tc.opts); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
