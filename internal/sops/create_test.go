package sops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".sops.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A matching rule that names a recipient besides age is refused, top-level or
// in a key group: its data key would go to a recipient nobody checked (D1191).
func TestRuleRecipientsRefusesNonAgeKeys(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"kms":        "creation_rules:\n  - path_regex: ^secrets/\n    age: age1aaa\n    kms: arn:aws:kms:region:111:key/x\n",
		"pgp":        "creation_rules:\n  - path_regex: ^secrets/\n    pgp: ABCDEF\n",
		"vault":      "creation_rules:\n  - path_regex: ^secrets/\n    hc_vault_transit_uri: https://vault.example.com/v1/transit/keys/k\n",
		"key_groups": "creation_rules:\n  - path_regex: ^secrets/\n    key_groups:\n      - age: [age1aaa]\n        pgp: [ABCDEF]\n",
	} {
		root := t.TempDir()
		writeConfig(t, root, body)
		_, _, err := RuleRecipients(root, "secrets/new.sops.yaml")
		if err == nil || !strings.Contains(err.Error(), "not an age key") || !strings.Contains(err.Error(), "^secrets/") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// An age-only rule, key groups included, is accepted.
	root := t.TempDir()
	writeConfig(t, root, "creation_rules:\n  - path_regex: ^secrets/\n    key_groups:\n      - age: [age1aaa, age1bbb]\n")
	if r, ok, err := RuleRecipients(root, "secrets/new.sops.yaml"); err != nil || !ok || len(r) != 2 {
		t.Errorf("age-only: %v %v %v", r, ok, err)
	}
	// A rule that does not match the path is not inspected.
	root = t.TempDir()
	writeConfig(t, root, "creation_rules:\n  - path_regex: ^other/\n    kms: arn\n  - path_regex: ^secrets/\n    age: age1aaa\n")
	if r, ok, err := RuleRecipients(root, "secrets/new.sops.yaml"); err != nil || !ok || len(r) != 1 {
		t.Errorf("non-matching kms rule: %v %v %v", r, ok, err)
	}
}

// Create names the root .sops.yaml to sops, so a secrets/.sops.yaml with other
// recipients cannot redirect the data key.
func TestCreatePassesRootConfigAndIgnoresNestedOne(t *testing.T) {
	// Create resolves the root (create.go): on macOS t.TempDir() is under the
	// /var -> /private/var symlink, so the expected path is the resolved one.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	argv := filepath.Join(t.TempDir(), "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argv + "\ncat >/dev/null\nprintf 'k: ENC[x]\\nsops: {}\\n'\n"
	if err := os.WriteFile(filepath.Join(bin, "sops"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("POSIX shell required")
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	key := filepath.Join(t.TempDir(), "k.age")
	if err := os.WriteFile(key, []byte("# public key: age1aaa\nSECRET\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, root, "creation_rules:\n  - path_regex: ^secrets/\n    age: age1aaa\n")
	writeConfig(t, filepath.Join(root, "secrets"), "creation_rules:\n  - age: age1evil\n")
	if err := Create(root, "secrets/new.sops.yaml", "/k", "v", key); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(argv)
	if !strings.Contains(string(got), "--config\n"+filepath.Join(root, ".sops.yaml")+"\n") {
		t.Fatalf("sops was not given the root config: %q", got)
	}
	if strings.Contains(string(got), "age1evil") {
		t.Fatalf("nested config reached sops: %q", got)
	}
}

// #1303: the refusal names the path as the caller sent it.
func TestCreateRefusalEchoesThePathAsSent(t *testing.T) {
	t.Parallel()
	for _, rel := range []string{"secrets/../x.sops.yaml", "x.sops.yaml", "../x.sops.yaml"} {
		err := Create(t.TempDir(), rel, "/k", "v", "")
		if err == nil || !strings.Contains(err.Error(), `path "`+rel+`" must be relative and inside the KB`) {
			if err != nil && strings.Contains(err.Error(), "sops binary not found") {
				t.Skip("sops not installed")
			}
			t.Errorf("Create(%q) = %v", rel, err)
		}
	}
}
