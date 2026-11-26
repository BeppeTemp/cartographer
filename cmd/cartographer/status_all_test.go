package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// AC-cad-7: `status --output json` reports binding_origin "all" for a marker,
// "explicit" for a list and "default" otherwise, and the human line prints it.
func TestStatusBindingOrigin(t *testing.T) {
	srv := syncPullServer(t, func(kb string) (string, bool) { return issuesPayload(t, kb, "skill-"+kb), false })
	defer srv.Close()
	home := t.TempDir()
	setHome(t, home)
	cfg := issuesConfig(srv.URL)
	cfg.Agents = []string{"claude", "codex", "opencode"}
	if err := cfg.BindAll("claude"); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Bind("codex", "one"); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	out := withStdout(t, func() { cmdStatus([]string{"--output", "json"}) })
	var snap statusSnapshot
	if err := json.Unmarshal([]byte(out), &snap); err != nil {
		t.Fatalf("status json: %v\n%s", err, out)
	}
	want := map[string]string{"claude": "all", "codex": "explicit", "opencode": "default"}
	seen := 0
	for _, p := range snap.Providers {
		if w, ok := want[p.Name]; ok {
			seen++
			if p.BindingOrigin != w {
				t.Errorf("%s binding_origin = %q, want %q", p.Name, p.BindingOrigin, w)
			}
		}
	}
	if seen != len(want) {
		t.Fatalf("providers reported: %+v\n%s", snap.Providers, out)
	}
	line := withStdout(t, func() {
		printBindingLine(providerStatus{Name: "claude", BindingOrigin: "all", BoundKBs: []string{"one", "two"}})
	})
	if !strings.Contains(line, "kbs one, two (all)") {
		t.Errorf("binding line = %q", line)
	}
}
