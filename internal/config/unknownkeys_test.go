package config

import (
	"strings"
	"testing"
)

// A key no table knows is a warning at load, not an error: a key a newer
// release adds must not stop an older server. The line names the key, where it
// is and the nearest known key; the keys that are right say nothing.
func TestLoadWarnsOfUnknownKeys(t *testing.T) {
	path := writeCfg(t, `http: ":39273"
credentials_dri: /creds
web:
  enabld: true
api:
  enabled: true
  cors_origns: []
kbs:
  - remote: https://git.example.com/user/kb-a.git
    brach: main
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("an unknown key is a warning, not a load error: %v", err)
	}
	got := strings.Join(cfg.Warnings, "\n")
	for _, want := range []string{
		"credentials_dri: unknown key, ignored (line 2); did you mean credentials_dir?",
		"web.enabld: unknown key, ignored (line 4); did you mean web.enabled?",
		"api.cors_origns: unknown key, ignored (line 7); did you mean api.cors_origins?",
		"kbs[0].brach: unknown key, ignored (line 10); did you mean kbs[0].branch?",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("warnings lack %q:\n%s", want, got)
		}
	}
	if n := len(cfg.Warnings); n != 4 {
		t.Errorf("%d warnings, want 4 (the right keys must say nothing):\n%s", n, got)
	}
	if cfg.HTTP != ":39273" || !cfg.API.Enabled {
		t.Errorf("the keys that are right were lost: %+v", cfg)
	}
}

// What the server accepts is not unknown: a removed key (its own message), a
// moved one, the sections whose entries are checked strictly, and every key the
// table describes. TestSchemaMatchesStructs keeps the table whole.
func TestUnknownKeysSilentForKnownAndRetired(t *testing.T) {
	data := []byte(`http: ":39273"
git:
  identity: Ada Example <ada@example.com>
  profile: server
kb_aliases: []
auth:
  mode: on
  tokens: [x]
hooks: []
components: []
plugins: {}
maintenance: {auto_repair: false}
`)
	if got := UnknownKeys(data); len(got) != 0 {
		t.Errorf("UnknownKeys = %+v, want none", got)
	}
	if got := UnknownKeys([]byte("htpp: x\n")); len(got) != 1 || got[0].Hint != "http" {
		t.Errorf("UnknownKeys of a typo = %+v, want one with the hint http", got)
	}
}

// Keys older releases read and the code no longer parses are retired, not
// unknown: no typo warning, and the realignment names and drops each (#2000).
func TestRemovedKeysOfOlderReleasesAreRetired(t *testing.T) {
	data := []byte(`http: ":39273"
search:
  ollama_url: http://ollama:11434
  ollama_model: nomic-embed-text
peers:
  enabled: true
mcp:
  mount_mode: per-kb
  tool_prefix_mode: kb-name
kbs:
  - remote: https://git.example.com/user/kb-a.git
    tool_prefix: old
`)
	if got := UnknownKeys(data); len(got) != 0 {
		t.Errorf("UnknownKeys = %+v, want none: the removed keys are retired", got)
	}
	out, rep, err := Schema.Realign(data, Retired)
	if err != nil {
		t.Fatal(err)
	}
	dropped := map[string]bool{}
	for _, c := range rep.Changes {
		if c.Kind == "removed" {
			dropped[c.Path] = true
		}
	}
	for _, p := range []string{"search", "peers", "mcp.mount_mode", "mcp.tool_prefix_mode", "kbs[0].tool_prefix"} {
		if !dropped[p] {
			t.Errorf("the realignment did not drop %s: %+v", p, rep.Changes)
		}
	}
	for _, s := range []string{"ollama", "peers", "mount_mode", "tool_prefix"} {
		if strings.Contains(string(out), s) {
			t.Errorf("the realigned file still holds %q:\n%s", s, out)
		}
	}
}
