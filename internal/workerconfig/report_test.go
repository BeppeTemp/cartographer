package workerconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

func e(key string, v any) Entry { return Entry{Key: key, Value: v, Origin: "file"} }

func TestScrubKeepsWhatMatchesItsType(t *testing.T) {
	t.Parallel()
	got := Scrub([]Entry{
		e("name", "team-a"),
		e("token_env", "WORKER_TOKEN (set)"),
		e("server_url", "https://example.com/mcp"),
		e("allow_insecure_http", true),
		e("schedule.poll_seconds", float64(60)),
		e("schedule.window.start", "22:30"),
		{Key: "models[].tier", Value: "large", Origin: "default"},
	})
	if len(got) != 7 {
		t.Fatalf("got %+v", got)
	}
	for _, x := range got {
		if x.Value == Redacted {
			t.Errorf("%s redacted", x.Key)
		}
	}
	row, _ := Schema.Lookup("token_env")
	if got[1].Description != row.Description || got[1].Default != row.Default || got[1].Type != row.Type {
		t.Errorf("row not the schema's: %+v", got[1])
	}
}

// Every probe of the review rounds, as an entry; none may leave a trace of S3CRET.
func TestScrubLeavesNoTrace(t *testing.T) {
	t.Parallel()
	probes := []Entry{
		e("token", "S3CRET"),                                    // unknown key
		e("password", "S3CRET"),                                 // unknown key
		e("S3CRET", "x"),                                        // the key is the secret
		e("models.api_key", "S3CRET"),                           // dotted unknown key
		e("models[].api_key", "S3CRET"),                         // list sub-key not in the table
		e("models", map[string]any{"S3CRET": "x"}),              // map value, secret key
		e("models", []any{map[string]any{"api_key": "S3CRET"}}), // list of maps
		e("kbs", []any{"S3CRET"}),                               // list key, any value
		e("kbs[]", "S3CRET"),                                    // scalar under a list key
		e("kbs", "S3CRET"),                                      // scalar where the table has a list
		e("schedule.poll_seconds", "S3CRET"),                    // string under an int key
		e("schedule.poll_seconds", float64(-1)),                 // out of range
		e("schedule.poll_seconds", 1.5),                         // not whole
		e("allow_insecure_http", "S3CRET"),                      // string under a bool key
		e("name", float64(7)),                                   // number under a string key
		e("name", map[string]any{"a": "S3CRET"}),                // map under a string key
		e("token_env", "sk-S3CRET"),                             // not a variable name
		e("token_env", []any{"S3CRET x"}),                       // list under an env key
		e("models[].api_key_env", "a S3CRET"),
		e("server_url", "https://u:S3CRET@example.com/mcp"),
		e("server_url", "u:S3CRET@example.com/x"),
		e("server_url", "https://example.com/mcp?token=S3CRET"),
		e("server_url", "https://example.com/mcp#S3CRET"),
		e("server_url", "http://[::1"),
		e("server_url", "S3CRET"),
		e("server_url", "ftp://example.com/S3CRET"),
		e("server_url", "https:///S3CRET"),
		e("models[].base_url", "https://u:S3CRET@example.com"),
		e("models[].provider", "S3CRET"), // outside the enum
		e("models[].tier", "S3CRET"),
		e("schedule.window.start", "S3CRET"), // not a clock
		e("schedule.window.timezone", "S3CRET sk=1"),
		e("tasks[].kind", "S3CRET sk=1"),
		{Key: "name", Value: "team-a", Origin: "S3CRET"}, // unknown origin
		{Key: "name", Value: "team-a", Origin: "S3CRET", Description: "S3CRET", Default: "S3CRET", Type: "S3CRET", Values: "S3CRET"},
		{Key: "name", Value: "S3CRET-ok", Origin: "file", Description: "S3CRET", Default: "S3CRET", Type: "S3CRET", Values: "S3CRET"}, // fields the sender controls
	}
	out, _ := json.Marshal(Scrub(probes))
	if strings.Contains(string(out), "S3CRET") {
		t.Fatalf("trace of the secret: %s", out)
	}
}

func TestScrubRedactsUnvouchedStrings(t *testing.T) {
	t.Parallel()
	got := Scrub([]Entry{e("models[].provider", "gemini"), e("server_url", "https://u:pw@example.com/"), e("models[].model", "sk live"), e("name", "Team A"), e("models[].base_url", "sk-bare-key")})
	if len(got) != 5 {
		t.Fatalf("got %+v", got)
	}
	for _, x := range got {
		if x.Value != Redacted {
			t.Errorf("%s = %v", x.Key, x.Value)
		}
	}
}

func TestScrubDropsRepeatedKeys(t *testing.T) {
	t.Parallel()
	got := Scrub([]Entry{e("name", "team-a"), e("name", "team-b")})
	if len(got) != 1 || got[0].Value != "team-a" {
		t.Fatalf("got %+v", got)
	}
}

func TestTemplateIsTheScalarKeysAtTheirDefaults(t *testing.T) {
	t.Parallel()
	for _, x := range Template() {
		k, ok := Schema.Lookup(x.Key)
		if !ok || k.Type == "list" || x.Origin != "default" || x.Default != k.Default {
			t.Errorf("%+v", x)
		}
	}
}
