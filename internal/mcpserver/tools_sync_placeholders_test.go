package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// placeholderKB mounts a KB named "docs" with a visible and a hidden concept
// and one skill, each citing its own placeholder key (D262).
func placeholderKB(t *testing.T) (*Server, *kb.KB, string) {
	t.Helper()
	k := setupTestKB(t)
	k.AuthName = "docs"
	for _, m := range []string{"ops", "hidden"} {
		if err := k.CreateMap(m, m, "map", nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(k.DataRoot(), "ops", "tools.md"), "---\ntype: Note\ntitle: Tools\n---\nKubeconfig at {{path:kubeconfig}}; syntax {{\\repo:x}} and {{repo:<name>}}.\n")
	write(filepath.Join(k.DataRoot(), "hidden", "secret.md"), "---\ntype: Note\ntitle: Secret\n---\nClone {{repo:private-infra}}.\n")
	write(filepath.Join(k.Root, "skills", "deploy", "SKILL.md"), "---\nname: deploy\ndescription: deploy\n---\nRun from {{repo:deployer}}.\n")
	s := New("test")
	RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}})
	return s, k, filepath.Join(k.DataRoot(), "ops", "tools.md")
}

type pullPlaceholders struct {
	Revision     string   `json:"revision"`
	Placeholders []string `json:"placeholders"`
}

// pullAs calls sync_pull as an admin through the dispatcher, or runs its
// handler directly under a narrowed principal. The dispatcher refuses
// sync_pull to a principal without whole-KB access, so the handler's own
// visibility projection is defense in depth — and it is what this checks.
func pullAs(t *testing.T, s *Server, k *kb.KB, admin bool) pullPlaceholders {
	t.Helper()
	var text string
	if admin {
		var isErr bool
		text, isErr = callJSON(t, s, adminCtx, "sync_pull", `{}`)
		if isErr {
			t.Fatalf("sync_pull: %s", text)
		}
	} else {
		res, err := toolSyncPull(k, fstest.MapFS{}, "", false, nil, nil).Handler(narrowCtx, json.RawMessage(`{}`))
		if err != nil || res.IsError {
			t.Fatalf("sync_pull handler: %v %+v", err, res)
		}
		text = res.Content[0].Text
	}
	var out pullPlaceholders
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("sync_pull: not JSON: %s", text)
	}
	return out
}

func TestSyncPullListsPlaceholders(t *testing.T) {
	s, k, conceptPath := placeholderKB(t)

	admin := pullAs(t, s, k, true)
	if want := []string{"path:kubeconfig", "repo:deployer", "repo:private-infra"}; !reflect.DeepEqual(admin.Placeholders, want) {
		t.Errorf("admin placeholders = %v, want %v", admin.Placeholders, want)
	}

	// A key only a hidden concept cites is not listed for a narrowed
	// principal: the list must not leak what it cannot read.
	narrow := pullAs(t, s, k, false)
	if want := []string{"path:kubeconfig", "repo:deployer"}; !reflect.DeepEqual(narrow.Placeholders, want) {
		t.Errorf("narrowed placeholders = %v, want %v", narrow.Placeholders, want)
	}

	// The list is outside revision: a concept citing a new key is not a
	// catalogue change.
	if err := os.WriteFile(conceptPath, []byte("---\ntype: Note\ntitle: Tools\n---\nNow {{path:elsewhere}}.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after := pullAs(t, s, k, true)
	if after.Revision != admin.Revision {
		t.Errorf("revision changed with a concept's placeholder: %s -> %s", admin.Revision, after.Revision)
	}
	if want := []string{"path:elsewhere", "repo:deployer", "repo:private-infra"}; !reflect.DeepEqual(after.Placeholders, want) {
		t.Errorf("after edit placeholders = %v, want %v", after.Placeholders, want)
	}
}

func TestSyncPullOmitsPlaceholdersWhenNone(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}})
	text, isErr := callJSON(t, s, adminCtx, "sync_pull", `{}`)
	if isErr {
		t.Fatal(text)
	}
	if _, present := decodeJSON(t, text)["placeholders"]; present {
		t.Errorf("placeholders must be omitted when empty: %s", text)
	}
}
