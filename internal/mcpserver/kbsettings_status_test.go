package mcpserver

import (
	"encoding/json"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// D746: kb_status says where a gate comes from, and which server value lost.
func TestKBStatus_ReportsKBSettingsAndIgnoredServerValues(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	k.AllowArtifactWrite = true
	k.SetSettingsInfo(kb.SettingsInfo{
		Present:         true,
		FromKB:          []string{"allow_artifact_write"},
		Ignored:         []kb.IgnoredSetting{{Key: "allow_artifact_write", ServerValue: "false", KBValue: "true"}},
		Findings:        []string{"write_gate: must be one of off, error, warning"},
		SecretsDeclared: true,
	})
	res, err := s.Tools()["kb_status"].Handler(authLocalContext(), json.RawMessage(`{}`))
	if err != nil || res.IsError {
		t.Fatalf("kb_status = %+v, %v", res, err)
	}
	var out struct {
		Capabilities map[string]KBCapability `json:"capabilities"`
		Ignored      []kb.IgnoredSetting     `json:"ignored_server_settings"`
		Findings     []string                `json:"settings_findings"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	c := out.Capabilities["artifact_write"]
	if c.State != "enabled" || c.Setting != "settings.yaml allow_artifact_write" || c.IgnoredServerValue != "false" {
		t.Errorf("artifact_write = %+v", c)
	}
	if len(out.Ignored) != 1 || out.Ignored[0].Key != "allow_artifact_write" || len(out.Findings) != 1 {
		t.Errorf("ignored=%+v findings=%v", out.Ignored, out.Findings)
	}
	if got := out.Capabilities["secrets"].State; got != "missing key" {
		t.Errorf("secrets = %q, want missing key", got)
	}

	// A KB with no settings.yaml reports nothing new.
	k.SetSettingsInfo(kb.SettingsInfo{})
	res, _ = s.Tools()["kb_status"].Handler(authLocalContext(), json.RawMessage(`{}`))
	var plain map[string]json.RawMessage
	_ = json.Unmarshal([]byte(res.Content[0].Text), &plain)
	if _, ok := plain["ignored_server_settings"]; ok {
		t.Error("ignored_server_settings present for a KB without settings.yaml")
	}
}
