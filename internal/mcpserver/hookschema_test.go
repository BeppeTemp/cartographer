package mcpserver

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/hooks/hooktest"
)

// The request the write veto really sends, and a deny answer, are valid
// against the published schema (docs/schemas/hooks, the contract kit).
func TestWriteVetoRequestMatchesTheSchema(t *testing.T) {
	t.Parallel()
	const deny = `{"version":1,"decision":"deny","reason":"frozen"}`
	h := vetoServer(t, answer(deny), nil)
	h.write(botPrincipal)
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.payloads) == 0 {
		t.Fatal("no request recorded")
	}
	body, _ := json.Marshal(h.payloads[0])
	dir := filepath.Join("..", "..", "docs", "schemas", "hooks")
	if err := hooktest.Validate(dir, "write_veto", "request", body); err != nil {
		t.Fatal(err)
	}
	if err := hooktest.Validate(dir, "write_veto", "response", []byte(deny)); err != nil {
		t.Fatal(err)
	}
}
