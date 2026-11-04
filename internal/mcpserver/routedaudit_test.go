package mcpserver

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/audit"
)

// The routed view records the calls it refuses itself (the target KB never
// sees them) and leaves the pair of a dispatched call to the target KB: each
// refusal adds one attempt+completion pair, a valid call still adds two events.
func TestRoutedMountAuditsItsOwnRefusals(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := audit.OpenWithOptions(path, audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })

	multi := NewMultiKBServer("test")
	for _, name := range []string{"kb-a", "kb-b", "kb-c"} {
		k := setupNamedTestKB(t, name)
		name := name
		multi.MountKB(name, func(s *Server) {
			RegisterKBTools(s, k, Deps{})
			s.SetAuditLog(l)
			s.SetKBName(name)
			s.SetTransport("http")
		})
	}
	if err := multi.EnableRoutedMount("test", func(s *Server) {
		s.SetTransport("http")
		s.SetRefusalAuditLog(l)
	}); err != nil {
		t.Fatal(err)
	}
	h := routedTokenStore("kb-a", "kb-b", "kb-c").Middleware(multi.Handler())

	post := func(p, tool string, args map[string]any) {
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
			"params": map[string]any{"name": tool, "arguments": args}})
		routedPost(t, h, p, string(body))
	}
	count := func() int {
		n, err := l.Count()
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	before := count()
	post(RoutedMountPath, "concept_read", map[string]any{"kb": "kb-a", "id": "manutenzione/marker"})
	if got := count() - before; got != 2 {
		t.Fatalf("a valid call added %d events, want 2", got)
	}

	refusals := map[string]func(){
		"missing kb": func() { post(RoutedMountPath, "concept_read", map[string]any{"id": "x"}) },
		"kb outside the set": func() {
			post(RoutedMountPath+"?kbs=kb-a,kb-b", "concept_read", map[string]any{"kb": "kb-c", "id": "x"})
		},
		"kb that is unknown": func() { post(RoutedMountPath, "concept_read", map[string]any{"kb": "nope", "id": "x"}) },
		"case-variant keys":  func() { post(RoutedMountPath, "concept_read", map[string]any{"kb": "kb-a", "id": "x", "ID": "y"}) },
		"unknown argument":   func() { post(RoutedMountPath, "concept_read", map[string]any{"kb": "kb-a", "id": "x", "idd": "y"}) },
		"read-only profile": func() {
			post(RoutedMountPath+"?access=read-min", "concept_write", map[string]any{"kb": "kb-a", "id": "x"})
		},
		"read-only bridge": func() { post(RoutedMountPath+"?access=read", "concept_write", map[string]any{"kb": "kb-a", "id": "x"}) },
		"read-only kb":     func() { post(RoutedMountPath+"?ro=kb-a", "concept_write", map[string]any{"kb": "kb-a", "id": "x"}) },
	}
	for name, do := range refusals {
		before := count()
		do()
		if got := count() - before; got != 2 {
			t.Errorf("%s: added %d audit events, want 2 (one denied pair)", name, got)
		}
	}

	events, err := l.Tail(100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range events {
		if e.Phase == audit.PhaseCompletion && e.Tool != "concept_read" && e.Tool != "concept_write" {
			t.Errorf("unexpected tool in audit: %+v", e)
		}
		if e.KB == "nope" || e.KB == "kb-c" && e.Outcome == audit.OutcomeUnauthorized {
			t.Errorf("a refusal attributed a name the caller merely typed: %+v", e)
		}
	}
}

// A tool the named KB lacks is refused by the routed view: one denied pair,
// attributed to the target KB, outcome unauthorized.
func TestRoutedMountAuditsAToolMissingFromTheTargetKB(t *testing.T) {
	t.Parallel()
	l, err := audit.OpenWithOptions(filepath.Join(t.TempDir(), "audit.jsonl"), audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	multi := NewMultiKBServer("test")
	full := setupNamedTestKB(t, "full-kb")
	multi.MountKB("full-kb", func(s *Server) {
		RegisterKBTools(s, full, Deps{})
		s.SetAuditLog(l)
		s.SetKBName("full-kb")
	})
	multi.MountKB("bare-kb", func(s *Server) { s.SetAuditLog(l); s.SetKBName("bare-kb") })
	if err := multi.EnableRoutedMount("test", func(s *Server) { s.SetRefusalAuditLog(l) }); err != nil {
		t.Fatal(err)
	}
	h := routedTokenStore("full-kb", "bare-kb").Middleware(multi.Handler())
	res := routedCall(t, h, "artifact_write", map[string]any{"kb": "bare-kb", "path": "skills/x/SKILL.md", "content": "x"})
	if !res.IsError {
		t.Fatal("the call succeeded")
	}
	events, err := l.Tail(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("%d audit events, want one denied pair: %+v", len(events), events)
	}
	for _, e := range events {
		if e.KB != "bare-kb" || e.Tool != "artifact_write" {
			t.Errorf("event %+v not attributed to bare-kb / artifact_write", e)
		}
		if e.Phase == audit.PhaseCompletion && e.Outcome != audit.OutcomeUnauthorized {
			t.Errorf("outcome = %q, want unauthorized", e.Outcome)
		}
	}
}
