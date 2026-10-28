package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
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

// #1278: a secret_refs entry whose file or key does not exist is refused at
// write, naming the reference and the KB-relative file, never a server path.
func TestWriteGate_BrokenSecretRef(t *testing.T) {
	t.Parallel()
	k := gateKB(t)
	if err := os.MkdirAll(filepath.Join(k.Root, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	enc := "pw: ENC[AES256_GCM,data:x,type:str]\nsops:\n  mac: x\n"
	if err := os.WriteFile(filepath.Join(k.Root, "secrets", "app.sops.yaml"), []byte(enc), 0o600); err != nil {
		t.Fatal(err)
	}
	s := gateSeed(t, k, map[string]string{"ops/clean": "---\ntype: Note\ntitle: C\n---\n# C\n\nline\n"})
	head := gitOut(t, k, "rev-parse", "HEAD")
	write := func(refs ...string) (string, bool) {
		args, _ := json.Marshal(map[string]any{"id": "ops/svc", "frontmatter": map[string]any{"type": "Service", "title": "S", "secret_refs": refs}, "body": "# S\n\nSee [C](clean.md).\n", "index_entry": true})
		return callJSON(t, s, adminCtx, "concept_write", string(args))
	}
	for _, refs := range [][]string{{"X=secrets/missing.sops.yaml#/x"}, {"PW=secrets/app.sops.yaml#/nope"}} {
		text, isErr := write(refs...)
		refusedCleanly(t, k, head, text, isErr, "secret_ref_broken")
		if strings.Contains(text, k.Root) {
			t.Fatalf("refusal leaks the server path: %s", text)
		}
	}
	if text, isErr := write("PW=secrets/app.sops.yaml#/pw"); isErr {
		t.Fatalf("valid reference refused: %s", text)
	}
}
