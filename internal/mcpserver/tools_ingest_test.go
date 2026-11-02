package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

type ingestSetupOut struct {
	Written  []string `json:"written"`
	Present  []string `json:"present"`
	Notes    []string `json:"notes"`
	Warnings []string `json:"warnings"`
}

func ingestSetup(t *testing.T, s *Server) ingestSetupOut {
	t.Helper()
	var out ingestSetupOut
	if err := json.Unmarshal([]byte(callOK(t, s, "ingest_setup", `{}`)), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIngestSetupWritesOnceAndIsIdempotent(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, false)
	out := ingestSetup(t, s)
	if len(out.Written) != 2 || len(out.Present) != 0 {
		t.Fatalf("first = %+v", out)
	}
	if got, _ := os.ReadFile(filepath.Join(k.Root, "templates/ingest-unit.md")); string(got) != kb.IngestUnitTemplate() {
		t.Fatalf("unit template = %q", got)
	}
	out = ingestSetup(t, s)
	if len(out.Written) != 0 || len(out.Present) != 2 {
		t.Fatalf("second = %+v", out)
	}
	if !strings.Contains(strings.Join(out.Notes, " "), "map_create") {
		t.Fatalf("notes = %v", out.Notes)
	}
	// template_list shows both, and concept_new creates a conforming unit.
	list := callOK(t, s, "template_list", `{}`)
	if !strings.Contains(list, "ingest-plan") || !strings.Contains(list, "ingest-unit") {
		t.Fatalf("template_list = %s", list)
	}
	if err := k.CreateMap("units", "Units", "journal", nil); err != nil {
		t.Fatal(err)
	}
	callOK(t, s, "concept_new", `{"template":"ingest-unit","id":"units/u1","index_entry":true,"vars":{"title":"Unit one"}}`)
}

func TestIngestSetupLeavesDifferentTemplateAlone(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, false)
	if err := os.MkdirAll(filepath.Join(k.Root, "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	custom := "---\ntype: Topic\n---\n# mine\n"
	p := filepath.Join(k.Root, "templates/ingest-unit.md")
	if err := os.WriteFile(p, []byte(custom), 0o644); err != nil {
		t.Fatal(err)
	}
	out := ingestSetup(t, s)
	if len(out.Written) != 1 || !strings.Contains(strings.Join(out.Notes, " "), "left as it is") {
		t.Fatalf("out = %+v", out)
	}
	if got, _ := os.ReadFile(p); string(got) != custom {
		t.Fatalf("overwritten: %q", got)
	}
}

func TestIngestSetupSymlinkedTemplatesIsAWarning(t *testing.T) {
	t.Parallel()
	k, s := sourceFixture(t, false)
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(k.Root, "templates")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	out := ingestSetup(t, s)
	if len(out.Written) != 0 || len(out.Warnings) != 2 {
		t.Fatalf("out = %+v", out)
	}
	if ents, _ := os.ReadDir(elsewhere); len(ents) != 0 {
		t.Fatal("wrote through the symlink")
	}
}

func TestIngestSetupNarrowedTokenRefused(t *testing.T) {
	t.Parallel()
	_, s := sourceFixture(t, true)
	policy := auth.Policy{Permissions: []auth.Permission{{KB: "docs", Journals: []string{"sources"}, Write: true}}}
	ctx := auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "narrow", Policy: policy})
	res := s.callTool(ctx, "ingest_setup", json.RawMessage(`{}`))
	if !res.IsError || !strings.Contains(res.Content[0].Text, "forbidden") {
		t.Fatalf("narrowed token must be refused: %+v", res)
	}
}
