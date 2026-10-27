package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

const (
	recipientA = "age1recipienta000000000000000000000000000000000000000000000000"
	recipientB = "age1recipientb000000000000000000000000000000000000000000000000"
)

// fakeEncryptSOPS installs a sops whose `encrypt` turns every scalar of stdin
// into ENC[x] and adds a sops block, as the real one does for the one-pointer
// document secret_set sends (D1191).
func fakeEncryptSOPS(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake sops")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"if [ \"$3\" = encrypt ]; then sed -E 's/^( *[^ :]+:) .+$/\\1 ENC[x]/'; echo 'sops: {}'; exit 0; fi\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "sops"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// secretKB is a KB whose server holds an age key with the public key
// recipientA and whose root .sops.yaml is rules ("" for none).
func secretKB(t *testing.T, rules string) *kb.KB {
	t.Helper()
	k := setupTestKB(t)
	keyFile := filepath.Join(t.TempDir(), "kb-a.age")
	if err := os.WriteFile(keyFile, []byte("# public key: "+recipientA+"\nAGE-SECRET-KEY-1PLACEHOLDER\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	k.SopsAgeKeyFile = keyFile
	if rules != "" {
		if err := os.WriteFile(filepath.Join(k.Root, ".sops.yaml"), []byte(rules), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return k
}

func ruleFor(recipient string) string {
	return "creation_rules:\n  - path_regex: ^secrets/.*\\.sops\\.yaml$\n    age: " + recipient + "\n"
}

func callSecretSet(t *testing.T, k *kb.KB, path, key, value string) ToolResult {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"path": path, "key": key, "value": value})
	res, err := toolSecretSet(k).Handler(authLocalContext(), b)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSecretSetCreatesFileUnderMatchingRule(t *testing.T) {
	fakeEncryptSOPS(t)
	k := secretKB(t, ruleFor(recipientA))
	const value = "plaintext-value-1"
	b, _ := json.Marshal(map[string]string{"path": "secrets/new.sops.yaml", "key": "/db/password", "value": value})
	res, err := gitWrap(k, toolSecretSet(k)).Handler(authLocalContext(), b)
	if err != nil || res.IsError {
		t.Fatalf("create refused: %v %#v", err, res)
	}
	if strings.Contains(res.Content[0].Text, value) || res.CommitSHA == "" {
		t.Fatalf("result leaks the value or did not commit: %#v", res)
	}
	got, err := os.ReadFile(filepath.Join(k.Root, "secrets", "new.sops.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), value) || !strings.Contains(string(got), "password: ENC[") || !strings.Contains(string(got), "sops:") {
		t.Fatalf("file not encrypted: %s", got)
	}
}

func TestSecretSetCreateRefusals(t *testing.T) {
	fakeEncryptSOPS(t)
	cases := []struct {
		name  string
		rules string
		path  string
		want  string
	}{
		{"no .sops.yaml", "", "secrets/new.sops.yaml", "no .sops.yaml"},
		{"no rule matches the path", "creation_rules:\n  - path_regex: ^other/.*\n    age: " + recipientA + "\n", "secrets/new.sops.yaml", "no .sops.yaml creation rule"},
		{"server key is not a recipient", ruleFor(recipientB), "secrets/new.sops.yaml", "not a recipient"},
	}
	for _, c := range cases {
		k := secretKB(t, c.rules)
		res := callSecretSet(t, k, c.path, "/token", "plaintext-value-2")
		if !res.IsError || !strings.Contains(res.Content[0].Text, c.want) || strings.Contains(res.Content[0].Text, "plaintext-value-2") {
			t.Errorf("%s: %#v, want an error containing %q", c.name, res, c.want)
		}
		if _, err := os.Stat(filepath.Join(k.Root, c.path)); err == nil {
			t.Errorf("%s: the file was created", c.name)
		}
	}
	// A list index cannot start a new file, and the key must be a pointer.
	k := secretKB(t, ruleFor(recipientA))
	if res := callSecretSet(t, k, "secrets/new.sops.yaml", "token", "x"); !res.IsError {
		t.Error("a key that is not a JSON Pointer was accepted")
	}
	// Without a server key nothing is attempted.
	k.SopsAgeKeyFile = ""
	if res := callSecretSet(t, k, "secrets/new.sops.yaml", "/token", "x"); !res.IsError || !strings.Contains(res.Content[0].Text, "age key") {
		t.Errorf("no key: %#v", res)
	}
}

// The three tools are listed to an agent, and the two that touch secrets are
// refused to a read-only token (D1191).
func TestSecretToolsAreListedAndNeedRW(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"secret_set", "secret_resolve", "service_get"} {
		if ToolAdvanced(name) {
			t.Errorf("%s is hidden from the agent tools/list", name)
		}
	}
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "r-tok", Scopes: []auth.KBScope{{KB: "kbx", Write: false}}},
		{Token: "rw-tok", Scopes: []auth.KBScope{{KB: "kbx", Write: true}}},
	})
	handler := newScopedTestHandler(t, ts)
	for _, name := range []string{"secret_set", "secret_resolve"} {
		assertMCPForbidden(t, doMCP(handler, "kbx", "r-tok", writeToolCallBody(name)))
		if rr := doMCP(handler, "kbx", "rw-tok", writeToolCallBody(name)); rr.Code != 200 {
			t.Errorf("%s with rw scope: status %d", name, rr.Code)
		}
	}
}

func TestKBStatusMissingKeyHint(t *testing.T) {
	t.Parallel()
	k := setupTestKB(t)
	k.AuthName = "kb-a"
	credDir := t.TempDir()
	k.CredentialsDir = credDir
	if err := os.MkdirAll(filepath.Join(k.Root, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	enc := "token: ENC[x]\nsops:\n  age:\n    - recipient: " + recipientA + "\n"
	if err := os.WriteFile(filepath.Join(k.Root, "secrets", "a.sops.yaml"), []byte(enc), 0o600); err != nil {
		t.Fatal(err)
	}
	hint := func() string {
		c := kbCapabilities(k)["secrets"]
		if c.State != "missing key" {
			t.Fatalf("state = %q", c.State)
		}
		return c.Hint
	}

	// No key in the directory: the expected recipient is named.
	if h := hint(); !strings.Contains(h, recipientA) || !strings.Contains(h, "no key in credentials_dir holds it") {
		t.Errorf("hint = %q", h)
	}
	// Another KB's key that is a recipient: copy it. Its file name is not shown.
	if err := os.WriteFile(filepath.Join(credDir, "kb-other.age"), []byte("# public key: "+recipientA+"\nAGE-SECRET-KEY-1PLACEHOLDER\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if h := hint(); !strings.Contains(h, "already in credentials_dir holds it") || !strings.Contains(h, "copy it to kb-a.age") || strings.Contains(h, "kb-other") {
		t.Errorf("hint = %q", h)
	}
	// A key that is not the recipient does not count.
	if err := os.WriteFile(filepath.Join(credDir, "kb-other.age"), []byte("# public key: "+recipientB+"\nAGE-SECRET-KEY-1PLACEHOLDER\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if h := hint(); !strings.Contains(h, "no key in credentials_dir holds it") {
		t.Errorf("hint = %q", h)
	}
	// The KB's own key file added after the start: keys are read at start only.
	if err := os.WriteFile(filepath.Join(credDir, "kb-a.age"), []byte("# public key: "+recipientA+"\nAGE-SECRET-KEY-1PLACEHOLDER\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if h := hint(); !strings.Contains(h, "restart the service to load it") || !strings.Contains(h, "now in credentials_dir") {
		t.Errorf("hint = %q", h)
	}
	// Loaded: no hint.
	k.SopsAgeKeyFile = filepath.Join(credDir, "kb-a.age")
	if c := kbCapabilities(k)["secrets"]; c.State != "enabled" || c.Hint != "" {
		t.Errorf("loaded key: %+v", c)
	}
}
