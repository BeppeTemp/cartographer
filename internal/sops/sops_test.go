package sops

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestParseYAMLFlat(t *testing.T) {
	input := []byte(`client_id: my-client
client_secret: super-secret-value
token: "quoted-value"
# comment
empty_line:

nested_key: "value-with-colon: in-it"
`)
	vals, err := parseYAMLFlat(input)
	if err != nil {
		t.Fatalf("parseYAMLFlat: %v", err)
	}
	if vals["client_id"] != "my-client" {
		t.Errorf("client_id = %q, want my-client", vals["client_id"])
	}
	if vals["client_secret"] != "super-secret-value" {
		t.Errorf("client_secret = %q, want super-secret-value", vals["client_secret"])
	}
	if vals["token"] != "quoted-value" {
		t.Errorf("token = %q, want quoted-value", vals["token"])
	}
}

func TestEnvForSkill(t *testing.T) {
	resolved := map[string]string{
		"DB_PASSWORD": "secret123",
		"API_KEY":     "key456",
	}
	env := EnvForSkill(resolved)
	if len(env) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(env))
	}
	found := map[string]bool{}
	for _, e := range env {
		found[e] = true
	}
	if !found["DB_PASSWORD=secret123"] {
		t.Error("missing DB_PASSWORD")
	}
	if !found["API_KEY=key456"] {
		t.Error("missing API_KEY")
	}
}

func TestAvailable(t *testing.T) {
	// Just verify it doesn't panic — result depends on system.
	_ = Available()
}

func TestDecryptMissingSops(t *testing.T) {
	if Available() {
		t.Skip("sops is available, skipping missing-sops test")
	}
	_, err := Decrypt(t.TempDir(), "nonexistent/file.sops.yaml")
	if err == nil {
		t.Error("expected error when sops not available")
	}
}

func TestAgeKeyEnv(t *testing.T) {
	if got := AgeKeyEnv(""); got != nil {
		t.Errorf("AgeKeyEnv(\"\") = %v, want nil", got)
	}
	got := AgeKeyEnv("/path/to/key.txt")
	want := []string{"SOPS_AGE_KEY_FILE=/path/to/key.txt"}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("AgeKeyEnv(...) = %v, want %v", got, want)
	}
}

// fakeSopsInPath installs a fake "sops" executable in a tempdir prepended to
// PATH for the duration of the test. The fake echoes the SOPS_AGE_KEY_FILE
// env var it received as a "resolved_key" entry, plus a fixed "client_id"
// entry, so tests can assert both the decrypted output and that env vars
// (notably AgeKeyEnv) are actually propagated to the subprocess.
func fakeSopsInPath(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake sops script requires a POSIX shell, skipping on windows")
	}
	dir := t.TempDir()
	script := `#!/bin/sh
echo "client_id: my-client"
echo "resolved_key: ${SOPS_AGE_KEY_FILE}"
`
	path := filepath.Join(dir, "sops")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake sops: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestDecrypt_WithEnv_FakeSops(t *testing.T) {
	fakeSopsInPath(t)

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secrets", "test.sops.yaml"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	sf, err := Decrypt(root, "secrets/test.sops.yaml", AgeKeyEnv("/tmp/age-key.txt")...)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if sf.Values["client_id"] != "my-client" {
		t.Errorf("client_id = %q, want my-client", sf.Values["client_id"])
	}
	if sf.Values["resolved_key"] != "/tmp/age-key.txt" {
		t.Errorf("resolved_key = %q, want /tmp/age-key.txt (AgeKeyEnv not propagated to subprocess env)", sf.Values["resolved_key"])
	}
}

func TestDecrypt_NoEnv_FakeSops(t *testing.T) {
	fakeSopsInPath(t)
	t.Setenv("SOPS_AGE_KEY_FILE", "") // ensure no ambient value leaks in

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secrets", "test.sops.yaml"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	sf, err := Decrypt(root, "secrets/test.sops.yaml")
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if sf.Values["resolved_key"] != "" {
		t.Errorf("resolved_key = %q, want empty when no env passed", sf.Values["resolved_key"])
	}
}

func TestParseYAMLFlat_NestedPointersAndTypes(t *testing.T) {
	values, err := parseYAMLFlat([]byte("a:\n  b:\n    c:\n      leaf: one\nlist:\n  - false\n  - null\n  - nested: two\n"))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"/a/b/c/leaf": "one", "/list/0": "false", "/list/1": "", "/list/2/nested": "two"} {
		if got := values[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestParseYAMLFlatRejectsAliasAndNonStringKey(t *testing.T) {
	for _, input := range []string{"a: &x value\nb: *x\n", "1: value\n"} {
		if _, err := parseYAMLFlat([]byte(input)); err == nil {
			t.Errorf("parseYAMLFlat(%q) succeeded", input)
		}
	}
}

func TestSelectorAndVerifyEncryptedPointerRespectNodeKinds(t *testing.T) {
	root, err := yamlDocument([]byte("map:\n  \"0\": ENC[x]\nlist:\n  - ENC[y]\nsops: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := selectorForPointer(root, "/map/0"); err != nil || got != `["map"]["0"]` {
		t.Errorf("map numeric selector = %q, %v", got, err)
	}
	if got, err := selectorForPointer(root, "/list/0"); err != nil || got != `["list"][0]` {
		t.Errorf("list selector = %q, %v", got, err)
	}
	if err := verifyEncryptedPointer([]byte("map:\n  \"0\": clear\nsops: {}\n"), "/map/0"); err == nil {
		t.Error("clear target accepted")
	}
	if err := verifyEncryptedPointer([]byte("map:\n  \"0\": ENC[x]\nsops: {}\n"), "/map/missing"); err == nil {
		t.Error("missing target accepted")
	}
}
