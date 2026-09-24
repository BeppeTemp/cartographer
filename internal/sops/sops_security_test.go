package sops

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// recordingSOPS installs a fake sops that logs its invocation. Its knobs are
// baked into the script and files beside it, never passed through the
// environment: sops runs hermetic (D260), so a FAKE_* variable set on the test
// process would not reach it.
func recordingSOPS(t *testing.T, decrypt, result string, exit int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake sops")
	}
	dir, log := t.TempDir(), filepath.Join(t.TempDir(), "sops.log")
	decryptFile, resultFile := filepath.Join(dir, "decrypt.out"), filepath.Join(dir, "set.out")
	if err := os.WriteFile(decryptFile, []byte(decrypt), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resultFile, []byte(result), 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n" +
		"LOG=" + shQuote(log) + "\n" +
		"printf 'cwd=%s\\n' \"$PWD\" >> \"$LOG\"\n" +
		"printf 'env=%s\\n' \"$SOPS_AGE_KEY_FILE\" >> \"$LOG\"\n" +
		"printf 'argv=' >> \"$LOG\"; for x in \"$@\"; do printf '<%s>' \"$x\" >> \"$LOG\"; done; printf '\\n' >> \"$LOG\"\n" +
		"if [ \"$1\" = set ]; then IFS= read -r stdin; printf 'stdin=%s\\n' \"$stdin\" >> \"$LOG\"; [ " + fmt.Sprint(exit) + " = 0 ] || exit " + fmt.Sprint(exit) + "; cat " + shQuote(resultFile) + " > \"$3\"; exit 0; fi\n" +
		"[ " + fmt.Sprint(exit) + " = 0 ] || exit " + fmt.Sprint(exit) + "\n" +
		"cat " + shQuote(decryptFile) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "sops"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

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

// envDumpingSOPS installs a fake sops that writes its whole environment to the
// returned file, then behaves as a successful decrypt or set.
func envDumpingSOPS(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake sops")
	}
	dir, dump := t.TempDir(), filepath.Join(t.TempDir(), "env.dump")
	script := "#!/bin/sh\n" +
		"env > " + shQuote(dump) + "\n" +
		"if [ \"$1\" = set ]; then IFS= read -r v; printf 'nested:\\n  old: ENC[new]\\nsops: {}\\n' > \"$3\"; exit 0; fi\n" +
		"printf 'k: v\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "sops"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dump
}

func readEnvDump(t *testing.T, path string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	return env
}

// TestSOPSChildEnvironmentIsHermetic pins D260: whatever identities the server
// process carries, the sops child sees only the key file Cartographer passes.
func TestSOPSChildEnvironmentIsHermetic(t *testing.T) {
	parentHome := t.TempDir()
	t.Setenv("HOME", parentHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(parentHome, ".config"))
	t.Setenv("SOPS_AGE_KEY", "AGE-SECRET-KEY-AMBIENT")
	t.Setenv("SOPS_AGE_KEY_CMD", "cat /ambient/key")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAAMBIENT")
	t.Setenv("SOPS_AGE_KEY_FILE", "/ambient/keys.txt")

	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "secrets"), 0o755)
	os.WriteFile(filepath.Join(root, "secrets", "a.sops.yaml"), encrypted(), 0o600)

	check := func(t *testing.T, env map[string]string) {
		t.Helper()
		for _, name := range []string{"SOPS_AGE_KEY", "SOPS_AGE_KEY_CMD", "AWS_ACCESS_KEY_ID"} {
			if v, ok := env[name]; ok {
				t.Errorf("child inherited %s=%q", name, v)
			}
		}
		if env["SOPS_AGE_KEY_FILE"] != "/kb/age.key" {
			t.Errorf("SOPS_AGE_KEY_FILE = %q, want the per-KB key", env["SOPS_AGE_KEY_FILE"])
		}
		if env["PATH"] == "" {
			t.Error("PATH not passed through")
		}
		home, xdg := env["HOME"], env["XDG_CONFIG_HOME"]
		if home == "" || home == parentHome || xdg == "" || xdg == filepath.Join(parentHome, ".config") {
			t.Errorf("HOME=%q XDG_CONFIG_HOME=%q still point at the parent's", home, xdg)
		}
		for _, dir := range []string{home, xdg} {
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("isolated home %q survived the call (err=%v)", dir, err)
			}
		}
	}

	t.Run("decrypt", func(t *testing.T) {
		dump := envDumpingSOPS(t)
		if _, err := Decrypt(root, "secrets/a.sops.yaml", AgeKeyEnv("/kb/age.key")...); err != nil {
			t.Fatal(err)
		}
		check(t, readEnvDump(t, dump))
	})
	t.Run("set", func(t *testing.T) {
		dump := envDumpingSOPS(t)
		if err := Set(root, "secrets/a.sops.yaml", "/nested/old", "v", AgeKeyEnv("/kb/age.key")...); err != nil {
			t.Fatal(err)
		}
		check(t, readEnvDump(t, dump))
	})
	t.Run("no-env", func(t *testing.T) {
		// Without caller entries the child still does not inherit everything.
		dump := envDumpingSOPS(t)
		if _, err := Decrypt(root, "secrets/a.sops.yaml"); err != nil {
			t.Fatal(err)
		}
		env := readEnvDump(t, dump)
		for _, name := range []string{"SOPS_AGE_KEY", "SOPS_AGE_KEY_CMD", "AWS_ACCESS_KEY_ID", "SOPS_AGE_KEY_FILE"} {
			if v, ok := env[name]; ok {
				t.Errorf("child inherited %s=%q", name, v)
			}
		}
	})
}

// TestDecryptParsesStdoutOnly: a warning sops prints on stderr during a
// successful decrypt must not enter the YAML parse.
func TestDecryptParsesStdoutOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake sops")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"echo 'WARNING: deprecated config: something' >&2\n" +
		"printf 'alpha: one\\nbeta: two\\n'\n"
	if err := os.WriteFile(filepath.Join(dir, "sops"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "secrets"), 0o755)
	os.WriteFile(filepath.Join(root, "secrets", "a.sops.yaml"), []byte("x"), 0o600)
	sf, err := Decrypt(root, "secrets/a.sops.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(sf.Values) != 2 || sf.Values["alpha"] != "one" || sf.Values["beta"] != "two" {
		t.Fatalf("values = %#v, want exactly the stdout keys", sf.Values)
	}
}

// TestDecryptFailureKeepsStderr: the error still carries the sops diagnostic.
func TestDecryptFailureKeepsStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake sops")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\necho 'no identity matched' >&2\nexit 128\n"
	if err := os.WriteFile(filepath.Join(dir, "sops"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "secrets"), 0o755)
	os.WriteFile(filepath.Join(root, "secrets", "a.sops.yaml"), []byte("x"), 0o600)
	_, err := Decrypt(root, "secrets/a.sops.yaml")
	if err == nil || !strings.Contains(err.Error(), "sops decrypt secrets/a.sops.yaml") || !strings.Contains(err.Error(), "no identity matched") {
		t.Fatalf("err = %v", err)
	}
}
