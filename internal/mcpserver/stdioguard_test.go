package mcpserver

import "testing"

// A bad line never ends a stdio session (#1298): it is answered with the error
// HTTP gives it and the next line is read.
func TestStdioBadLinesAnswerErrorsAndSessionStaysOpen(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	s := New("0.1.0-m1")
	RegisterKBTools(s, k, Deps{})

	resps := runStdioSession(t, s.Run, []string{
		`garbage`,
		`[]`,
		`{"jsonrpc":"1.0","id":7,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":{"a":1},"method":"ping"}`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{}}}`,
	})
	if len(resps) != 5 {
		t.Fatalf("want 5 responses, got %d: %+v", len(resps), resps)
	}
	want := []struct {
		code int
		id   string
	}{{ErrCodeParseError, "null"}, {ErrCodeInvalidRequest, "null"}, {ErrCodeInvalidRequest, "7"}, {ErrCodeInvalidRequest, "null"}}
	for i, w := range want {
		e := resps[i].Error
		if e == nil || e.Code != w.code {
			t.Errorf("response %d: want code %d, got %+v", i, w.code, resps[i])
		}
		if got := string(resps[i].ID); got != w.id {
			t.Errorf("response %d: id %s, want %s", i, got, w.id)
		}
	}
	if resps[4].Error != nil || resps[4].Result == nil {
		t.Fatalf("initialize after bad lines: %+v", resps[4])
	}
}
