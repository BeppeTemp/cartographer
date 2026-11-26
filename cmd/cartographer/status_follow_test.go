package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// AC-cad-15: `status --output json` lists kbs_pending.added for a KB mounted
// since the last sync and kb_change after the sync that applied it; the human
// output prints both lines; a provider with an explicit list shows neither.
func TestStatusPendingAndLastKBChange(t *testing.T) {
	srv := newFollowServer(t, "one", "two")
	dir := followHome(t, srv, "all", "one", "two")
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Agents = []string{"claude", "codex"}
	if err := cfg.Bind("codex", "one"); err != nil {
		t.Fatal(err)
	}
	if err := clientconfig.Save(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	snapshot := func() statusSnapshot {
		out := withStdout(t, func() { cmdStatus([]string{"--output", "json"}) })
		var s statusSnapshot
		if err := json.Unmarshal([]byte(out), &s); err != nil {
			t.Fatalf("status json: %v\n%s", err, out)
		}
		return s
	}
	provider := func(s statusSnapshot, name string) providerStatus {
		for _, p := range s.Providers {
			if p.Name == name {
				return p
			}
		}
		t.Fatalf("no provider %s in %+v", name, s.Providers)
		return providerStatus{}
	}

	if s := snapshot(); s.KBChange != nil || provider(s, "claude").KBsPending != nil {
		t.Fatalf("nothing changed yet: change=%+v pending=%+v", s.KBChange, provider(s, "claude").KBsPending)
	}

	srv.set(func(f *followServer) { f.kbs = []string{"one", "two", "three"} })
	s := snapshot()
	if p := provider(s, "claude").KBsPending; p == nil || strings.Join(p.Added, ",") != "three" || len(p.Removed) != 0 {
		t.Errorf("claude pending = %+v", p)
	}
	if provider(s, "codex").KBsPending != nil {
		t.Errorf("an explicit list got a pending change: %+v", provider(s, "codex").KBsPending)
	}
	if s.State != "in_sync" && s.State != "drift" {
		t.Logf("state %s", s.State) // a new KB is not drift by itself
	}
	human := withStdout(t, func() { cmdStatus(nil) })
	if !strings.Contains(human, "  not synced yet: three mounted (cartographer sync applies it)") {
		t.Errorf("no pending line:\n%s", human)
	}

	if _, _, err := followSync(t, dir, syncOptions{}); err != nil {
		t.Fatal(err)
	}
	s = snapshot()
	if provider(s, "claude").KBsPending != nil {
		t.Errorf("still pending after the sync: %+v", provider(s, "claude").KBsPending)
	}
	if s.KBChange == nil || strings.Join(s.KBChange.Added, ",") != "three" || s.KBChange.At == "" {
		t.Fatalf("kb_change = %+v", s.KBChange)
	}
	human = withStdout(t, func() { cmdStatus(nil) })
	if !strings.Contains(human, "  last change ") || !strings.Contains(human, ": + three") {
		t.Errorf("no last change line:\n%s", human)
	}
	if strings.Count(human, "last change") != 1 {
		t.Errorf("the explicit provider shows the line too:\n%s", human)
	}
}
