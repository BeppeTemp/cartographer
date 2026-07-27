package sops

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func recordingSOPS(t *testing.T, decrypt, result string, exit int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake sops")
	}
	dir, log := t.TempDir(), filepath.Join(t.TempDir(), "sops.log")
	script := "#!/bin/sh\n" +
		"printf 'cwd=%s\\n' \"$PWD\" >> \"$FAKE_SOPS_LOG\"\n" +
		"printf 'env=%s\\n' \"$SOPS_AGE_KEY_FILE\" >> \"$FAKE_SOPS_LOG\"\n" +
		"printf 'argv=' >> \"$FAKE_SOPS_LOG\"; for x in \"$@\"; do printf '<%s>' \"$x\" >> \"$FAKE_SOPS_LOG\"; done; printf '\\n' >> \"$FAKE_SOPS_LOG\"\n" +
		"if [ \"$1\" = set ]; then IFS= read -r stdin; printf 'stdin=%s\\n' \"$stdin\" >> \"$FAKE_SOPS_LOG\"; [ \"$FAKE_SOPS_EXIT\" = 0 ] || exit \"$FAKE_SOPS_EXIT\"; printf '%s' \"$FAKE_SOPS_RESULT\" > \"$3\"; exit 0; fi\n" +
		"[ \"$FAKE_SOPS_EXIT\" = 0 ] || exit \"$FAKE_SOPS_EXIT\"\n" +
		"printf '%s' \"$FAKE_SOPS_DECRYPT\"\n"
	if err := os.WriteFile(filepath.Join(dir, "sops"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_SOPS_LOG", log)
	t.Setenv("FAKE_SOPS_DECRYPT", decrypt)
	t.Setenv("FAKE_SOPS_RESULT", result)
	t.Setenv("FAKE_SOPS_EXIT", fmt.Sprint(exit))
	return log
}

func encrypted() []byte { return []byte("nested:\n  old: ENC[old]\nlist:\n  - ENC[list]\nsops: {}\n") }

func TestSetSecurityContract(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "secrets", "a.sops.yaml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encrypted(), 0o640); err != nil {
		t.Fatal(err)
	}
	secret := "raw secret \\\" newline\n"
	log := recordingSOPS(t, "", "nested:\n  old: ENC[new]\nlist:\n  - ENC[list]\nsops: {}\n", 0)
	if err := Set(root, "secrets/a.sops.yaml", "/nested/old", secret, AgeKeyEnv("/age/key")...); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	text := string(b)
	resolvedRoot, _ := filepath.EvalSymlinks(root)
	if !strings.Contains(text, "cwd="+resolvedRoot) || !strings.Contains(text, "env=/age/key") || !strings.Contains(text, "argv=<set><--value-stdin><secrets/.sops-set-") || !strings.Contains(text, "><[\"nested\"][\"old\"]>") {
		t.Fatalf("wrong invocation: %s", text)
	}
	if !strings.Contains(text, "stdin=\"raw secret") || strings.Contains(text, "argv=<"+secret+">") || strings.Contains(text, "env="+secret) {
		t.Fatalf("secret leaked outside stdin: %s", text)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o640 {
		t.Errorf("mode = %o", info.Mode().Perm())
	}
}

func TestSetSelectorsAndFailuresAreAtomic(t *testing.T) {
	cases := []struct {
		name, pointer, result string
		exit                  int
	}{{"new-leaf", "/nested/new", "nested:\n  old: ENC[old]\n  new: ENC[new]\nlist:\n  - ENC[list]\nsops: {}\n", 0}, {"sequence", "/list/0", "nested:\n  old: ENC[old]\nlist:\n  - ENC[new]\nsops: {}\n", 0}, {"subprocess", "/nested/old", "", 3}, {"clear", "/nested/old", "nested:\n  old: clear\nsops: {}\n", 0}, {"missing", "/nested/old", "nested: {}\nsops: {}\n", 0}, {"non-scalar", "/nested/old", "nested:\n  old: {}\nsops: {}\n", 0}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			p := filepath.Join(root, "secrets", "a.sops.yaml")
			os.MkdirAll(filepath.Dir(p), 0o755)
			os.WriteFile(p, encrypted(), 0o640)
			before, _ := os.ReadFile(p)
			log := recordingSOPS(t, "", tc.result, tc.exit)
			err := Set(root, "secrets/a.sops.yaml", tc.pointer, "never-report-me")
			if tc.exit == 0 && (tc.name == "new-leaf" || tc.name == "sequence") {
				if err != nil {
					t.Fatal(err)
				}
				b, _ := os.ReadFile(log)
				want := "[\"nested\"][\"new\"]"
				if tc.name == "sequence" {
					want = "[\"list\"][0]"
				}
				if !strings.Contains(string(b), "<"+want+">") {
					t.Errorf("selector missing: %s", b)
				}
				return
			}
			if err == nil {
				t.Fatal("expected failure")
			}
			after, _ := os.ReadFile(p)
			if string(before) != string(after) {
				t.Error("failure changed original bytes")
			}
			if strings.Contains(err.Error(), "never-report-me") {
				t.Errorf("secret leaked in %v", err)
			}
		})
	}
}

func TestSetRejectsUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "secrets"), 0o755)
	os.WriteFile(filepath.Join(root, "secrets", "a.sops.yaml"), encrypted(), 0o600)
	recordingSOPS(t, "", "", 0)
	for _, p := range []string{"../a.sops.yaml", filepath.Join(root, "secrets/a.sops.yaml")} {
		if err := Set(root, p, "/x", "v"); err == nil {
			t.Errorf("accepted %q", p)
		}
	}
	if err := Set(root, "secrets/missing.sops.yaml", "/x", "v"); err == nil {
		t.Error("accepted missing file")
	}
	os.WriteFile(filepath.Join(root, "secrets", "plain.sops.yaml"), []byte("x: y\n"), 0o600)
	if err := Set(root, "secrets/plain.sops.yaml", "/x", "v"); err == nil {
		t.Error("accepted plaintext")
	}
	if runtime.GOOS != "windows" {
		os.Symlink(filepath.Join(root, "secrets"), filepath.Join(root, "link"))
		os.Symlink(filepath.Join(root, "secrets", "a.sops.yaml"), filepath.Join(root, "secrets", "final.sops.yaml"))
		for _, p := range []string{"link/a.sops.yaml", "secrets/final.sops.yaml"} {
			if err := Set(root, p, "/nested/old", "v"); err == nil {
				t.Errorf("accepted symlink %q", p)
			}
		}
	}
}

func TestFlattenAndResolveRefsSafety(t *testing.T) {
	vals, err := parseYAMLFlat([]byte("a/b: one\na~b: two\nleft: {same: x}\nright: {same: y}\nlist: [{k: true}, {k: 42}, {k: 1.5}, {k: null}]\n"))
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"a~1b": "one", "a~0b": "two", "/left/same": "x", "/right/same": "y", "/list/0/k": "true", "/list/1/k": "42", "/list/2/k": "1.5", "/list/3/k": ""} {
		if vals[key] != want {
			t.Errorf("%s = %q", key, vals[key])
		}
	}
	for _, input := range []string{"[x]", "a: 1\na: 2", "a: [", "a: &x x\nb: *x", "1: x"} {
		if _, err := parseYAMLFlat([]byte(input)); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "secrets"), 0o755)
	os.WriteFile(filepath.Join(root, "secrets", "a.sops.yaml"), []byte("x"), 0o600)
	log := recordingSOPS(t, "one: value-one\ntwo: value-two\n", "", 0)
	got, err := ResolveRefs(root, []SecretRef{{Name: "ONE", SOPSFile: "secrets/a.sops.yaml", SOPSKey: "one"}, {Name: "TWO", SOPSFile: "secrets/a.sops.yaml", SOPSKey: "two"}})
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %#v", err, got)
	}
	b, _ := os.ReadFile(log)
	if strings.Count(string(b), "argv=<decrypt>") != 1 {
		t.Error("decrypted file more than once")
	}
	_, err = ResolveRefs(root, []SecretRef{{Name: "X", SOPSFile: "secrets/a.sops.yaml", SOPSKey: "nope"}})
	if err == nil || strings.Contains(err.Error(), "value-one") || !strings.Contains(err.Error(), "one") {
		t.Errorf("unsafe missing error: %v", err)
	}
	for _, refs := range [][]SecretRef{{{Name: "X", SOPSFile: "../a", SOPSKey: "/x"}}, {{Name: "X", SOPSFile: "secrets/a.sops.yaml", SOPSKey: "/x"}, {Name: "X", SOPSFile: "secrets/a.sops.yaml", SOPSKey: "/x"}}} {
		if _, err := ResolveRefs(root, refs); err == nil {
			t.Error("accepted invalid refs")
		}
	}
}
