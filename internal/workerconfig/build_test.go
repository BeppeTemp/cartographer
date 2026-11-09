package workerconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildCarriesNoSecret(t *testing.T) {
	const marker = "MARKER-SECRET-VALUE"
	cfg := `name: docs-night
server_url: https://user:pw-in-url@example.com/mcp?token=` + marker + `
token_env: WORKER_TOKEN
kbs: [kb-a]
models:
  - name: small-a
    provider: anthropic
    api_key_env: MODEL_KEY
    base_url: https://user2:` + marker + `@example.com
schedule:
  idle_minutes: 30
tasks:
  - kind: exec
    command: /usr/bin/tool --password ` + marker + `
`
	env := map[string]string{"WORKER_TOKEN": marker, "MODEL_KEY": marker}
	r, err := Build([]byte(cfg), "docs-night", "docs-night-ab12cd", "1.2.3", []string{"kb-a"}, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(r)
	for _, bad := range []string{marker, "pw-in-url", "--password", "user2"} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("report contains %q: %s", bad, raw)
		}
	}
	got := map[string]any{}
	for _, e := range r.Entries {
		got[e.Key] = e.Value
	}
	if got["token_env"] != "WORKER_TOKEN (set)" {
		t.Errorf("token_env = %v, want the name and (set)", got["token_env"])
	}
	if got["server_url"] != "https://example.com/mcp" {
		t.Errorf("server_url = %v", got["server_url"])
	}
	if got["schedule.idle_minutes"] != float64(30) {
		t.Errorf("idle_minutes = %v (%T)", got["schedule.idle_minutes"], got["schedule.idle_minutes"])
	}
	for _, e := range r.Entries {
		if strings.Contains(e.Key, "[]") {
			t.Errorf("list element key %s reported", e.Key)
		}
	}
}

func TestBuildUnsetEnvAndBadYAML(t *testing.T) {
	r, err := Build([]byte("token_env: T\n"), "p", "w", "1", []string{"kb-a"}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.Entries {
		if e.Key == "token_env" && e.Value != "T (unset)" {
			t.Errorf("token_env = %v", e.Value)
		}
	}
	if _, err := Build([]byte("a: [unclosed"), "p", "w", "1", nil, nil); err == nil {
		t.Error("invalid YAML must be an error")
	}
}
