package repodocs

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// The quarantine of flaky Go tests (docs/testing.md §Go tests, D-new-test-quarantine): a line of
// test/quarantine.tsv takes a test out of the verdict, through `go test -skip` in the Makefile and
// through the plan of scripts/test-shard.sh, until its expiry, when it lapses back in.

// quarantineMaxDays is the longest an entry may stay: expires - added, in days.
const quarantineMaxDays = 30

var (
	quarantineTestName = regexp.MustCompile(`^Test([A-Z0-9_][A-Za-z0-9_]*)?$`)
	quarantineIssue    = regexp.MustCompile(`^[1-9][0-9]*$`)
)

// checkQuarantine returns what is wrong with the list at root's test/quarantine.tsv: the shape of
// each line, the 30-day bound, and that each entry names a top-level test of its package that no
// other package of the module declares (-skip matches a name in every package of the call). It
// reads no clock: a gate never turns red on a date, an expired entry lapses instead.
func checkQuarantine(t *testing.T, root string) []string {
	t.Helper()
	raw := readFile(t, root, "test/quarantine.tsv")
	var bad []string
	type entry struct {
		line      int
		pkg, name string
	}
	var entries []entry
	for i, line := range strings.Split(strings.ReplaceAll(raw, "\r", ""), "\n") {
		n := i + 1
		if s := strings.TrimSpace(line); s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			bad = append(bad, fmt.Sprintf("line %d: want five tab-separated fields, got %d", n, len(f)))
			continue
		}
		if !quarantineTestName.MatchString(f[1]) {
			bad = append(bad, fmt.Sprintf("line %d: %q is not a Go test function name", n, f[1]))
			continue
		}
		if !quarantineIssue.MatchString(f[2]) {
			bad = append(bad, fmt.Sprintf("line %d: %s has no issue number (got %q)", n, f[1], f[2]))
		}
		added, errA := time.Parse("2006-01-02", f[3])
		expires, errE := time.Parse("2006-01-02", f[4])
		switch {
		case errA != nil || errE != nil:
			bad = append(bad, fmt.Sprintf("line %d: %s: added %q and expires %q must be YYYY-MM-DD dates", n, f[1], f[3], f[4]))
		case expires.Before(added):
			bad = append(bad, fmt.Sprintf("line %d: %s expires before it was added", n, f[1]))
		case expires.Sub(added) > quarantineMaxDays*24*time.Hour:
			bad = append(bad, fmt.Sprintf("line %d: %s expires %d days after it was added, at most %d", n, f[1], int(expires.Sub(added).Hours()/24), quarantineMaxDays))
		}
		entries = append(entries, entry{n, f[0], f[1]})
	}
	if len(entries) == 0 {
		return bad
	}
	module := regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`).FindStringSubmatch(readFile(t, root, "go.mod"))
	if module == nil {
		t.Fatal("go.mod has no module line")
	}
	decls := topLevelTests(t, root)
	for _, e := range entries {
		dir, ok := ".", e.pkg == module[1]
		if !ok && strings.HasPrefix(e.pkg, module[1]+"/") {
			dir, ok = strings.TrimPrefix(e.pkg, module[1]+"/"), true
		}
		if !ok {
			bad = append(bad, fmt.Sprintf("line %d: %s is not a package of %s", e.line, e.pkg, module[1]))
			continue
		}
		dirs := decls[e.name]
		if !dirs[dir] {
			bad = append(bad, fmt.Sprintf("line %d: %s declares no top-level test %s", e.line, e.pkg, e.name))
			continue
		}
		if len(dirs) > 1 {
			var all []string
			for d := range dirs {
				all = append(all, d)
			}
			sort.Strings(all)
			bad = append(bad, fmt.Sprintf("line %d: %s is not unique in the module (declared in %s): -skip would skip them all", e.line, e.name, strings.Join(all, ", ")))
		}
	}
	return bad
}

// topLevelTests maps each top-level `func TestX(*testing.T)` of the module's _test.go files to the
// directories that declare it, parsed (a test file that writes Go code in a string declares nothing).
// The directories go skips (testdata, `.` and `_` prefixes) and the repository's skipped ones are left out.
func topLevelTests(t *testing.T, root string) map[string]map[string]bool {
	t.Helper()
	out := map[string]map[string]bool{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if rel != "." && (skipDir(rel, name) || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !quarantineTestName.MatchString(fn.Name.Name) || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
				continue
			}
			star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			if sel, ok := star.X.(*ast.SelectorExpr); !ok || sel.Sel.Name != "T" {
				continue
			}
			dir := filepath.ToSlash(filepath.Dir(rel))
			if out[fn.Name.Name] == nil {
				out[fn.Name.Name] = map[string]bool{}
			}
			out[fn.Name.Name][dir] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The real list: every line parses, every entry has an issue, an expiry at most 30 days out, and
// names an existing test of its package, unique in the module; and the script accepts it.
func TestQuarantineListIsValid(t *testing.T) {
	root := repoRoot(t)
	for _, msg := range checkQuarantine(t, root) {
		t.Errorf("test/quarantine.tsv: %s", msg)
	}
	if _, err := exec.LookPath("bash"); err != nil || runtime.GOOS == "windows" {
		return
	}
	if out, err := exec.Command("bash", filepath.Join(root, "scripts", "quarantine.sh"), "--count").CombinedOutput(); err != nil {
		t.Errorf("scripts/quarantine.sh refuses the list: %v\n%s", err, out)
	}
}

// writeTree writes files (slash paths) under a new temporary directory and returns it.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") || strings.HasPrefix(rel, "bin/") {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// The static rules, on a toy module: each kind of bad entry is named, a valid one passes.
func TestQuarantineChecksEachRule(t *testing.T) {
	tree := map[string]string{
		"go.mod":      "module example.com/toy\n\ngo 1.22\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n\nfunc TestDup(t *testing.T) {}\n\nfunc TestMain(m *testing.M) {}\n",
		// The same name in a build-tagged twin of one package is one package.
		"a/a_windows_test.go": "package a\n\nimport \"testing\"\n\nfunc TestWin(t *testing.T) {}\n",
		"a/a_other_test.go":   "//go:build !windows\n\npackage a\n\nimport \"testing\"\n\nfunc TestWin(t *testing.T) {}\n",
		"b/b_test.go":         "package b_test\n\nimport \"testing\"\n\nfunc TestDup(t *testing.T) {}\n\ntype s struct{}\n\nfunc (s) TestMethod(t *testing.T) {}\n",
		// Go code inside a string declares nothing, and testdata is not a package.
		"c/c_test.go":            "package c\n\nconst src = `\nfunc TestGhost(t *testing.T) {}\n`\n",
		"c/testdata/x/x_test.go": "package x\n\nimport \"testing\"\n\nfunc TestHidden(t *testing.T) {}\n",
	}
	for _, c := range []struct {
		name, line, want string
	}{
		{"valid", "example.com/toy/a\tTestA\t12\t2026-01-01\t2026-01-31", ""},
		{"valid in a build-tagged twin", "example.com/toy/a\tTestWin\t12\t2026-01-01\t2026-01-31", ""},
		{"thirty-one days", "example.com/toy/a\tTestA\t12\t2026-01-01\t2026-02-01", "31 days after"},
		{"expires before added", "example.com/toy/a\tTestA\t12\t2026-01-10\t2026-01-01", "expires before"},
		{"no issue", "example.com/toy/a\tTestA\t\t2026-01-01\t2026-01-02", "no issue number"},
		{"a date that does not parse", "example.com/toy/a\tTestA\t12\t2026-02-30\t2026-03-01", "YYYY-MM-DD"},
		{"four fields", "example.com/toy/a\tTestA\t12\t2026-01-01", "five tab-separated fields"},
		{"not a test name", "example.com/toy/a\tTesta\t12\t2026-01-01\t2026-01-02", "not a Go test function name"},
		{"missing", "example.com/toy/a\tTestMissing\t12\t2026-01-01\t2026-01-02", "declares no top-level test TestMissing"},
		{"in another package", "example.com/toy/b\tTestA\t12\t2026-01-01\t2026-01-02", "declares no top-level test TestA"},
		{"TestMain is not a test", "example.com/toy/a\tTestMain\t12\t2026-01-01\t2026-01-02", "declares no top-level test TestMain"},
		{"a method is not a test", "example.com/toy/b\tTestMethod\t12\t2026-01-01\t2026-01-02", "declares no top-level test"},
		{"inside a string", "example.com/toy/c\tTestGhost\t12\t2026-01-01\t2026-01-02", "declares no top-level test"},
		{"under testdata", "example.com/toy/c/testdata/x\tTestHidden\t12\t2026-01-01\t2026-01-02", "declares no top-level test"},
		{"not unique", "example.com/toy/a\tTestDup\t12\t2026-01-01\t2026-01-02", "not unique in the module (declared in a, b)"},
		{"outside the module", "example.org/other\tTestA\t12\t2026-01-01\t2026-01-02", "not a package of example.com/toy"},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"test/quarantine.tsv": "# a comment\n\n" + c.line + "\n"}
			for k, v := range tree {
				files[k] = v
			}
			got := strings.Join(checkQuarantine(t, writeTree(t, files)), "\n")
			if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
				t.Errorf("want %q, got %q", c.want, got)
			}
		})
	}
}

// quarantineTree is a tree holding the real scripts/quarantine.sh and the given list.
func quarantineTree(t *testing.T, list string, extra map[string]string) string {
	t.Helper()
	needBash(t)
	files := map[string]string{
		"scripts/quarantine.sh": readFile(t, repoRoot(t), "scripts/quarantine.sh"),
		"test/quarantine.tsv":   list,
	}
	for k, v := range extra {
		files[k] = v
	}
	return writeTree(t, files)
}

// runQuarantine runs the tree's quarantine.sh with `--today today` (the real clock when today is
// empty): its stdout, stderr and exit code. env is added to the environment.
func runQuarantine(t *testing.T, dir, today string, args ...string) (string, string, int) {
	t.Helper()
	return runQuarantineEnv(t, dir, nil, today, args...)
}

func runQuarantineEnv(t *testing.T, dir string, env []string, today string, args ...string) (string, string, int) {
	t.Helper()
	argv := []string{filepath.Join(dir, "scripts", "quarantine.sh")}
	if today != "" {
		argv = append(argv, "--today", today)
	}
	cmd := exec.Command("bash", append(argv, args...)...)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatal(err)
		}
		code = ee.ExitCode()
	}
	return stdout.String(), stderr.String(), code
}

func TestQuarantineScript(t *testing.T) {
	const (
		a = "example.com/m/a\tTestA\t1\t2026-01-01\t2026-01-31\n"
		b = "example.com/m/b\tTestB\t2\t2026-01-01\t2026-01-10\n"
		c = "example.com/m/a\tTestC\t3\t2026-01-05\t2026-02-01\n"
	)
	header := "# comment\n\n"
	for _, tc := range []struct {
		name, list, today string
		args              []string
		want              string
	}{
		{"no entry", header, "2026-01-10", []string{"--skip"}, ""},
		{"one", header + a, "2026-01-10", []string{"--skip"}, "^(TestA)$\n"},
		{"three", header + a + b + c, "2026-01-10", []string{"--skip"}, "^(TestA|TestB|TestC)$\n"},
		{"of one package", header + a + b + c, "2026-01-10", []string{"--skip", "example.com/m/a"}, "^(TestA|TestC)$\n"},
		{"of a package with none", header + a, "2026-01-10", []string{"--skip", "example.com/m/b"}, ""},
		{"active on its expiry day", header + b, "2026-01-10", []string{"--skip"}, "^(TestB)$\n"},
		{"lapsed the day after", header + a + b, "2026-01-11", []string{"--skip"}, "^(TestA)$\n"},
		{"count", header + a + b + c, "2026-01-11", []string{"--count"}, "2\n"},
		{"count of none", header, "2026-01-11", []string{"--count"}, "0\n"},
		{"list", header + a + b, "2026-01-11", []string{"--list"}, a},
		{"expired", header + a + b + c, "2026-02-01", []string{"--expired"}, a + b},
		{"CRLF", strings.ReplaceAll(header+a, "\n", "\r\n"), "2026-01-10", []string{"--skip"}, "^(TestA)$\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, code := runQuarantine(t, quarantineTree(t, tc.list, nil), tc.today, tc.args...)
			if code != 0 || out != tc.want {
				t.Errorf("%v: exit %d, got %q, want %q\n%s", tc.args, code, out, tc.want, errOut)
			}
		})
	}

	// An invalid line is exit 2 with its line number, for every mode, and prints no expression.
	for _, tc := range []struct{ name, line, why string }{
		{"four fields", "example.com/m/a\tTestA\t1\t2026-01-01", "want five tab-separated fields, got 4"},
		{"not a test name", "example.com/m/a\tthing\t1\t2026-01-01\t2026-01-02", "not a Go test function name"},
		{"no issue", "example.com/m/a\tTestA\tx\t2026-01-01\t2026-01-02", "not an issue number"},
		{"a date that does not parse", "example.com/m/a\tTestA\t1\t2026-02-29\t2026-03-01", "added is not a YYYY-MM-DD date"},
		{"a name twice", "example.com/m/b\tTestA\t1\t2026-01-01\t2026-01-02", "TestA is already on line 3"},
		// The list cannot switch off its own validator: not by its package, and not by its name in
		// another package, since -skip matches the name in every package of the call.
		{"a package of internal/repodocs", "example.com/m/internal/repodocs\tTestZ\t1\t2026-01-01\t2026-01-02",
			"internal/repodocs checks the documents and the process: its tests cannot be quarantined"},
		{"a subpackage of internal/repodocs", "example.com/m/internal/repodocs/sub\tTestZ\t1\t2026-01-01\t2026-01-02",
			"internal/repodocs checks the documents and the process"},
		{"a test of internal/repodocs in another package", "example.com/m/b\tTestPinnedCheck\t1\t2026-01-01\t2026-01-02",
			"TestPinnedCheck is a test of internal/repodocs, which cannot be quarantined"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := quarantineTree(t, header+a+tc.line+"\n", map[string]string{
				"internal/repodocs/pins_test.go": "package repodocs\r\n\r\nimport \"testing\"\r\n\r\nfunc TestPinnedCheck(t *testing.T) {}\r\n",
			})
			for _, mode := range []string{"--skip", "--list", "--count", "--expired", "--run"} {
				out, errOut, code := runQuarantine(t, dir, "2026-01-10", mode)
				if code != 2 || !strings.Contains(errOut, "test/quarantine.tsv:4: "+tc.why) || strings.Contains(out, "^(") {
					t.Errorf("%s: exit %d, stdout %q, stderr %q", mode, code, out, errOut)
				}
			}
		})
	}
	// A bad call, a bad clock and a missing list are exit 2 too.
	dir := quarantineTree(t, header+a, nil)
	for _, args := range [][]string{nil, {"--nope"}, {"--count", "extra"}} {
		if _, _, code := runQuarantine(t, dir, "2026-01-10", args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
	if _, errOut, code := runQuarantine(t, dir, "tomorrow", "--skip"); code != 2 || !strings.Contains(errOut, "not a YYYY-MM-DD date") {
		t.Errorf("a bad --today: exit %d, %q", code, errOut)
	}
	if _, _, code := runQuarantine(t, dir, "", "--today"); code != 2 {
		t.Errorf("--today without a date: exit %d, want 2", code)
	}
	// The clock is an option the tests pass, never the environment: an exported date that would keep
	// a lapsed entry active is ignored, and the real clock decides.
	lapsed := quarantineTree(t, "example.com/m/a\tTestA\t1\t2000-01-01\t2000-01-31\n", nil)
	for _, mode := range []string{"--skip", "--count", "--list"} {
		out, errOut, code := runQuarantineEnv(t, lapsed, []string{"QUARANTINE_TODAY=2000-01-10", "TODAY=2000-01-10"}, "", mode)
		if code != 0 || (out != "" && out != "0\n") {
			t.Errorf("%s with an exported past date: exit %d, %q, %q", mode, code, out, errOut)
		}
	}
	if err := os.Remove(filepath.Join(dir, "test", "quarantine.tsv")); err != nil {
		t.Fatal(err)
	}
	if _, errOut, code := runQuarantine(t, dir, "2026-01-10", "--skip"); code != 2 || !strings.Contains(errOut, "cannot read test/quarantine.tsv") {
		t.Errorf("a missing list: exit %d, %q", code, errOut)
	}
}

// A toy module with one quarantined test of two: the shard plan holds only the other, quarantine.sh
// --run runs the quarantined one alone and reports it, and once the entry lapses it is back.
func TestQuarantineDropsTheTestFromTheShardPlan(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	// The real clock decides (the script reads no date from the environment): one entry far in the
	// future, active, and the same one lapsed long ago. The exported QUARANTINE_TODAY below would
	// keep the lapsed one active if anything read it.
	const (
		active = "example.com/toy/p\tTestFlaky\t7\t2000-01-01\t9999-12-31\n"
		lapsed = "example.com/toy/p\tTestFlaky\t7\t2000-01-01\t2000-01-15\n"
	)
	dir := quarantineTree(t, active, map[string]string{
		"scripts/test-shard.sh": readFile(t, repoRoot(t), "scripts/test-shard.sh"),
		"go.mod":                "module example.com/toy\n\ngo 1.22\n",
		"p/p_test.go": `package p

