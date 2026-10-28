package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

const readOnlyPath = RoutedMountPath + "?access=read"

func routedCallAt(t *testing.T, handler http.Handler, path, tool string, args map[string]any) ToolResult {
	t.Helper()
	return aliasCall(t, handler, path, tool, args)
}

// The listing of a read-only session is exactly the read tools of the
// read-write one: no write tool, every read tool.
func TestRoutedReadOnly_ToolsListHasOnlyReadTools(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a", "kb-b")

	full := decodeToolList(t, routedPost(t, handler, RoutedMountPath, toolsListBody))
	ro := decodeToolList(t, routedPost(t, handler, readOnlyPath, toolsListBody))

	listed := map[string]bool{}
	for _, tool := range ro {
		listed[tool.Name] = true
		if ToolRequiresWrite(tool.Name) {
			t.Errorf("read-only session lists the write tool %q", tool.Name)
		}
	}
	reads := 0
	for _, tool := range full {
		if ToolRequiresWrite(tool.Name) {
			continue
		}
		reads++
		if !listed[tool.Name] {
			t.Errorf("read-only session lacks the read tool %q", tool.Name)
		}
	}
	if reads == 0 || len(ro) != reads {
		t.Errorf("read-only lists %d tools, want the %d read tools", len(ro), reads)
	}
	if len(full) <= len(ro) {
		t.Errorf("the read-write session must list write tools too: %d vs %d", len(full), len(ro))
	}
}

func TestRoutedReadOnly_WriteIsRefusedWithTheReason(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a", "kb-b")
	args := map[string]any{"kb": "kb-a", "id": "manutenzione/new", "frontmatter": map[string]any{"title": "x"}, "body": "x"}

	res := routedCallAt(t, handler, readOnlyPath, "concept_write", args)
	if !res.IsError {
		t.Fatalf("concept_write on a read-only session succeeded: %+v", res)
	}
	msg := res.Content[0].Text
	for _, want := range []string{"forbidden", denyUnboundFolder, "workspace bind", "paths.yaml"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal %q does not mention %q", msg, want)
		}
	}

	// A read still works, and an unknown name stays refused.
	if res := routedCallAt(t, handler, readOnlyPath, "concept_read", map[string]any{"kb": "kb-a", "id": "manutenzione/marker"}); res.IsError {
		t.Errorf("concept_read on a read-only session failed: %+v", res)
	}
	if res := routedCallAt(t, handler, readOnlyPath, "no_such_tool", map[string]any{}); !res.IsError {
		t.Errorf("unknown tool succeeded on a read-only session")
	}
}

func TestRoutedReadOnly_CombinesWithKBS(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a", "kb-b", "kb-c")
	path := RoutedMountPath + "?kbs=kb-a,kb-b&access=read"

	rr := routedPost(t, handler, path, toolsListBody)
	if got := rr.Header().Get(AccessHeader); got != AccessReadValue {
		t.Errorf("%s = %q, want %q", AccessHeader, got, AccessReadValue)
	}
	tools := decodeToolList(t, rr)
	for _, tool := range tools {
		if ToolRequiresWrite(tool.Name) {
			t.Errorf("write tool %q listed", tool.Name)
		}
		if strings.Contains(string(tool.InputSchema), "kb-c") {
			t.Errorf("%s: schema names kb-c, outside kbs", tool.Name)
		}
	}
	if res := routedCallAt(t, handler, path, "concept_write", map[string]any{"kb": "kb-a", "id": "x/y", "frontmatter": map[string]any{"title": "x"}, "body": "x"}); !res.IsError ||
		!strings.Contains(res.Content[0].Text, denyUnboundFolder) {
		t.Errorf("concept_write: %+v", res)
	}
}

func TestRoutedReadOnly_HeaderOnlyOnReadOnlySessions(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a", "kb-b")
	if got := routedPost(t, handler, RoutedMountPath, toolsListBody).Header().Get(AccessHeader); got != "" {
		t.Errorf("read-write session carries %s: %q", AccessHeader, got)
	}
	// Every response, not only tools/list.
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	if got := routedPost(t, handler, readOnlyPath, init).Header().Get(AccessHeader); got != AccessReadValue {
		t.Errorf("initialize: %s = %q", AccessHeader, got)
	}
}

func TestRoutedReadOnly_BogusAccessIs400(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a")
	rr := routedPost(t, handler, RoutedMountPath+"?access=bogus", toolsListBody)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"read"`) {
		t.Errorf("the 400 must name the accepted value: %s", rr.Body.String())
	}
}

func TestRoutedReadOnly_InstructionsSayReadOnly(t *testing.T) {
	t.Parallel()
	handler := newRoutedTestHandler(t, "kb-a", "kb-b")
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	instr := func(path string) string {
		rr := routedPost(t, handler, path, init)
		var resp struct {
			Result struct {
				Instructions string `json:"instructions"`
			} `json:"result"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v; %s", err, rr.Body.String())
		}
		return resp.Result.Instructions
	}
	// Unmarked connections get the guide (D1097); the bridge's workspace-bind
	// sentence is for a provisioned bridge only.
	if got := instr(readOnlyPath); !strings.Contains(got, "read-only") || strings.Contains(got, "workspace bind") {
		t.Errorf("unmarked read-only instructions = %q", got)
	}
	if got := instr(readOnlyPath + "&provisioned=1"); got != readOnlyInstructions {
		t.Errorf("provisioned read-only instructions = %q, want readOnlyInstructions", got)
	}
	if got := instr(RoutedMountPath); !strings.Contains(got, "concept_write") {
		t.Errorf("read-write guide = %q, want the operational bullets", got)
	}
	if got := instr(RoutedMountPath + "?provisioned=1"); got != "" {
		t.Errorf("provisioned read-write instructions = %q, want none", got)
	}
}

// The mode is part of the cache key: two modes on one set are two Servers, with
// and without ?kbs=.
func TestRoutedReadOnly_DoesNotShareACachedServer(t *testing.T) {
	t.Parallel()
	multi := NewMultiKBServer("test")
	for _, name := range []string{"kb-a", "kb-b"} {
		k := setupNamedTestKB(t, name)
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	if err := multi.EnableRoutedMount("test", nil); err != nil {
		t.Fatal(err)
	}
	admin := auth.ContextWithPrincipal(context.Background(), auth.LocalAdminPrincipal())
	for _, kbs := range [][]string{nil, {"kb-a,kb-b"}, {"kb-a"}} {
		rw, _, _, err := multi.routedFor(admin, kbs, "")
		if err != nil {
			t.Fatal(err)
		}
		ro, _, _, err := multi.routedFor(admin, kbs, AccessReadValue)
		if err != nil {
			t.Fatal(err)
		}
		if rw == ro {
			t.Errorf("kbs=%v: read-only and read-write share a Server", kbs)
		}
		if !ro.readOnlySession || rw.readOnlySession {
			t.Errorf("kbs=%v: modes swapped (ro=%v rw=%v)", kbs, ro.readOnlySession, rw.readOnlySession)
		}
		again, _, _, _ := multi.routedFor(admin, kbs, AccessReadValue)
		if again != ro {
			t.Errorf("kbs=%v: the read-only Server is not cached", kbs)
		}
	}
}

// accessOf maps the old bridge flag of the view fixtures to an access value.
func accessOf(readOnly bool) string {
	if readOnly {
		return AccessReadValue
	}
	return ""
}
