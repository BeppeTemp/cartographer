package surveyor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/surveyor/recipe"
	"github.com/BeppeTemp/cartographer/internal/surveyor/system"
)

// gateServer is a real server over a committed KB whose "ingest" map already
// holds live pages: a lone page passes the orphan check (D1099), so only a map
// with neighbours shows what a unit with no link does to the write gate.
func gateServer(t *testing.T) *mcpserver.Server { return gateServerIndex(t, "generated") }

// gateServerIndex is gateServer with the map's index mode: "curated" lists the
// live pages by hand, so a new page needs index_entry (#2139).
func gateServerIndex(t *testing.T, index string) *mcpserver.Server {
	t.Helper()
	k := kbtest.New(t)
	if err := k.CreateMap("ingest", "Ingest", "map", nil); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"ingest/_map.md":      "---\ntype: Map\nkind: map\ntitle: Ingest\nindex: " + index + "\n---\n# Ingest\n",
		"ingest/index.md":     "# Ingest\n\n- [Plan](plan.md)\n- [Other](other.md)\n- [Another](another-live.md)\n",
		"ingest/plan":         "---\ntype: IngestPlan\ntitle: Plan\nstatus: in-progress\n---\n# Plan\n\nSee [[ingest/other]].\n",
		"ingest/other":        "---\ntype: Note\ntitle: Other\n---\n# Other\n\nSee [[ingest/plan]].\n",
		"ingest/another-live": "---\ntype: Note\ntitle: Another\n---\n# Another\n\nSee [[ingest/plan]].\n",
	}
	for id, content := range files {
		p := filepath.Join(k.DataRoot(), filepath.FromSlash(id))
		if !strings.HasSuffix(p, ".md") {
			p += ".md"
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := kbCommit(k); err != nil {
		t.Fatal(err)
	}
	s := mcpserver.New("test")
	mcpserver.RegisterKBTools(s, k, mcpserver.Deps{})
	return s
}

func kbCommit(k *kb.KB) (string, error) { return k.CommitOpAs("test: seed", "", "") }

func writeUnit(t *testing.T, s *mcpserver.Server, id, body string) (string, bool) {
	t.Helper()
	fm := map[string]any{"type": "IngestUnit", "shape": "ingest-unit", "title": "Unit", "source": "src-x", "plan": "ingest/plan",
		"status": "open", "targets": []string{"ingest/other"}, "extract": "e", "verify": "v", "executor": "any"}
	return writeOp(t, s, unitWriteOp(id, fm, body))
}

func writeOp(t *testing.T, s *mcpserver.Server, op map[string]any) (string, bool) {
	t.Helper()
	args, _ := json.Marshal(map[string]any{"operations": []map[string]any{op}})
	ctx := auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "admin", Policy: auth.Policy{Admin: true}})
	res, err := s.Tools()["concept_batch"].Handler(ctx, args)
	if err != nil {
		t.Fatal(err)
	}
	return res.Content[0].Text, res.IsError
}

// A unit with no link to its plan is refused as an orphan, and the refusal names
// that check; the same unit with the plan link goes through (#2137).
func TestSystemUnitThroughTheRealWriteGate(t *testing.T) {
	s := gateServer(t)
	c := system.Change{Kind: recipe.Added, Key: "g1"}
	// The body as it was written before the plan link: refused.
	text, isErr := writeUnit(t, s, "ingest/unit-old", systemBody("Brief.", "", c, "r1", "2026-01-01", nil))
	if !isErr {
		t.Fatalf("a unit with no link was accepted: %s", text)
	}
	if got := refusalCheck(text); got != "write_gate: orphan" {
		t.Fatalf("refusalCheck = %q for %q", got, text)
	}
	text, isErr = writeUnit(t, s, "ingest/unit-new", systemBody("Brief.", "ingest/plan", c, "r1", "2026-01-01", nil))
	if isErr {
		t.Fatalf("the unit with the plan link was refused: %s", text)
	}
}

func TestFeedUnitThroughTheRealWriteGate(t *testing.T) {
	s := gateServer(t)
	if text, isErr := writeUnit(t, s, "ingest/feed-old", "Brief of the document."); !isErr {
		t.Fatalf("a unit with no link was accepted: %s", text)
	}
	body := withPlanLink("Brief of the document.", "ingest/plan")
	if text, isErr := writeUnit(t, s, "ingest/feed-new", body); isErr {
		t.Fatalf("the unit with the plan link was refused: %s", text)
	}
}