import (
	"os"
	"testing"
)

func TestKeep(t *testing.T) {
	if err := os.WriteFile(os.Getenv("KEPT"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFlaky(t *testing.T) { t.Fatal("flaky") }
`,
	})
	kept := filepath.Join(t.TempDir(), "kept")
	setList := func(list string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "test", "quarantine.tsv"), []byte(list), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) (string, int) {
		t.Helper()
		cmd := exec.Command("bash", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "QUARANTINE_TODAY=2000-01-10", "KEPT="+kept, "GOWORK=off", "GOFLAGS=")
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatal(err)
			}
			code = ee.ExitCode()
		}
		return string(out), code
	}
	// The last is the build host's Windows retry and probe (scripts/signoff.sh): the leg's own
	// call over the import paths of the failed tests' packages.
	for _, args := range [][]string{{"1", "0"}, {"1", "all"}, {"4", "all", "example.com/toy/p"}} {
		_ = os.Remove(kept)
		out, code := run(append([]string{"scripts/test-shard.sh"}, args...)...)
		if code != 0 || !strings.Contains(out, "quarantine: 1 test(s) skipped (test/quarantine.tsv)\n") {
			t.Errorf("test-shard.sh %v: the quarantined test must be out of the plan, and the shard say so: exit %d\n%s", args, code, out)
		}
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("test-shard.sh %v: the other test of the package must still run", args)
		}
	}
	setList(lapsed)
	for _, args := range [][]string{{"1", "0"}, {"4", "all", "example.com/toy/p"}} {
		if out, code := run(append([]string{"scripts/test-shard.sh"}, args...)...); code != 1 || !strings.Contains(out, "--- FAIL: TestFlaky") || strings.Contains(out, "quarantine:") {
			t.Errorf("test-shard.sh %v: a lapsed entry is back in the plan: exit %d\n%s", args, code, out)
		}
	}
	setList(active)
	out, code := run("scripts/quarantine.sh", "--run")
	if code != 0 || !strings.Contains(out, "quarantine: example.com/toy/p.TestFlaky FAIL\n") ||
		!strings.HasSuffix(out, "quarantine: 1 entries, 1 failed\n") {
		t.Errorf("--run: exit %d\n%s", code, out)
	}
	setList("x\n")
	if out, code := run("scripts/test-shard.sh", "1", "all"); code != 2 {
		t.Errorf("a list that does not parse fails the shard with 2: exit %d\n%s", code, out)
	}
}

// The four make targets add the -skip to each `go test` and say how many they skipped; a list
// that does not parse fails them before any test runs. A fake go logs its arguments.
func TestQuarantineMakeTargetsSkip(t *testing.T) {
	needBash(t)
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not on PATH")
	}
	// The real clock decides: the entries expire far in the future, the lapsed ones long ago.
	const two = "example.com/m/a\tTestA\t1\t2000-01-01\t9999-12-31\nexample.com/m/b\tTestB\t2\t2000-01-01\t9999-12-31\n"
	const twoLapsed = "example.com/m/a\tTestA\t1\t2000-01-01\t2000-01-31\nexample.com/m/b\tTestB\t2\t2000-01-01\t2000-01-31\n"
	setup := func(list string) (string, string) {
		dir := quarantineTree(t, list, map[string]string{
			"Makefile":             readFile(t, repoRoot(t), "Makefile"),
			"internal/x/x_test.go": "package x\n\nimport \"testing\"\n\nfunc TestRaceX(t *testing.T) {}\n",
			"bin/go":               "#!/bin/sh\n[ \"$1\" = test ] || exit 0\nprintf '%s|' \"$@\" >> \"$GO_LOG\"; echo >> \"$GO_LOG\"\n",
			"bin/gofmt":            "#!/bin/sh\nexit 0\n",
			"internal/x/x.go":      "package x\n",
		})
		return dir, filepath.Join(dir, "go.log")
	}
	makeIn := func(dir, goLog string, args ...string) (string, error) {
		cmd := exec.Command("make", append([]string{"-s", "-C", dir}, args...)...)
		// make imports the environment as variables: what the outer run exported (`make check
		// E2E=1` exports E2E and CHECK_E2E, a rerun RERUN_PKGS) would change the toy targets, and
		// check-run would start the e2e scenarios the toy tree does not have.
		var env []string
		for _, kv := range os.Environ() {
			name, _, _ := strings.Cut(kv, "=")
			switch {
			case strings.HasPrefix(name, "MAKE"), name == "MFLAGS", name == "PATH",
				strings.HasPrefix(name, "E2E"), strings.HasPrefix(name, "CHECK_"), strings.HasPrefix(name, "RERUN_"), name == "RACE_RUN":
				continue
			}
			env = append(env, kv)
		}
		cmd.Env = append(env, "PATH="+filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"),
			"GO_LOG="+goLog)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	targets := [][]string{{"test"}, {"race"}, {"test-rerun", "RERUN_PKGS=example.com/m/a,example.com/m/b"}, {"check-run", "CHECK_PKGS=./internal/x"}}

	dir, goLog := setup("# none yet\n")
	for _, target := range targets {
		out, err := makeIn(dir, goLog, target...)
		if err != nil || strings.Contains(out, "quarantine:") {
			t.Errorf("make %v with an empty list: %v\n%s", target, err, out)
		}
	}
	if log := readFileAbs(t, goLog); strings.Contains(log, "-skip") || strings.Count(log, "\n") != 5 {
		t.Errorf("no -skip without an entry, and every go test ran:\n%s", log)
	}

	dir, goLog = setup(two)
	for _, target := range targets {
		out, err := makeIn(dir, goLog, target...)
		if err != nil || !strings.Contains(out, "quarantine: 2 test(s) skipped (test/quarantine.tsv)\n") {
			t.Errorf("make %v: %v\n%s", target, err, out)
		}
		// scripts/test-failures.sh tells a race failure by the line `race` prints: it still
		// starts with `go test -race `.
		if target[0] == "race" && !regexp.MustCompile(`(?m)^go test -race -count=1 -run '[^']+' -skip '\^\(TestA\|TestB\)\$' \./internal/x *$`).MatchString(out) {
			t.Errorf("make race no longer prints its go test line in the shape test-failures.sh reads:\n%s", out)
		}
	}
	calls := strings.Split(strings.TrimSpace(readFileAbs(t, goLog)), "\n")
	if len(calls) != 5 {
		t.Fatalf("want the five go test calls of test, race, test-rerun and check-run, got:\n%s", strings.Join(calls, "\n"))
	}
	for _, c := range calls {
		if !strings.Contains(c, "|-skip|^(TestA|TestB)$|") {
			t.Errorf("a go test without the skip: %s", c)
		}
	}

	// Lapsed: no skip, no note, even with a past date exported as a variable make imports from the
	// environment (QUARANTINE_TODAY, the clock override this script once read from there).
	dir, goLog = setup(twoLapsed)
	if out, err := makeIn(dir, goLog, "test", "QUARANTINE_TODAY=2000-01-10"); err != nil || strings.Contains(out, "quarantine:") || strings.Contains(readFileAbs(t, goLog), "-skip") {
		t.Errorf("a lapsed list skips nothing: %v\n%s", err, out)
	}
	t.Setenv("QUARANTINE_TODAY", "2000-01-10")
	_ = os.Remove(goLog)
	if out, err := makeIn(dir, goLog, "test"); err != nil || strings.Contains(out, "quarantine:") || strings.Contains(readFileAbs(t, goLog), "-skip") {
		t.Errorf("a lapsed list skips nothing with QUARANTINE_TODAY exported: %v\n%s", err, out)
	}

	dir, goLog = setup(two + "not a line\n")
	for _, target := range targets {
		out, err := makeIn(dir, goLog, target...)
		if err == nil || !strings.Contains(out, "test/quarantine.tsv:3:") {
			t.Errorf("make %v must fail on a list that does not parse: %v\n%s", target, err, out)
		}
	}
	if _, err := os.Stat(goLog); err == nil {
		t.Errorf("no go test may run on a list that does not parse:\n%s", readFileAbs(t, goLog))
	}
}

// Every `go test` that runs tests from the Makefile, a script or a workflow carries the quarantine,
// so a new call site cannot forget it. The exemptions are by name, and each must still match.
func TestEveryGoTestCallSiteSkipsTheQuarantine(t *testing.T) {
	root := repoRoot(t)
	call := regexp.MustCompile(`(^|[\s;&|("'@])go test(\s|$)`)
	exempt := []struct{ file, target, sub, why string }{
		{"", "", "go test -list ", "lists the tests, runs none"},
		{"Makefile", "decisions-index", "", "a generator: one named test with -update"},
		{"Makefile", "codemap", "", "a generator: one named test with -update"},
		{"Makefile", "runs-where", "", "a generator: one named test with -update"},
		{"Makefile", "settings-example", "", "a generator: one named test"},
		{"scripts/test-shard.sh", "", `-run "$re"`, "drops the quarantined names from its plan instead"},
		{"scripts/quarantine.sh", "", `-run "^$name\$"`, "runs the quarantined tests, on purpose"},
	}
	used := make([]bool, len(exempt))
	files := []string{"Makefile"}
	for _, glob := range []string{"scripts/*.sh", ".github/workflows/*.yml"} {
		m, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(glob)))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range m {
			rel, _ := filepath.Rel(root, f)
			files = append(files, filepath.ToSlash(rel))
		}
	}
	target := regexp.MustCompile(`^([A-Za-z0-9_.-]+):`)
	sites := 0
	for _, file := range files {
		cur := ""
		for i, line := range strings.Split(readFile(t, root, file), "\n") {
			if file == "Makefile" {
				if m := target.FindStringSubmatch(line); m != nil {
					cur = m[1]
				}
			}
			trimmed := strings.TrimLeft(line, " \t@-")
			if strings.HasPrefix(trimmed, "#") || !call.MatchString(line) {
				continue
			}
			skipped := false
			for j, e := range exempt {
				if (e.file == "" || e.file == file) && (e.target == "" || e.target == cur) && (e.sub == "" || strings.Contains(line, e.sub)) {
					used[j], skipped = true, true
				}
			}
			if skipped {
				continue
			}
			sites++
			want := "-skip"
			if file == "Makefile" {
				want = "$(QUARANTINE_SKIP)"
			}
			if !strings.Contains(line, want) {
				t.Errorf("%s:%d runs go test without the quarantine (%s), or exempt it here by name:\n%s", file, i+1, want, line)
			}
		}
	}
	for j, e := range exempt {
		if !used[j] {
			t.Errorf("the exemption %+v matches no line any more: remove it", e)
		}
	}
	if sites < 6 {
		t.Errorf("only %d go test call sites found: the test reads nothing", sites)
	}
}

