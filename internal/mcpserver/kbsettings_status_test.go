package mcpserver

import (
	"encoding/json"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// D746: kb_status says where a gate comes from, and and the entries of the file that were left out.
func TestKBStatus_ReportsKBSettingsAndFindings(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	k.SetSettingsInfo(kb.SettingsInfo{
		Present:  true,
		FromKB:   []string{"usage_stale_days"},
		Findings: []string{"write_gate: removed setting, ignored"},
	})
	res, err := s.Tools()["kb_status"].Handler(authLocalContext(), json.RawMessage(`{}`))
	if err != nil || res.IsError {
		t.Fatalf("kb_status = %+v, %v", res, err)
	}
	var out struct {
		Capabilities map[string]KBCapability `json:"capabilities"`
		Findings     []string                `json:"settings_findings"`
	}
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if c := out.Capabilities["mount"]; c.State == "" {
		t.Errorf("mount = %+v", c)
	}
	if len(out.Findings) != 1 {
		t.Errorf("findings=%v", out.Findings)
	}

	// A KB with no settings.yaml reports nothing new.
	k.SetSettingsInfo(kb.SettingsInfo{})
	res, _ = s.Tools()["kb_status"].Handler(authLocalContext(), json.RawMessage(`{}`))
	var plain map[string]json.RawMessage
	_ = json.Unmarshal([]byte(res.Content[0].Text), &plain)
	if _, ok := plain["settings_findings"]; ok {
		t.Error("settings_findings present for a KB without settings.yaml")
	}
}