// A map with a curated index and other live pages refuses a unit whose creating
// write lacks index_entry, naming index_incomplete; the unit write op of the
// poll (system and feed) asks for it and lands in the index (#2139).
func TestUnitsThroughTheRealWriteGateInACuratedIndexMap(t *testing.T) {
	s := gateServerIndex(t, "curated")
	c := system.Change{Kind: recipe.Added, Key: "g1"}
	sysBody := systemBody("Brief.", "ingest/plan", c, "r1", "2026-01-01", nil)
	feedBody := withPlanLink("Brief of the document.", "ingest/plan")
	fm := map[string]any{"type": "IngestUnit", "shape": "ingest-unit", "title": "Unit", "source": "src-x", "plan": "ingest/plan",
		"status": "open", "targets": []string{"ingest/other"}, "extract": "e", "verify": "v", "executor": "any"}
	// The op as it was before the fix: refused, and the check is named.
	text, isErr := writeOp(t, s, map[string]any{"op": "write", "id": "ingest/unit-old", "frontmatter": fm, "body": sysBody})
	if !isErr || refusalCheck(text) != "write_gate: index_incomplete" {
		t.Fatalf("no index_entry: err=%v check=%q text=%s", isErr, refusalCheck(text), text)
	}
	for id, body := range map[string]string{"ingest/unit-sys": sysBody, "ingest/unit-feed": feedBody} {
		if text, isErr := writeUnit(t, s, id, body); isErr {
			t.Fatalf("%s refused: %s", id, text)
		}
	}
	res, err := s.Tools()["index_get"].Handler(context.Background(), []byte(`{"path":"ingest"}`))
	if err != nil || res.IsError {
		t.Fatalf("index_get: %v %v", err, res)
	}
	for _, id := range []string{"unit-sys", "unit-feed"} {
		if !strings.Contains(res.Content[0].Text, id) {
			t.Errorf("the curated index lacks %s: %s", id, res.Content[0].Text)
		}
	}
}

func TestPlanLinkIsNotRepeatedOnReuse(t *testing.T) {
	c := system.Change{Kind: recipe.Added, Key: "g1"}
	first := systemBody("Brief.", "p/plan", c, "r1", "2026-01-01", nil)
	if strings.Count(first, "[[p/plan]]") != 1 {
		t.Fatalf("link count in %q", first)
	}
	// The poll reuses what precedes "## Change": no link rides along.
	brief, _, _ := strings.Cut(first, "\n## Change")
	if strings.Contains(brief, "[[") {
		t.Fatalf("the reused brief carries a link: %q", brief)
	}
	if strings.Count(systemBody(brief, "p/plan", c, "r2", "2026-01-02", nil), "[[p/plan]]") != 1 {
		t.Fatal("link repeated or lost on reuse")
	}
	// A feed unit reuses its whole previous body.
	f := withPlanLink("Brief.", "p/plan")
	if withPlanLink(f, "p/plan") != f || strings.Count(f, "[[p/plan]]") != 1 {
		t.Fatalf("feed body %q", f)
	}
}

func TestRefusalCheck(t *testing.T) {
	gate := "write_gate: refused, nothing was written. This call introduced 3 finding(s) a write can avoid (D907):\n" +
		`[{"path":"a.md","check":"orphan","message":"orphan page /home/user/secret"},{"path":"b.md","check":"orphan","message":"x"},{"path":"c.md","check":"potential_secret","message":"y"}]` +
		"\nFix each one."
	got := refusalCheck(gate)
	if got != "write_gate: orphan, potential_secret" || strings.Contains(got, "/home/user") {
		t.Fatalf("got %q", got)
	}
	for _, in := range []string{"", "write_gate: could not restore the write, nothing was written", "write_gate: refused\n[not json", "x\n[{\"check\":\"Bad Name\"}]"} {
		if got := refusalCheck(in); !strings.HasPrefix(got, "no check named") {
			t.Errorf("%q -> %q", in, got)
		}
	}
}
