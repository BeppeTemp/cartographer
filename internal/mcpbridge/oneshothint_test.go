package mcpbridge

import (
	"encoding/json"
	"strings"
	"testing"
)

func kbStatusResponse(t *testing.T, hint string) []byte {
	t.Helper()
	st, _ := json.Marshal(map[string]any{"connection": map[string]any{"kbs": []string{"kb-a"}, "hint": hint}})
	return mustJSON(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(st)}}}})
}

func hintOf(t *testing.T, body []byte) string {
	t.Helper()
	var r struct {
		Result struct {
			Content []struct{ Text string }
		}
	}
	if err := json.Unmarshal(body, &r); err != nil || len(r.Result.Content) == 0 {
		t.Fatalf("bad response %s: %v", body, err)
	}
	var st struct{ Connection struct{ Hint string } }
	if err := json.Unmarshal([]byte(r.Result.Content[0].Text), &st); err != nil {
		t.Fatal(err)
	}
	return st.Connection.Hint
}

// A one-shot call has no agent session to restart: the hint ends at the command.
func TestOneShotHintDropsTheNewSessionTail(t *testing.T) {
	const in = "added after this session started: run cartographer sync (bind it first with cartographer client bind if it is in unbound)" + SessionHintTail
	got := hintOf(t, withOneShotHint(kbStatusResponse(t, in)))
	if strings.Contains(got, "agent session") || !strings.HasSuffix(got, "if it is in unbound).") {
		t.Errorf("hint = %q", got)
	}
	// Nothing to rewrite: a body without a hint, or a non-result, is untouched.
	for _, b := range [][]byte{kbStatusResponse(t, "something else"), []byte(`{"jsonrpc":"2.0","id":1,"error":{}}`), []byte("not json")} {
		if out := withOneShotHint(b); string(out) != string(b) {
			t.Errorf("rewrote %s into %s", b, out)
		}
	}
}

// Only a bridge marked OneShot rewrites kb_status.
func TestOneShotBridgeRewritesOnlyKBStatus(t *testing.T) {
	var b Bridge
	m := message{Method: "tools/call", Params: mustJSON(map[string]any{"name": "kb_status"})}
	if b.responseRewriter(Scope{}, m, false) != nil {
		t.Error("a session bridge rewrote kb_status")
	}
	b.OneShot = true
	if b.responseRewriter(Scope{}, m, false) == nil {
		t.Error("a one-shot bridge left kb_status alone")
	}
	m.Params = mustJSON(map[string]any{"name": "concept_read"})
	if b.responseRewriter(Scope{}, m, false) != nil {
		t.Error("a one-shot bridge rewrote concept_read")
	}
}
