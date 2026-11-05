package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// D1712: a client that cannot install a KB's skills as files is told to read
// them with artifact_read. That route must work on an unbound folder (access=read)
// and kb_guide must name the same tool and path shape the instructions block does.
func TestUnplacedSkills_ReadableThroughMCPOnAnUnboundFolder(t *testing.T) {
	t.Parallel()
	multi := NewMultiKBServer("test")
	k := setupNamedTestKB(t, "kb-a")
	dir := filepath.Join(k.Root, "skills", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const body = "---\nname: demo\ndescription: A demo skill.\n---\n# Demo\nFollow these steps.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	multi.MountKB("kb-a", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatal(err)
	}
	handler := routedTokenStore("kb-a").Middleware(multi.Handler())

	res := routedCallAt(t, handler, readOnlyPath, "artifact_read", map[string]any{"kb": "kb-a", "path": "skills/demo/SKILL.md"})
	if res.IsError || !strings.Contains(res.Content[0].Text, "Follow these steps.") {
		t.Fatalf("artifact_read on access=read: %+v", res)
	}
	guide := routedCallAt(t, handler, readOnlyPath, "kb_guide", map[string]any{"kb": "kb-a"})
	if guide.IsError || !strings.Contains(guide.Content[0].Text, provisioning.ArtifactReadShape) {
		t.Fatalf("kb_guide does not name %s: %+v", provisioning.ArtifactReadShape, guide)
	}
}