// The clock override of quarantine.sh is for its tests only: no caller passes it, and none reads it
// from the environment, where an exported date would keep lapsed entries out of a gate.
func TestNoGateCallerOverridesTheQuarantineClock(t *testing.T) {
	root := repoRoot(t)
	files := []string{"Makefile"}
	for _, glob := range []string{"scripts/*.sh", ".github/workflows/*.yml"} {
		m, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(glob)))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range m {
			rel, _ := filepath.Rel(root, f)
			files = append(files, filepath.ToSlash(rel))
		}
	}
	for _, file := range files {
		body := readFile(t, root, file)
		if file == "scripts/quarantine.sh" {
			if strings.Contains(body, "QUARANTINE_TODAY") || strings.Contains(body, "${TODAY") || strings.Contains(body, "$TODAY") {
				t.Errorf("%s reads its clock from the environment", file)
			}
			continue
		}
		for _, word := range []string{"--today", "QUARANTINE_TODAY"} {
			if strings.Contains(body, word) {
				t.Errorf("%s names %s: the quarantine's clock is overridden by its tests only", file, word)
			}
		}
	}
	if len(files) < 10 {
		t.Errorf("only %d files read: the test reads nothing", len(files))
	}
}

// The `quarantine` job runs on push to main only, outside every verdict: not in HOSTED_JOBS nor in
// main-status's needs, continue-on-error, and its issues: write token only in the step that comments.
func TestQuarantineJobIsOutsideTheVerdict(t *testing.T) {
	root := repoRoot(t)
	var wf struct {
		Jobs map[string]struct {
			If              string            `yaml:"if"`
			ContinueOnError any               `yaml:"continue-on-error"`
			Permissions     map[string]string `yaml:"permissions"`
			Needs           any               `yaml:"needs"`
			Env             map[string]any    `yaml:"env"`
			Steps           []struct {
				Uses string         `yaml:"uses"`
				Run  string         `yaml:"run"`
				Env  map[string]any `yaml:"env"`
				With map[string]any `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readFile(t, root, ".github/workflows/ci.yml")), &wf); err != nil {
		t.Fatal(err)
	}
	job, ok := wf.Jobs["quarantine"]
	if !ok {
		t.Fatal("ci.yml has no quarantine job")
	}
	if job.If != "github.event_name == 'push'" {
		t.Errorf("quarantine.if = %q: push to main only, never a dispatched gate", job.If)
	}
	if fmt.Sprint(job.ContinueOnError) != "true" {
		t.Errorf("quarantine must be continue-on-error, got %v", job.ContinueOnError)
	}
	if fmt.Sprint(job.Permissions) != "map[contents:read issues:write]" {
		t.Errorf("quarantine.permissions = %v, want exactly contents: read, issues: write", job.Permissions)
	}
	if needsOf(job.Needs) != nil || len(job.Env) != 0 {
		t.Errorf("quarantine needs nothing and has no job env: %v %v", job.Needs, job.Env)
	}
	for _, n := range needsOf(wf.Jobs["main-status"].Needs) {
		if n == "quarantine" {
			t.Error("main-status must not need quarantine: a quarantined test never turns main red")
		}
	}
	// The workflow's page names the job, its trigger and its token, and points at the rules.
	bullet := ""
	for _, line := range strings.Split(readFile(t, root, "docs/deployment.md"), "\n") {
		if strings.HasPrefix(line, "- **`ci.yml`**") {
			bullet = line
		}
	}
	for _, want := range []string{"a `quarantine` job runs on push to `main` only", "`issues: write`", "(`testing.md` §Go tests)"} {
		if !strings.Contains(bullet, want) {
			t.Errorf("the ci.yml bullet of docs/deployment.md must name the quarantine job: missing %q", want)
		}
	}
	m := regexp.MustCompile(`(?m)^HOSTED_JOBS="([^"]+)"$`).FindStringSubmatch(readFile(t, root, "scripts/signoff.sh"))
	if m == nil || strings.Contains(" "+m[1]+" ", " quarantine ") {
		t.Errorf("HOSTED_JOBS must not hold quarantine: %v", m)
	}
	var runs, comments int
	for i, st := range job.Steps {
		token := false
		for _, v := range st.Env {
			if strings.Contains(fmt.Sprint(v), "github.token") || strings.Contains(fmt.Sprint(v), "secrets.") {
				token = true
			}
		}
		switch {
		case strings.HasPrefix(st.Uses, "actions/checkout@"):
			if fmt.Sprint(st.With["persist-credentials"]) != "false" {
				t.Errorf("step %d: the checkout keeps no credentials", i+1)
			}
		case strings.Contains(st.Run, "scripts/quarantine.sh --run"):
			runs++
			if token || !strings.Contains(st.Run, "GITHUB_STEP_SUMMARY") {
				t.Errorf("step %d runs the tests: no token, and its lines go to the job summary", i+1)
			}
		case strings.Contains(st.Run, "scripts/quarantine.sh --expired"):
			comments++
			for _, want := range []string{
				`note="quarantine expired on $expires: the test is back in the gate; fix it, or renew the line with a reason"`,
				`grep -qF "quarantine expired on $expires:" <<<"$bodies"`,
				`gh issue comment "$issue"`,
			} {
				if !strings.Contains(st.Run, want) {
					t.Errorf("step %d comments once per expiry date: lacks %q", i+1, want)
				}
			}
		case token:
			t.Errorf("step %d holds the token and is not the comment step", i+1)
		}
	}
	if runs != 1 || comments != 1 {
		t.Errorf("want one step that runs the entries and one that comments, got %d and %d", runs, comments)
	}
}
