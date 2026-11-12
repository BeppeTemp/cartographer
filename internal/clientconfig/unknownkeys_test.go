package clientconfig_test

import (
	"os"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// UnknownKeys names what the client file holds that the table does not know,
// with the nearest known key; the keys of the older layouts, which Load and Save
// handle on their own, are not reported.
func TestUnknownKeys(t *testing.T) {
	data := []byte(`server:
  ulr: http://127.0.0.1:39273/mcp
  name: cartographer
update_chek: false
legacy_mode: fast
placeholders:
  search_root: [~/src]
knowledge_bases:
  work-kb: {access: read}
# older layouts and dropped keys:
server_url: http://127.0.0.1:39273/mcp
clients: {claude: {kbs: [kb-a]}}
kbs: [a]
known_kbs: [a]
`)
	var got []string
	for _, u := range clientconfig.UnknownKeys(data) {
		got = append(got, u.Path+"|"+u.Hint)
	}
	want := "server.ulr|server.url, update_chek|update_check, legacy_mode|, placeholders.search_root|placeholders.search_roots"
	if strings.Join(got, ", ") != want {
		t.Errorf("UnknownKeys = %v, want %s", got, want)
	}
}

// A file the client wrote has no unknown key, and a TOP-LEVEL unknown one is
// kept and written back by Save, as before: reporting it must not drop it. A
// nested one (server.ulr) is not: Save writes the struct fields, so it is gone
// on the next save, and doctor is the only place it shows before that.
func TestUnknownKeyIsKeptBySaveAndSavedFilesHaveNone(t *testing.T) {
	dir := t.TempDir()
	writeConfigFile(t, dir, "server:\n  url: http://127.0.0.1:39273/mcp\n  ulr: http://typo.example.com/mcp\nlegacy_mode: fast\n")
	before, _ := os.ReadFile(clientconfig.Path(dir))
	if got := clientconfig.UnknownKeys(before); len(got) != 2 {
		t.Fatalf("UnknownKeys before Save = %+v, want legacy_mode and server.ulr", got)
	}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(clientconfig.Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "legacy_mode: fast") {
		t.Fatalf("Save dropped the unknown key:\n%s", raw)
	}
	if strings.Contains(string(raw), "ulr") {
		t.Errorf("a nested unknown key survived Save; the docs say it is dropped:\n%s", raw)
	}
	if got := clientconfig.UnknownKeys(raw); len(got) != 1 || got[0].Path != "legacy_mode" {
		t.Errorf("UnknownKeys = %+v, want only legacy_mode", got)
	}

	fresh := t.TempDir()
	if err := clientconfig.Save(fresh, clientconfig.Default()); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(clientconfig.Path(fresh))
	if got := clientconfig.UnknownKeys(raw); len(got) != 0 {
		t.Errorf("a saved default file has unknown keys: %+v\n%s", got, raw)
	}
}
