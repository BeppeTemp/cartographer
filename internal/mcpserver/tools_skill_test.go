package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
)

// setupServiceTestKB builds a temp KB (via setupTestKB) plus a Service
// concept with a secrets_source pointing at a (non-existent, for the
// resolve_secrets=false path) SOPS file. sopsAgeKeyFile, if non-empty, is
// set on the returned KB.
func setupServiceTestKB(t *testing.T, sopsAgeKeyFile string) *kb.KB {
	t.Helper()
	k := setupTestKB(t)
	k.SopsAgeKeyFile = sopsAgeKeyFile

	svcContent := "---\ntype: Service\ntitle: Test Service\nsecrets_source: secrets/test.sops.yaml\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "svc-test.md"), []byte(svcContent), 0o644); err != nil {
		t.Fatalf("write service concept: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(k.Root, "secrets"), 0o755); err != nil {
		t.Fatalf("mkdir secrets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "secrets", "test.sops.yaml"), []byte("placeholder"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	return k
}

// fakeSopsInPathMCP mirrors internal/sops's fakeSopsInPath: installs a fake
// "sops" script in PATH that echoes a fixed secret plus the
// SOPS_AGE_KEY_FILE it received, so tests can assert env propagation without
// depending on a real encrypted file.
func fakeSopsInPathMCP(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake sops script requires a POSIX shell, skipping on windows")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\necho \"db_password: super-secret\"\necho \"resolved_key: ${SOPS_AGE_KEY_FILE}\"\n"
	path := filepath.Join(dir, "sops")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sops: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func callServiceGet(t *testing.T, k *kb.KB, serviceID string, resolveSecrets bool) ToolResult {
	t.Helper()
	return callServiceGetArgs(t, k, map[string]any{
		"service_id":      serviceID,
		"resolve_secrets": resolveSecrets,
	})
}

func callServiceGetArgs(t *testing.T, k *kb.KB, params map[string]any) ToolResult {
	t.Helper()
	tool := toolServiceGet(k)
	args, _ := json.Marshal(params)
	res, err := tool.Handler(authLocalContext(), args)
	if err != nil {
		t.Fatalf("service_get handler error: %v", err)
	}
	return res
}

func TestServiceGet_ResolveSecretsFalse_Unchanged(t *testing.T) {
	k := setupServiceTestKB(t, "")

	res := callServiceGet(t, k, "svc-test", false)
	if res.IsError {
		t.Fatalf("service_get resolve_secrets=false: unexpected error: %v", res.Content)
	}
	text := res.Content[0].Text
	if !containsAll(text, []string{"type: Service", "Test Service"}) {
		t.Errorf("service_get output missing expected frontmatter: %s", text)
	}
	if containsAll(text, []string{"db_password"}) {
		t.Errorf("service_get resolve_secrets=false must not include secrets: %s", text)
	}
}

func TestServiceGet_ResolveSecretsTrue_NoAgeKey_Errors(t *testing.T) {
	fakeSopsInPathMCP(t)
	k := setupServiceTestKB(t, "") // no SopsAgeKeyFile configured

	res := callServiceGet(t, k, "svc-test", true)
	if !res.IsError {
		t.Fatalf("expected error when SopsAgeKeyFile is not configured, got: %v", res.Content)
	}
}

func TestServiceGet_ResolveSecrets_RejectsPathTraversal(t *testing.T) {
	fakeSopsInPathMCP(t)
	k := setupServiceTestKB(t, filepath.Join(t.TempDir(), "age.key"))

	// Overwrite the service with a traversal secrets_source: must be rejected
	// before any decryption, even with age key + (fake) sops available.
	svc := "---\ntype: Service\ntitle: Test Service\nsecrets_source: ../../../etc/passwd\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "svc-test.md"), []byte(svc), 0o644); err != nil {
		t.Fatalf("write service concept: %v", err)
	}

	res := callServiceGet(t, k, "svc-test", true)
	if !res.IsError {
		t.Fatalf("expected error for traversal secrets_source, got: %v", res.Content)
	}
	if !strings.Contains(res.Content[0].Text, "inside the KB") {
		t.Errorf("expected traversal rejection message, got: %s", res.Content[0].Text)
	}
}

func TestServiceGet_ResolveSecretsTrue_FakeSops(t *testing.T) {
	fakeSopsInPathMCP(t)
	k := setupServiceTestKB(t, "/tmp/age-key.txt")

	res := callServiceGetArgs(t, k, map[string]any{"service_id": "svc-test", "resolve_secrets": true, "reveal": true})
	if res.IsError {
		t.Fatalf("service_get resolve_secrets=true reveal=true: unexpected error: %v", res.Content)
	}
	text := res.Content[0].Text
	if !containsAll(text, []string{"db_password=super-secret", "resolved_key=/tmp/age-key.txt"}) {
		t.Errorf("service_get resolve_secrets=true reveal=true missing expected secrets/env propagation: %s", text)
	}
}

// The only caller of service_get is an MCP client, so resolving secrets must
// not print them into the transcript unless reveal is passed (D261, as D158
// for secret_resolve).
func TestServiceGet_ResolveSecrets_RedactsByDefault(t *testing.T) {
	fakeSopsInPathMCP(t)
	k := setupServiceTestKB(t, "/tmp/age-key.txt")

	res := callServiceGet(t, k, "svc-test", true)
	if res.IsError {
		t.Fatalf("service_get resolve_secrets=true: unexpected error: %v", res.Content)
	}
	text := res.Content[0].Text
	// The absence assertion is the one that makes a regression fail the build
	// instead of leaking.
	if strings.Contains(text, "super-secret") {
		t.Errorf("the plaintext value leaked into the default output: %q", text)
	}
	if !containsAll(text, []string{"db_password=<redacted>", "secrets (from "}) {
		t.Errorf("output = %q, want the header and the key name with a redacted value", text)
	}
}

// reveal without resolve_secrets is a no-op, not an error: the output is the
// plain concept, exactly as without either flag.
func TestServiceGet_RevealWithoutResolveSecrets_IsNoop(t *testing.T) {
	k := setupServiceTestKB(t, "")
	plain := callServiceGet(t, k, "svc-test", false)
	revealed := callServiceGetArgs(t, k, map[string]any{"service_id": "svc-test", "reveal": true})
	if plain.IsError || revealed.IsError {
		t.Fatalf("service_get errors: plain=%v revealed=%v", plain.Content, revealed.Content)
	}
	if plain.Content[0].Text != revealed.Content[0].Text {
		t.Errorf("reveal without resolve_secrets changed the output:\n%q\nvs\n%q", plain.Content[0].Text, revealed.Content[0].Text)
	}
}

func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
