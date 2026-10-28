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
	t.Parallel()
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
	t.Parallel()
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

// fakeSopsPerFile installs a fake "sops" whose output depends on the file it
// is asked to decrypt (the last argument), so a two-file secrets_source can be
// told apart.
func fakeSopsPerFile(t *testing.T, outputs map[string]string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake sops script requires a POSIX shell, skipping on windows")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nfor last; do :; done\ncase \"$last\" in\n"
	for file, out := range outputs {
		script += file + ") printf '%s\\n' '" + out + "';;\n"
	}
	script += "*) exit 1;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "sops"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func writeSourceService(t *testing.T, k *kb.KB, sourceField string, files ...string) {
	t.Helper()
	svc := "---\ntype: Service\ntitle: Test Service\nsecrets_source: " + sourceField + "\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "svc-test.md"), []byte(svc), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(k.Root, f), []byte("placeholder"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// secrets_source is a list (the shape lint enforces); a one-element list
// resolves as a plain string does (#1323).
func TestServiceGet_SecretsSource_OneElementList(t *testing.T) {
	fakeSopsInPathMCP(t)
	k := setupServiceTestKB(t, "/tmp/age-key.txt")
	writeSourceService(t, k, "[secrets/test.sops.yaml]")

	res := callServiceGetArgs(t, k, map[string]any{"service_id": "svc-test", "resolve_secrets": true, "reveal": true})
	if res.IsError || !strings.Contains(res.Content[0].Text, "db_password=super-secret") {
		t.Fatalf("one-element list: %v", res.Content)
	}
}

// Every listed file is resolved, in order; a key in two files is an error
// naming both, with no server path.
func TestServiceGet_SecretsSource_TwoFiles(t *testing.T) {
	k := setupServiceTestKB(t, "/tmp/age-key.txt")
	if err := os.WriteFile(filepath.Join(k.Root, "secrets", "b.sops.yaml"), []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	fakeSopsPerFile(t, map[string]string{"secrets/test.sops.yaml": "alpha: one", "secrets/b.sops.yaml": "beta: two"})
	writeSourceService(t, k, "[secrets/test.sops.yaml, secrets/b.sops.yaml]")

	res := callServiceGetArgs(t, k, map[string]any{"service_id": "svc-test", "resolve_secrets": true, "reveal": true})
	if res.IsError || !containsAll(res.Content[0].Text, []string{"alpha=one", "beta=two"}) {
		t.Fatalf("two files: %v", res.Content)
	}

	fakeSopsPerFile(t, map[string]string{"secrets/test.sops.yaml": "alpha: one", "secrets/b.sops.yaml": "alpha: two"})
	res = callServiceGetArgs(t, k, map[string]any{"service_id": "svc-test", "resolve_secrets": true, "reveal": true})
	text := res.Content[0].Text
	if !res.IsError || !containsAll(text, []string{`"alpha"`, "secrets/test.sops.yaml", "secrets/b.sops.yaml"}) || strings.Contains(text, k.Root) {
		t.Fatalf("duplicate key: %v", res.Content)
	}
	if strings.Contains(text, "one") || strings.Contains(text, "two") {
		t.Errorf("a value leaked: %q", text)
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

func callSecretTool(t *testing.T, tool Tool, params map[string]any) ToolResult {
	t.Helper()
	args, _ := json.Marshal(params)
	res, err := tool.Handler(authLocalContext(), args)
	if err != nil {
		t.Fatalf("%s handler error: %v", tool.Name, err)
	}
	return res
}

func secretRefsKB(t *testing.T) *kb.KB {
	t.Helper()
	k := setupTestKB(t)
	k.SopsAgeKeyFile = filepath.Join(t.TempDir(), "kb-a.age")
	svc := "---\ntype: Service\ntitle: S\nsecret_refs:\n  - PW=secrets/app.sops.yaml#/pw\n  - X=secrets/missing.sops.yaml#/x\n---\nBody.\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "svc-refs.md"), []byte(svc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(k.Root, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "secrets", "app.sops.yaml"), []byte("placeholder"), 0o600); err != nil {
		t.Fatal(err)
	}
	return k
}

// #1278: names restricts the resolution; no server path reaches the caller.
func TestSecretResolve_NamesRestrictAndErrorsCarryNoServerPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake sops script requires a POSIX shell")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sops"), []byte("#!/bin/sh\necho 'pw: hunter2'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	k := secretRefsKB(t)
	tool := toolSecretResolve(k)

	res := callSecretTool(t, tool, map[string]any{"concept_id": "svc-refs", "names": []string{"PW"}})
	if res.IsError || !strings.Contains(res.Content[0].Text, "PW=<redacted>") {
		t.Fatalf("names [PW] must ignore the broken X: %v", res.Content)
	}
	res = callSecretTool(t, tool, map[string]any{"concept_id": "svc-refs", "names": []string{"ZZ"}})
	if !res.IsError || !strings.Contains(res.Content[0].Text, `unknown name "ZZ"`) {
		t.Fatalf("undeclared name: %v", res.Content)
	}
	res = callSecretTool(t, tool, map[string]any{"concept_id": "svc-refs"})
	text := res.Content[0].Text
	if !res.IsError || !strings.Contains(text, "reference X: secrets/missing.sops.yaml not found") || strings.Contains(text, k.Root) {
		t.Fatalf("broken reference error = %q", text)
	}
}

func TestSecretResolve_DecryptFailureNamesTheAgeKeyNotThePaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake sops script requires a POSIX shell")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\necho \"no identity matched; looked in $HOME/.ssh/id_rsa\" >&2\nexit 128\n"
	if err := os.WriteFile(filepath.Join(dir, "sops"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	k := secretRefsKB(t)
	k.AuthName = "kb-a"
	res := callSecretTool(t, toolSecretResolve(k), map[string]any{"concept_id": "svc-refs", "names": []string{"PW"}})
	text := res.Content[0].Text
	if !res.IsError || !strings.Contains(text, "age key missing or invalid for kb-a") {
		t.Fatalf("error = %q", text)
	}
	for _, leak := range []string{k.Root, k.SopsAgeKeyFile, ".ssh", "no identity", os.TempDir()} {
		if strings.Contains(text, leak) {
			t.Errorf("error leaks %q: %q", leak, text)
		}
	}
}

// #1272: each message says the way out.
func TestSecretToolMessagesSayTheWayOut(t *testing.T) {
	k := secretRefsKB(t)
	k.AuthName = "kb-a"

	res := callSecretTool(t, toolSecretSet(k), map[string]any{"path": "secrets/x.yaml", "key": "/k", "value": "v"})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "secrets/x.sops.yaml") {
		t.Errorf("secret_set bad path: %v", res.Content)
	}

	res = callSecretTool(t, toolSecretResolve(k), map[string]any{"concept_id": "../secrets/app.sops.yaml"})
	text := res.Content[0].Text
	if !res.IsError || strings.Contains(text, "non-canonical concept id") || !strings.Contains(text, "secret_refs") {
		t.Errorf("secret_resolve malformed argument: %q", text)
	}

	k.SopsAgeKeyFile = ""
	res = callSecretTool(t, toolSecretSet(k), map[string]any{"path": "secrets/app.sops.yaml", "key": "/k", "value": "v"})
	if !res.IsError || !strings.Contains(res.Content[0].Text, "age-keygen -o <credentials_dir>/kb-a.age") {
		t.Errorf("secret_set without key: %v", res.Content)
	}
}
