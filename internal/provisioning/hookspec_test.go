package provisioning

import (
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func TestValidateHookJSON(t *testing.T) {
	cases := []struct {
		name, json, wantErr string
	}{
		{"valid", `{"event":"PostToolUse","matcher":"Bash","command":"./run.sh"}`, ""},
		{"bare command", `{"event":"Stop","command":"jq ."}`, ""},
		{"absolute command", `{"event":"Stop","command":"/usr/bin/true"}`, ""},
		{"env command", `{"event":"Stop","command":"$HOME/x.sh"}`, ""},
		{"invalid json", `{"event":`, "invalid JSON"},
		{"missing event", `{"command":"./run.sh"}`, `"event"`},
		{"blank event", `{"event":"  ","command":"./run.sh"}`, `"event"`},
		{"missing command", `{"event":"Stop"}`, `"command"`},
		{"unknown event is a warning, not an error", `{"event":"PostTooluse","command":"./run.sh"}`, ""},
		{"escaping command", `{"event":"Stop","command":"../other/run.sh --x"}`, "outside the hook's directory"},
		{"escaping backslash command", `{"event":"Stop","command":"..\\other\\run.sh"}`, "outside the hook's directory"},
		{"escaping via inner dotdot", `{"event":"Stop","command":"a/../../run.sh"}`, "outside the hook's directory"},
		{"inner dotdot staying inside", `{"event":"Stop","command":"a/../run.sh"}`, ""},
	}
	for _, c := range cases {
		_, err := ValidateHookJSON([]byte(c.json))
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErr) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.wantErr, err)
		}
	}
}

func TestValidateHookJSON_ReportsClientsThatWillNotFire(t *testing.T) {
	misses, err := ValidateHookJSON([]byte(`{"event":"PreInvocation","command":"./x.sh"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(misses, ","); got != "claude,codex,opencode,kiro" {
		t.Errorf("PreInvocation misses = %q, want claude,codex,opencode,kiro", got)
	}
	misses, _ = ValidateHookJSON([]byte(`{"event":"Stop","command":"./x.sh"}`))
	if len(misses) != 0 {
		t.Errorf("Stop reaches every hook client, got misses %v", misses)
	}
	// Kiro's tool events were never probed (D300): not claimed.
	misses, _ = ValidateHookJSON([]byte(`{"event":"PreToolUse","command":"./x.sh"}`))
	if got := strings.Join(misses, ","); got != "kiro" {
		t.Errorf("PreToolUse misses = %q, want kiro", got)
	}
}

// The declared vocabulary is the single source; the per-client mappings the
// OpenCode and Antigravity registrars keep must not drift from it (D284).
func TestHookEventVocabularyAgreesWithRegistrars(t *testing.T) {
	for event := range openCodeHookEvents {
		if !hookEventReaches(event, configurator.ProviderOpenCode) {
			t.Errorf("openCodeHookEvents maps %q but the vocabulary says opencode does not fire it", event)
		}
	}
	for event := range antigravityHookEvents {
		if !hookEventReaches(event, configurator.ProviderAntigravity) {
			t.Errorf("antigravityHookEvents maps %q but the vocabulary says antigravity does not fire it", event)
		}
	}
	for event, providers := range hookEventReach {
		for _, p := range providers {
			switch p {
			case configurator.ProviderOpenCode:
				if _, ok := openCodeHookEvents[event]; !ok {
					t.Errorf("vocabulary sends %q to opencode, which has no mapping for it", event)
				}
			case configurator.ProviderAntigravity:
				if !antigravityHookEvents[event] {
					t.Errorf("vocabulary sends %q to antigravity, which has no mapping for it", event)
				}
			}
		}
	}
}

// An event outside the vocabulary warns (a client may fire more than the repo
// encodes, e.g. Claude Code's Notification) instead of rejecting the write.
func TestUnknownHookEvent(t *testing.T) {
	if w := UnknownHookEvent([]byte(`{"event":"Notification","command":"./x.sh"}`)); !strings.Contains(w, `"Notification"`) {
		t.Errorf("unknown event: want a warning naming it, got %q", w)
	}
	for _, j := range []string{`{"event":"Stop","command":"./x.sh"}`, `{"event":`} {
		if w := UnknownHookEvent([]byte(j)); w != "" {
			t.Errorf("%s: want no warning, got %q", j, w)
		}
	}
}
