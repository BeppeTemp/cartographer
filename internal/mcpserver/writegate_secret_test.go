package mcpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

// D1202: a credential pasted into a page is refused at write, never echoed.
// The token is assembled at runtime: a literal one trips push protection.
func TestWriteGate_PotentialSecret(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	tok := "ghp_" + strings.Repeat("a1B2", 9)
	s := gateSeed(t, k, map[string]string{"ops/clean": "---\ntype: Note\ntitle: C\n---\n# C\n\nline\n"})
	head := gitOut(t, k, "rev-parse", "HEAD")

	write := func(body string) (string, bool) {
		args, _ := json.Marshal(map[string]any{"id": "ops/s", "frontmatter": map[string]string{"type": "Note", "title": "S"}, "body": body, "index_entry": true})
		return callJSON(t, s, adminCtx, "concept_write", string(args))
	}
	text, isErr := write("# S\n\nSee [C](clean.md). run curl -H " + tok + "\n")
	refusedCleanly(t, k, head, text, isErr, "potential_secret")
	if strings.Contains(text, tok) || strings.Contains(text, tok[4:12]) {
		t.Fatalf("refusal echoes the token: %s", text)
	}
	if text, isErr = write("# S\n\nSee [C](clean.md). run curl -H ghp_<token>\n"); isErr {
		t.Fatalf("placeholder refused: %s", text)
	}

	// A page already holding a secret (seeded straight to disk and committed).
	k2 := gateKB(t)
	s2 := gateSeed(t, k2, map[string]string{"ops/p": "---\ntype: Note\ntitle: P\n---\n# P\n\nkey " + tok + "\n"})
	head = gitOut(t, k2, "rev-parse", "HEAD")
	patch := func(old, repl string) (string, bool) {
		cd, _ := k2.ReadConcept("ops/p")
		b, _ := json.Marshal(map[string]string{"id": "ops/p", "old_string": old, "new_string": repl, "if_match": cd.ContentHash})
		return callJSON(t, s2, adminCtx, "concept_patch", string(b))
	}
	// Editing above the secret moves its line, which the gate reads as a new
	// finding (the message carries the line): refused, as wanted.
	text, isErr = patch("# P\n", "# P\n\nadded\n")
	refusedCleanly(t, k2, head, text, isErr, "potential_secret")
	if text, isErr = patch(tok, "ghp_<token>"); isErr {
		t.Fatalf("removing the secret was refused: %s", text)
	}
}
