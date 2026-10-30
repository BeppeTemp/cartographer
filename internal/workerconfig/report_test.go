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
	entries, _, _, _ := Scrub([]Entry{{Key: "token_env", Value: "WORKER_TOKEN (set)", Origin: "file"}, {Key: "models", Value: "a b", Origin: "file"}, {Key: "name", Origin: ""}}, "kbs: []\n")
	if len(entries) != 2 || entries[0].Value != "WORKER_TOKEN (set)" || entries[1].Value != "a b" {
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
		// A key the table does not know is dropped; a known one is masked.
		dropped := map[string]bool{"unknown key": true, "password": true, "nested unknown": true, "unknown sequence": true, "upper url": true}
		if (!dropped[name] && !strings.Contains(out, Redacted)) || strings.Contains(out, "s3cret") || strings.Contains(out, "sk-live") || strings.Contains(out, "hunter2") || strings.Contains(out, "pw@") {
			t.Errorf("%s: leaked: %q", name, out)
		}
	}
	// Entries: the schema's row plus the sender's value and origin.
	entries, _, _, _ := Scrub([]Entry{
		{Key: "token", Value: "sk-live-1", Origin: "file"},
		{Key: "sk-live-key", Value: "x", Origin: "file"},
		{Key: "models", Value: []any{map[string]any{"name": "c", "api_key": "s3cret", "api_key_env": "a b"}}, Origin: "file",
			Description: "sk-in-description", Default: "sk-in-default", Type: "sk-in-type", Values: "sk-in-values"},
		{Key: "kbs", Value: []any{"kb-a"}, Origin: "default"},
		{Key: "server_url", Value: "https://example.com/mcp?x=s3cret", Origin: "file"},
		{Key: "name", Value: "team-a", Origin: "sk-in-origin"},
	}, "kbs: []\n")
	if len(entries) != 3 {
		t.Fatalf("entries = %+v", entries)
	}
	m := entries[0].Value.([]any)[0].(map[string]any)
	row, _ := Schema.Lookup("models")
	if m["api_key"] != Redacted || m["api_key_env"] != Redacted || m["name"] != "c" ||
		entries[0].Description != row.Description || entries[0].Default != row.Default || entries[0].Type != row.Type || entries[0].Values != row.Values {
		t.Errorf("models = %+v", entries[0])
	}
	if entries[1].Value.([]any)[0] != "kb-a" || entries[2].Value != Redacted {
		t.Errorf("entries = %+v", entries[1:])
	}
}

func TestScrubDropsUnknownAndNonScalarKeys(t *testing.T) {
	t.Parallel()
	for name, in := range map[string]string{
		"empty value":    "kbs: [kb-a]\nsk-SECRET: \"\"\n",
		"mapping value":  "kbs: [kb-a]\nsk-SECRET:\n  a: b\n",
		"nested unknown": "kbs: [kb-a]\nmodels:\n  - name: c\n    sk-SECRET: \"\"\n",
		"block key":      "kbs: [kb-a]\n? |\n  sk-SECRET\n: x\n",
	} {
		_, out, _, err := Scrub(nil, in)
		if err != nil || strings.Contains(out, "SECRET") || !strings.Contains(out, "kb-a") {
			t.Errorf("%s: err %v out %q", name, err, out)
		}
	}
	for name, in := range map[string]string{
		"mapping key":  "kbs: [kb-a]\n? {token: sk-X}\n: x\n",
		"sequence key": "kbs: [kb-a]\n? [sk-X]\n: x\n",
	} {
		if _, _, _, err := Scrub(nil, in); err == nil {
			t.Errorf("%s accepted", name)
		}
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
