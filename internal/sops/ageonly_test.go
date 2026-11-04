package sops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSecret(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secrets", "a.sops.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// A file whose sops block names a non-age master key is refused by Decrypt and
// Set before sops runs: the fake sops must never be invoked.
func TestDecryptAndSetRefuseNonAgeMasterKeys(t *testing.T) {
	blocks := map[string]string{
		"kms":        "sops:\n  kms:\n    - arn: arn:aws:kms:eu-west-1:1:key/x\n",
		"gcp_kms":    "sops:\n  gcp_kms:\n    - resource_id: projects/p/keys/k\n",
		"azure_kv":   "sops:\n  azure_kv:\n    - vault_url: https://v.example.com\n",
		"hc_vault":   "sops:\n  hc_vault:\n    - vault_address: https://v.example.com\n",
		"pgp":        "sops:\n  pgp:\n    - fp: ABCD\n",
		"mixed":      "sops:\n  age:\n    - recipient: age1aaa\n  kms:\n    - arn: x\n",
		"key_group":  "sops:\n  key_groups:\n    - age:\n        - recipient: age1aaa\n      kms:\n        - arn: x\n",
		"merge":      "sops:\n  key_groups:\n    - merge:\n        - kms:\n            - arn: x\n",
		"unknown":    "sops:\n  future_key:\n    - x: y\n",
		"two_blocks": "sops:\n  age: []\nsops:\n  kms:\n    - arn: x\n",
	}
	for name, block := range blocks {
		t.Run(name, func(t *testing.T) {
			root := writeSecret(t, "k: ENC[x]\n"+block)
			log := recordingSOPS(t, "k: v\n", "k: ENC[y]\nsops: {}\n", 0)
			_, err := Decrypt(root, "secrets/a.sops.yaml")
			if !errors.Is(err, ErrNotAgeOnly) && !strings.Contains(err.Error(), "exactly one") {
				t.Fatalf("Decrypt err = %v", err)
			}
			if err := Set(root, "secrets/a.sops.yaml", "/k", "v"); err == nil {
				t.Fatal("Set accepted the file")
			}
			if _, statErr := os.Stat(log); statErr == nil {
				t.Fatal("sops was invoked")
			}
		})
	}
}

// An age-only file, with the empty entries sops itself writes for the other
// key types, still decrypts.
func TestDecryptAcceptsAgeOnlyMetadata(t *testing.T) {
	root := writeSecret(t, "k: ENC[x]\nsops:\n  kms: null\n  gcp_kms: []\n  azure_kv:\n  hc_vault: []\n  pgp: null\n  age:\n    - recipient: age1aaa\n      enc: x\n  lastmodified: now\n  mac: ENC[m]\n  version: 3.9.0\n")
	recordingSOPS(t, "k: v\n", "", 0)
	if _, err := Decrypt(root, "secrets/a.sops.yaml"); err != nil {
		t.Fatal(err)
	}
}

func TestHermeticEnvDisablesCloudMetadata(t *testing.T) {
	env, cleanup, err := hermeticEnv(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	have := strings.Join(env, "\n")
	for _, want := range []string{"AWS_EC2_METADATA_DISABLED=true", "GCE_METADATA_HOST=invalid.invalid", "AZURE_POD_IDENTITY_AUTHORITY_HOST="} {
		if !strings.Contains(have, want) {
			t.Errorf("env lacks %s", want)
		}
	}
}

func TestRuleRecipientsAllowList(t *testing.T) {
	cases := map[string]struct {
		rule    string
		refused bool
	}{
		"azure_keyvault":       {"  - azure_keyvault: https://v.example.com/keys/k/1\n", true},
		"azure_kv":             {"  - azure_kv: [{vault_url: x}]\n", true},
		"merge":                {"  - key_groups:\n      - merge:\n          - kms:\n              - arn: x\n", true},
		"merge age ok":         {"  - key_groups:\n      - merge:\n          - age: [age1aaa]\n", false},
		"group azure_keyvault": {"  - key_groups:\n      - age: [age1aaa]\n        azure_keyvault: [{vault_url: x}]\n", true},
		"unknown key":          {"  - future_kms: x\n", true},
		"age only":             {"  - path_regex: ^secrets/\n    age: age1aaa\n    encrypted_regex: ^pw$\n", false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			os.WriteFile(filepath.Join(root, ".sops.yaml"), []byte("creation_rules:\n"+c.rule), 0o644)
			rec, matched, err := RuleRecipients(root, "secrets/a.sops.yaml")
			if c.refused {
				if err == nil {
					t.Fatalf("accepted: %v", rec)
				}
				return
			}
			if err != nil || !matched || len(rec) != 1 || rec[0] != "age1aaa" {
				t.Fatalf("rec=%v matched=%v err=%v", rec, matched, err)
			}
		})
	}
}

// sops picks its store from the extension, so only .yaml, .yml and .json are
// read: a dotenv file that is also valid YAML with an age-only sops block must
// not get past the check, whatever its metadata lines say.
func TestDecryptAndSetRefuseOtherStores(t *testing.T) {
	polyglot := "sops: {\"age\": \"a_unencrypted=b\"}\nx_unencrypted=: \"\nsops_kms__list_0__map_arn=arn:aws:kms:eu-west-1:1:key/x\nz_unencrypted=x\"\n"
	for _, name := range []string{"a.env", "a.ini", "a.sops", "a.YAML", "a"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			os.MkdirAll(filepath.Join(root, "secrets"), 0o755)
			os.WriteFile(filepath.Join(root, "secrets", name), []byte(polyglot), 0o600)
			log := recordingSOPS(t, "k: v\n", "", 0)
			rel := "secrets/" + name
			if _, err := Decrypt(root, rel); err == nil || !strings.Contains(err.Error(), "only .yaml") {
				t.Errorf("Decrypt err = %v", err)
			}
			if _, err := ResolveRefs(root, []SecretRef{{Name: "X", SOPSFile: rel, SOPSKey: "/k"}}); err == nil {
				t.Error("ResolveRefs accepted the file")
			}
			if err := Set(root, rel, "/k", "v"); err == nil {
				t.Error("Set accepted the file")
			}
			if p := RefProblem(root, rel, "/k"); p != "" {
				t.Errorf("RefProblem judged an unsupported store: %q", p)
			}
			if _, err := os.Stat(log); err == nil {
				t.Error("sops was invoked")
			}
		})
	}
}

// What is checked is what is decrypted: the bytes travel on stdin and the path
// only names the file (--filename-override), so a swap after the check is moot.
func TestDecryptGivesSopsTheCheckedBytes(t *testing.T) {
	root := writeSecret(t, "k: ENC[x]\nsops:\n  age: []\n")
	log := recordingSOPS(t, "k: v\n", "", 0)
	if _, err := Decrypt(root, "secrets/a.sops.yaml"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	if !strings.Contains(string(b), "<--input-type><yaml>") || !strings.Contains(string(b), "<--filename-override><secrets/a.sops.yaml>") || !strings.HasSuffix(strings.TrimSpace(string(b)), "<secrets/a.sops.yaml>") {
		t.Errorf("argv = %s", b)
	}
}
