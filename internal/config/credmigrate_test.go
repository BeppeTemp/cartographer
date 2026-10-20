package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func clearLegacyCredentialEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"CARTOGRAPHER_GIT_TOKEN_DIR", "CARTOGRAPHER_SOPS_AGE_KEY_DIR", "CARTOGRAPHER_SOPS_AGE_KEY_FILE", "CARTOGRAPHER_AUDIT_KEY"} {
		t.Setenv(k, "")
	}
}

func TestLegacyCredentials(t *testing.T) {
	clearLegacyCredentialEnv(t)
	tokens := t.TempDir()
	for _, n := range []string{"b.token", "a.token", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(tokens, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(tokens, "dir.token"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARTOGRAPHER_GIT_TOKEN_DIR", tokens)
	t.Setenv("CARTOGRAPHER_AUDIT_KEY", " env-seed\n")
	t.Setenv("KB_A_SEED", "kb-a-seed")
	data := []byte(`git:
  ssh_key: /k/id
  known_hosts: /k/known_hosts
  token_dir: /ignored/by/the/env
audit:
  key_seed: file-seed
kbs:
  - remote: git@example.com:team/kb-a.git
    ssh_key: /k/a
    known_hosts: /k/a_known_hosts
    artifact_signing_seed: ${KB_A_SEED}
  - remote: https://example.com/x.git
    name: kb-b
    sops_age_key_file: /k/b.age
`)
	creds, problems := LegacyCredentials(data)
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	want := []LegacyCredential{
		{Setting: "kbs[0].ssh_key", Target: "kb-a.ssh", From: "/k/a"},
		{Setting: "kbs[0].artifact_signing_seed", Target: "kb-a.signing", Value: "kb-a-seed"},
		{Setting: "kbs[0].known_hosts", Target: "known_hosts", From: "/k/a_known_hosts"},
		{Setting: "kbs[1].sops_age_key_file", Target: "kb-b.age", From: "/k/b.age"},
		{Setting: "git.ssh_key", Target: "default.ssh", From: "/k/id"},
		{Setting: "git.known_hosts", Target: "known_hosts", From: "/k/known_hosts"},
		{Setting: "CARTOGRAPHER_GIT_TOKEN_DIR", Target: "a.token", From: filepath.Join(tokens, "a.token")},
		{Setting: "CARTOGRAPHER_GIT_TOKEN_DIR", Target: "b.token", From: filepath.Join(tokens, "b.token")},
		{Setting: "CARTOGRAPHER_AUDIT_KEY", Target: "audit.key", Value: "env-seed"},
	}
	if !reflect.DeepEqual(creds, want) {
		t.Errorf("creds =\n%+v\nwant\n%+v", creds, want)
	}
}

// What cannot be resolved is a problem that names the setting, and the rest is
// still listed.
func TestLegacyCredentialsProblems(t *testing.T) {
	clearLegacyCredentialEnv(t)
	t.Setenv("UNSET_AUDIT_SEED", "")
	data := []byte(`audit:
  key_seed: ${UNSET_AUDIT_SEED}
sops:
  age_key_dir: ` + filepath.Join(t.TempDir(), "missing") + `
kbs:
  - ssh_key: /k/nameless
  - remote: git@example.com:team/kb-c.git
    ssh_key: /k/c
`)
	creds, problems := LegacyCredentials(data)
	if len(creds) != 1 || creds[0].Target != "kb-c.ssh" {
		t.Errorf("creds = %+v, want only kb-c.ssh", creds)
	}
	for _, want := range []string{"audit.key_seed:", "sops.age_key_dir:", "kbs[0]: no usable KB name"} {
		found := false
		for _, p := range problems {
			found = found || strings.HasPrefix(p, want)
		}
		if !found {
			t.Errorf("no problem starts with %q: %v", want, problems)
		}
	}

	if creds, problems := LegacyCredentials([]byte("http: \":8080\"\n")); len(creds) != 0 || len(problems) != 0 {
		t.Errorf("a file without them: creds %v problems %v", creds, problems)
	}
}

// A per-KB directory mounted from a Kubernetes Secret, sops-nix or agenix
// holds links: the one to a file is copied, the one to a directory ignored,
// and a broken one is a problem, so the config is not realigned without it.
func TestLegacyCredentialsFollowLinks(t *testing.T) {
	clearLegacyCredentialEnv(t)
	tokens, data := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(data, "kb-a.token"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	links := map[string]string{
		"kb-a.token":   filepath.Join(data, "kb-a.token"),
		"kb-b.token":   filepath.Join(data, "missing"),
		"nested.token": data,
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(tokens, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	creds, problems := LegacyCredentials([]byte("git:\n  token_dir: " + tokens + "\n"))
	want := []LegacyCredential{{Setting: "git.token_dir", Target: "kb-a.token", From: filepath.Join(tokens, "kb-a.token")}}
	if !reflect.DeepEqual(creds, want) {
		t.Errorf("creds = %+v, want %+v", creds, want)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "kb-b.token by hand") {
		t.Errorf("problems = %v, want the broken link reported", problems)
	}
}

// A path KB without name or remote is named after its folder, like the KB
// itself, so its files are migrated rather than left to be placed by hand.
func TestLegacyCredentialsPathKB(t *testing.T) {
	clearLegacyCredentialEnv(t)
	data := []byte(`kbs:
  - path: /srv/kbs/kb-a/
    sops_age_key_file: /k/a.age
    artifact_signing_seed: abcd
`)
	creds, problems := LegacyCredentials(data)
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	want := []LegacyCredential{
		{Setting: "kbs[0].sops_age_key_file", Target: "kb-a.age", From: "/k/a.age"},
		{Setting: "kbs[0].artifact_signing_seed", Target: "kb-a.signing", Value: "abcd"},
	}
	if !reflect.DeepEqual(creds, want) {
		t.Errorf("creds = %+v, want %+v", creds, want)
	}
}
