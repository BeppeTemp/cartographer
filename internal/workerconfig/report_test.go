package workerconfig

import (
	"strings"
	"testing"
)

func TestScrub(t *testing.T) {
	t.Parallel()
	_, out, kbs, err := Scrub([]Entry{{Key: "token_env", Value: "WORKER_TOKEN (set)"}}, "kbs: [kb-a, work-kb]\ntoken_env: WORKER_TOKEN\nmodels:\n  - api_key_env: sk-hunter2\n")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "sk-hunter2") || !strings.Contains(out, "WORKER_TOKEN") || len(kbs) != 2 {
		t.Fatalf("out = %q kbs = %v", out, kbs)
	}
	entries, _, _, _ := Scrub([]Entry{{Key: "token_env", Value: "WORKER_TOKEN (set)"}, {Key: "x_env", Value: "a b"}}, "kbs: []\n")
	if entries[0].Value != "WORKER_TOKEN (set)" || entries[1].Value != Redacted {
		t.Fatalf("entries = %+v", entries)
	}
}

func TestTemplateEqualsSchemaFormat(t *testing.T) {
	t.Parallel()
	entries, text := Template()
	want, err := Schema.Format(nil)
	if err != nil || text != string(want) || len(entries) != len(Schema.Keys) {
		t.Fatalf("template mismatch: %v", err)
	}
	for _, e := range entries {
		if e.Origin != "default" {
			t.Errorf("%s origin %q", e.Key, e.Origin)
		}
	}
}

func TestScrubFailsClosed(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"unknown key":      "kbs: [kb-a]\ntoken: sk-live-1\n",
		"password":         "kbs: [kb-a]\npassword: hunter2\n",
		"nested unknown":   "kbs: [kb-a]\nmodels:\n  - name: c\n    api_key: s3cret\n",
		"unknown sequence": "kbs: [kb-a]\nextra:\n  - s3cret\n",
		"env sequence":     "kbs: [kb-a]\nmodels:\n  - api_key_env: [a b]\n",
		"upper url":        "kbs: [kb-a]\nSERVER_URL: https://u:pw@example.com/x\n",
		"userinfo":         "kbs: [kb-a]\nserver_url: https://u:pw@example.com/mcp\n",
		"schemeless":       "kbs: [kb-a]\nserver_url: u:pw@example.com/x\n",
		"query":            "kbs: [kb-a]\nserver_url: https://example.com/mcp?token=s3cret\n",
		"fragment":         "kbs: [kb-a]\nserver_url: https://example.com/mcp#s3cret\n",
		"unparsable":       "kbs: [kb-a]\nserver_url: \"http://[::1\"\n",
		"url in list":      "kbs: [kb-a]\nmodels:\n  - base_url: https://u:pw@example.com\n",
		"commented":        "kbs: [kb-a]\n# password: s3cret\n",
	}
	for name, in := range cases {
		_, out, _, err := Scrub(nil, in)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !strings.Contains(out, Redacted) || strings.Contains(out, "s3cret") || strings.Contains(out, "sk-live") || strings.Contains(out, "hunter2") || strings.Contains(out, "pw@") {
			t.Errorf("%s: leaked: %q", name, out)
		}
	}
	// Entries: unknown key, nested unknown key, bare token, list of env names.
	entries, _, _, _ := Scrub([]Entry{
		{Key: "token", Value: "sk-live-1", Default: "sk-live-1"},
		{Key: "models", Value: []any{map[string]any{"name": "c", "api_key": "s3cret", "api_key_env": "a b"}}},
		{Key: "kbs", Value: []any{"kb-a"}},
		{Key: "server_url", Value: "https://example.com/mcp?x=s3cret"},
	}, "kbs: []\n")
	if entries[0].Value != Redacted || entries[0].Default != Redacted {
		t.Errorf("unknown entry = %+v", entries[0])
	}
	m := entries[1].Value.([]any)[0].(map[string]any)
	if m["api_key"] != Redacted || m["api_key_env"] != Redacted || m["name"] != "c" {
		t.Errorf("nested = %v", m)
	}
	if entries[2].Value.([]any)[0] != "kb-a" || entries[3].Value != Redacted {
		t.Errorf("entries = %+v", entries[2:])
	}
}

func TestScrubKeepsWhatIsSafe(t *testing.T) {
	t.Parallel()
	in := "kbs:\n  - kb-a\nserver_url: https://example.com/mcp\nschedule:\n  poll_seconds: 60\nmodels:\n  - name: c\n    api_key_env: CLOUD_KEY\n    budget:\n      daily_tokens: 100\n"
	_, out, _, err := Scrub(nil, in)
	if err != nil || strings.Contains(out, Redacted) {
		t.Fatalf("err %v out %q", err, out)
	}
}

func TestScrubRefusesMergeKeysAliasesAndDocuments(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"merge": "a: &a {k: v}\nb:\n  <<: *a\n",
		"alias": "a: &a v\nb: *a\n",
		"two":   "kbs: []\n---\nkbs: []\n",
		"bad":   "a: [b",
		"empty": "",
	} {
		if _, _, _, err := Scrub(nil, in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
