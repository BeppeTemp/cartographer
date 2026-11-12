package mcpserver

import (
	"net/http"
	"strings"
	"testing"
)

const goneReason = "path missing: moved? update kbs[].path"

// #1963: a KB the start could not mount is reported by /health, kb_status and
// the per-KB endpoint, with the reason, to a caller who may see it, and a
// caller scoped to other KBs learns nothing about it.
func TestSkippedKBIsReportedToWhoMaySeeIt(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "work-kb")
	skipped := NewSkippedKBs()
	skipped.Set(SkippedKB{Name: "kb-gone", Reason: goneReason})
	skipped.Set(SkippedKB{Name: "kb-remote", Reason: "the clone failed: host", Retrying: true})
	f.multi.SetSkippedKBs(skipped)

	raw, body := healthAs(t, f.handler, tokAdmin)
	rows, _ := body["skipped_kbs"].([]any)
	if len(rows) != 2 || !strings.Contains(raw, goneReason) || !strings.Contains(raw, `"retrying":true`) {
		t.Errorf("admin /health skipped_kbs = %s", raw)
	}
	for _, bearer := range []string{"", tokRO, tokNone} {
		raw, body := healthAs(t, f.handler, bearer)
		if _, ok := body["skipped_kbs"]; ok || strings.Contains(raw, "kb-gone") || strings.Contains(raw, goneReason) {
			t.Errorf("bearer %q learns about a skipped KB it cannot read: %s", bearer, raw)
		}
	}

	// kb_status on the skipped KB says it is not mounted and why.
	res := f.call(t, tokAdmin, RoutedMountPath, "kb_status", map[string]any{"kb": "kb-gone"})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "not mounted") || !strings.Contains(res.Content[0].Text, goneReason) {
		t.Errorf("kb_status on a skipped KB = %+v, want 'not mounted' and the reason", res)
	}
	// A caller scoped elsewhere gets the answer of an unknown KB, not the reason.
	res = f.call(t, tokRO, RoutedMountPath, "kb_status", map[string]any{"kb": "kb-gone"})
	if strings.Contains(res.Content[0].Text, goneReason) {
		t.Errorf("a scoped caller read the reason: %s", res.Content[0].Text)
	}

	// The per-KB endpoint says so too, and only to who may see it.
	rr := f.post(t, tokAdmin, "/mcp/kb-gone", `{}`)
	if rr.Code != http.StatusNotFound || !strings.Contains(rr.Body.String(), goneReason) {
		t.Errorf("/mcp/kb-gone = %d %q", rr.Code, rr.Body.String())
	}
	rr = f.post(t, tokRO, "/mcp/kb-gone", `{}`)
	if rr.Code != http.StatusNotFound || strings.Contains(rr.Body.String(), goneReason) {
		t.Errorf("/mcp/kb-gone for a scoped caller = %d %q", rr.Code, rr.Body.String())
	}
}
