package sops

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fakeSOPS(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake sops script requires a POSIX shell")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sops"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func kbWithSecret(t *testing.T, body string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secrets", "app.sops.yaml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// #1274: sops picks its store from the extension of the file it is given, so
// the temporary copy must keep the target's (a copy without one is read as
// JSON and every set on an existing YAML file failed).
func TestSetTemporaryCopyKeepsTheTargetExtension(t *testing.T) {
	// $3 is the file: set --value-stdin <file> <selector>.
	fakeSOPS(t, `case "$3" in *.yaml) ;; *) echo "bad store" >&2; exit 1;; esac
cat >/dev/null
printf 'pw: ENC[new]\nsops:\n  mac: x\n' > "$3"
`)
	root := kbWithSecret(t, "pw: ENC[old]\nsops:\n  mac: x\n")
	for i := 0; i < 2; i++ {
		if err := Set(root, "secrets/app.sops.yaml", "/pw", "v"); err != nil {
			t.Fatalf("set #%d: %v", i, err)
		}
	}
}

// The same with the real sops: a key set twice on an existing file.
func TestSetTwiceOnExistingFileRealSOPS(t *testing.T) {
	if !Available() {
		t.Skip("sops is not installed")
	}
	if _, err := exec.LookPath("age-keygen"); err != nil {
		t.Skip("age-keygen is not installed")
	}
	root := t.TempDir()
	keyFile := filepath.Join(t.TempDir(), "k.age")
	if out, err := exec.Command("age-keygen", "-o", keyFile).CombinedOutput(); err != nil {
		t.Fatalf("age-keygen: %v: %s", err, out)
	}
	pub, err := exec.Command("age-keygen", "-y", keyFile).Output()
	if err != nil {
		t.Fatal(err)
	}
	writeConfig(t, root, "creation_rules:\n  - path_regex: ^secrets/.*\\.sops\\.yaml$\n    age: "+strings.TrimSpace(string(pub))+"\n")
	env := AgeKeyEnv(keyFile)
	if err := Create(root, "secrets/app.sops.yaml", "/PW", "one", keyFile, env...); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, v := range []string{"two", "three"} {
		if err := Set(root, "secrets/app.sops.yaml", "/PW", v, env...); err != nil {
			t.Fatalf("set %s: %v", v, err)
		}
	}
	sf, err := Decrypt(root, "secrets/app.sops.yaml", env...)
	if err != nil || sf.Values["PW"] != "three" {
		t.Fatalf("decrypt: %v %v", err, sf)
	}
}

func TestSetFailureNamesTheStepWithoutSOPSOutput(t *testing.T) {
	fakeSOPS(t, "echo 'secret-diagnostic /srv/private/path' >&2; exit 1\n")
	root := kbWithSecret(t, "pw: ENC[old]\nsops:\n  mac: x\n")
	err := Set(root, "secrets/app.sops.yaml", "/pw", "v")
	if err == nil || err.Error() != "sops set failed on secrets/app.sops.yaml" {
		t.Fatalf("err = %v", err)
	}
}

// #1278: names restricts resolution; a broken reference nobody asked for is
// not touched, and an undeclared name is "unknown name".
func TestResolveNamedRestrictsToTheNamedRefs(t *testing.T) {
	fakeSOPS(t, "echo 'pw: hunter2'\n")
	root := kbWithSecret(t, "x")
	refs := []SecretRef{
		{Name: "PW", SOPSFile: "secrets/app.sops.yaml", SOPSKey: "/pw"},
		{Name: "X", SOPSFile: "secrets/missing.sops.yaml", SOPSKey: "/x"},
	}
	got, err := ResolveNamed(root, refs, []string{"PW"})
	if err != nil || got["PW"] != "hunter2" || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := ResolveNamed(root, refs, []string{"ZZ"}); err == nil || !strings.Contains(err.Error(), `unknown name "ZZ"`) {
		t.Fatalf("undeclared name: %v", err)
	}
	// Without names every reference is resolved, so the broken one is found,
	// named, and carries no server path.
	_, err = ResolveNamed(root, refs, nil)
	if err == nil || err.Error() != "reference X: secrets/missing.sops.yaml not found" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(Detail(err), root) {
		t.Errorf("the detail for the log should keep the full path: %q", Detail(err))
	}
}

func TestDecryptFailureCarriesNoServerPath(t *testing.T) {
	fakeSOPS(t, "echo \"Failed to get the data key: $HOME/.config/sops/age/keys.txt\" >&2; exit 128\n")
	root := kbWithSecret(t, "x")
	_, err := Decrypt(root, "secrets/app.sops.yaml")
	if !errors.Is(err, ErrDecrypt) {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "keys.txt") || strings.Contains(err.Error(), "Failed") || strings.Contains(err.Error(), os.TempDir()) {
		t.Errorf("leaks sops output or a path: %q", err)
	}
	if !strings.Contains(Detail(err), "keys.txt") {
		t.Errorf("the log detail lost the sops output: %q", Detail(err))
	}
}

// A name asked twice is resolved once, not reported as unknown.
func TestResolveNamedRepeatedNameIsResolvedOnce(t *testing.T) {
	fakeSOPS(t, "echo 'pw: hunter2'\n")
	root := kbWithSecret(t, "x")
	refs := []SecretRef{{Name: "PW", SOPSFile: "secrets/app.sops.yaml", SOPSKey: "/pw"}}
	got, err := ResolveNamed(root, refs, []string{"PW", "PW"})
	if err != nil || got["PW"] != "hunter2" {
		t.Fatalf("got %v, %v", got, err)
	}
}
