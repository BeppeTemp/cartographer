package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fakeSecretSOPS(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake sops")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = set ]; then IFS= read -r v; printf 'key: ENC[x]\\nsops: {}\\n' > \"$3\"; exit 0; fi\n" +
		"printf 'allowed: only-this\\nother: not-requested\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "sops"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeSecretConcept(t *testing.T, kroot, id, frontmatter string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(kroot, "data", id+".md"), []byte("---\n"+frontmatter+"---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseSecretRefsContracts(t *testing.T) {
	refs, err := parseSecretRefs([]string{"TOKEN=secrets/a.sops.yaml#/allowed"})
	if err != nil || len(refs) != 1 || refs[0].SOPSKey != "/allowed" {
		t.Fatalf("%v %#v", err, refs)
	}
	for _, raw := range []any{nil, []string{}, []string{"bad"}, []string{"1BAD=secrets/a.sops.yaml#/x"}, []string{"X=/tmp/a.sops.yaml#/x"}, []string{"X=secrets/a.yaml#/x"}, []string{"X=secrets/a.sops.yaml#x"}, []string{"X=secrets/a.sops.yaml#/x", "X=secrets/a.sops.yaml#/y"}} {
		if _, err := parseSecretRefs(raw); err == nil {
			t.Errorf("accepted %#v", raw)
		}
	}
}

func TestServiceAndSecretResolveScopeDeclaredRefs(t *testing.T) {
	fakeSecretSOPS(t)
	k := setupServiceTestKB(t, "/age/key")
	writeSecretConcept(t, k.Root, "svc-refs", "type: Service\nsecret_refs:\n  - TOKEN=secrets/test.sops.yaml#/allowed\n")
	res := callServiceGet(t, k, "svc-refs", true)
	if res.IsError {
		t.Fatal(res.Content)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, "TOKEN=only-this") || strings.Contains(text, "not-requested") {
		t.Fatalf("unexpected refs output: %s", text)
	}
	writeSecretConcept(t, k.Root, "dossier", "type: Dossier\nsecret_refs:\n  - TOKEN=secrets/test.sops.yaml#/allowed\n")
	args, _ := json.Marshal(map[string]any{"concept_id": "dossier", "names": []string{"TOKEN"}})
	got, _ := toolSecretResolve(k).Handler(args)
	if got.IsError || got.Content[0].Text != "TOKEN=only-this" {
		t.Fatalf("resolve=%#v", got)
	}
	args, _ = json.Marshal(map[string]any{"concept_id": "dossier", "names": []string{"UNKNOWN"}})
	got, _ = toolSecretResolve(k).Handler(args)
	if got.IsError || got.Content[0].Text != "" {
		t.Fatalf("unknown filter=%#v", got)
	}
	writeSecretConcept(t, k.Root, "none", "type: Dossier\n")
	args, _ = json.Marshal(map[string]any{"concept_id": "none"})
	got, _ = toolSecretResolve(k).Handler(args)
	if !got.IsError || !strings.Contains(got.Content[0].Text, "no secret_refs") {
		t.Fatalf("missing declaration=%#v", got)
	}
}

func TestServiceLegacyAndMalformedRefsPrecedeSOPSGuards(t *testing.T) {
	k := setupServiceTestKB(t, "")
	res := callServiceGet(t, k, "svc-test", true)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "sops_age_key_file") {
		t.Fatal(res.Content)
	}
	writeSecretConcept(t, k.Root, "bad", "type: Service\nsecret_refs:\n  - malformed\n")
	res = callServiceGet(t, k, "bad", true)
	if !res.IsError || !strings.Contains(res.Content[0].Text, "invalid secret_refs") {
		t.Fatal(res.Content)
	}
}

func TestSecretSetSafetyAndRegistration(t *testing.T) {
	fakeSecretSOPS(t)
	k := setupServiceTestKB(t, "/age/key")
	os.WriteFile(filepath.Join(k.Root, "secrets", "test.sops.yaml"), []byte("key: ENC[old]\nsops: {}\n"), 0o600)
	value := "private-value"
	args, _ := json.Marshal(map[string]string{"path": "secrets/test.sops.yaml", "key": "/key", "value": value})
	res, _ := toolSecretSet(k).Handler(args)
	if res.IsError || strings.Contains(res.Content[0].Text, value) || !strings.Contains(res.Content[0].Text, "secrets/test.sops.yaml /key") {
		t.Fatalf("set=%#v", res)
	}
	if msg := commitMessage("secret_set", args); strings.Contains(msg, value) {
		t.Fatalf("commit message leaked: %s", msg)
	}
	for _, a := range []map[string]string{{"path": "secrets/missing.sops.yaml", "key": "/key", "value": value}, {"path": "../bad", "key": "/key", "value": value}, {"path": "secrets/test.sops.yaml", "key": "bad", "value": value}} {
		b, _ := json.Marshal(a)
		r, _ := toolSecretSet(k).Handler(b)
		if !r.IsError || strings.Contains(r.Content[0].Text, value) {
			t.Errorf("unsafe error %#v", r)
		}
	}
	if !advancedToolNames["secret_resolve"] || !advancedToolNames["secret_set"] || !ToolRequiresWrite("secret_resolve") || !ToolRequiresWrite("secret_set") {
		t.Fatal("secret tool profile incorrect")
	}
}
