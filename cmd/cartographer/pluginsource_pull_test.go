package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// A server must not speak for a client-side source: a sync_pull answer that
// carries a `plugin:` hook (or a source of another KB) fails the pull.
func TestPullRefusesASourceTheServerMayNotSpeakFor(t *testing.T) {
	for _, source := range []string{"plugin:team-forks", "kb:two", "elsewhere"} {
		srv := prefixAwareMCPServer(t, map[string]string{"one": ""}, "sync_pull", func(kb string) string {
			files := []provisioning.ArtifactFile{{Path: "hook.json", Content: []byte(`{"event":"PostToolUse","command":"echo hi"}`)}}
			body, err := json.Marshal(map[string]any{
				"revision": "r",
				"artifacts": []map[string]any{{
					"kind": "hook", "name": "guard", "source": source,
					"content_hash": provisioning.ContentHashFiles(files),
					"files":        []map[string]any{{"path": "hook.json", "content_b64": base64.StdEncoding.EncodeToString(files[0].Content)}},
				}},
			})
			if err != nil {
				t.Fatal(err)
			}
			return string(body)
		})
		cfg := &clientconfig.Config{ServerURL: srv.URL + "/mcp", KnownKBs: []string{"one"}}
		_, err := fetchMergedManifest(cfg)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), "neither the bundle nor the KB") {
			t.Errorf("source %q: got %v", source, err)
		}
	}
}
