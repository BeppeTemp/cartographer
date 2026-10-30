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
