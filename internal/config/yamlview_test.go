package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func yamlView() View {
	return View{
		Server: []Entry{
			{Key: "http", Value: "127.0.0.1:39273", Origin: OriginFlag, Flag: "--http"},
			{Key: "auth.mode", Value: "on", Origin: OriginEnv, Env: "CARTOGRAPHER_AUTH"},
			{Key: "auth.tokens", Value: SecretSet, Origin: OriginFile, Secret: true},
			{Key: "credentials_dir", Value: "", Origin: OriginDefault},
			{Key: "data", Value: "/etc/data", Origin: OriginFile},
			{Key: "kbs", Value: []string{"kb-a", "work-kb"}, Origin: OriginFile},
		},
		KBs: []KBEntries{
			{Name: "kb-a", Entries: []Entry{
				{Key: "kbs[].remote", Value: "https://git.example.com/user/kb-a.git", Origin: OriginFile},
				{Key: "kbs[].usage_stale_days", Value: 30, Origin: OriginKB},
				{Key: "kbs[].branch", Value: "x", Origin: OriginDefault},
			}},
			{Name: "work-kb", Entries: []Entry{
				{Key: "kbs[].branch", Value: "x", Origin: OriginDefault},
			}},
		},
	}
}

func TestServerYAMLIsTheEffectiveFileDocumented(t *testing.T) {
	out, err := yamlView().ServerYAML()
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{
		"http: 127.0.0.1:39273 # from --http",
		"mode: \"on\" # from CARTOGRAPHER_AUTH",
		"tokens: " + Redacted,
		"data: /etc/data",
		// A key at its default is documented, commented out, not set.
		"# credentials_dir: /etc/cartographer/credentials",
		// The server file's kbs[] entry, not what the KB's own file sets.
		"remote: https://git.example.com/user/kb-a.git",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("server YAML lacks %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"usage_stale_days: 30", "work-kb"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("server YAML carries %q:\n%s", unwanted, text)
		}
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatalf("server YAML does not parse: %v", err)
	}
}

func TestKBYAMLIsTheKBsSettingsFile(t *testing.T) {
	out, ok, err := yamlView().KBYAML("kb-a")
	if err != nil || !ok {
		t.Fatalf("KBYAML: ok=%v err=%v", ok, err)
	}
	text := string(out)
	for _, want := range []string{
		"usage_stale_days: 30",
		"remote: https://git.example.com/user/kb-a.git # from the server's kbs[] entry",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("KB YAML lacks %q:\n%s", want, text)
		}
	}

	// Nothing set: only the documented block, not an empty "{}" mapping.
	empty, ok, err := yamlView().KBYAML("work-kb")
	if err != nil || !ok {
		t.Fatalf("KBYAML: ok=%v err=%v", ok, err)
	}
	if strings.HasPrefix(string(empty), "{}") || !strings.Contains(string(empty), "# --- available settings: uncomment a key to set it ---") {
		t.Errorf("empty KB YAML:\n%s", empty)
	}

	if _, ok, _ := yamlView().KBYAML("missing"); ok {
		t.Error("KBYAML of an unknown KB reported ok")
	}
}
