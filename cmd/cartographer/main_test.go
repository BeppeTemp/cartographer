package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/repodocs"
)

func TestDiscoverKBPaths(t *testing.T) {
	base := t.TempDir()

	// Create subdirs: two regular, one dotfile, one file (not dir).
	dirs := []string{"alpha", "beta"}
	for _, d := range dirs {
		if err := os.Mkdir(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// dotfile dir — must be excluded
	if err := os.Mkdir(filepath.Join(base, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	// regular file — must be excluded
	if err := os.WriteFile(filepath.Join(base, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := discoverKBPaths(base, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sort.Strings(got)
	want := []string{
		filepath.Join(base, "alpha"),
		filepath.Join(base, "beta"),
	}
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("got %d paths, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("path[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestDiscoverKBPathsMissingDirCreated covers the D83 fix: a missing data
// dir must not fail server startup (launchd install races data dir
// creation with the first serve, and a removed data dir must not
// crash-loop the service). It is created and treated as empty.
func TestDiscoverKBPathsMissingDirCreated(t *testing.T) {
	base := filepath.Join(t.TempDir(), "does-not-exist-yet")

	got, err := discoverKBPaths(base, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty result, got %v", got)
	}
	if info, statErr := os.Stat(base); statErr != nil || !info.IsDir() {
		t.Fatalf("expected data dir to be created at %q, stat: %v", base, statErr)
	}
}

func TestDiscoverKBPathsEmpty(t *testing.T) {
	base := t.TempDir()
	got, err := discoverKBPaths(base, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty result, got %v", got)
	}
}

// withStdout redirects os.Stdout for the duration of f and returns what was written.
func withStdout(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old

	buf := make([]byte, 64*1024)
	n, _ := r.Read(buf)
	return string(buf[:n])
}

// withStderr redirects os.Stderr for the duration of f and returns what was
// written. Operator guidance and warnings go to stderr, so a test asserting on
// them cannot use withStdout.
func withStderr(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	f()
	w.Close()
	os.Stderr = old

	buf := make([]byte, 64*1024)
	n, _ := r.Read(buf)
	return string(buf[:n])
}

func TestRunNoArgsPrintsUsage(t *testing.T) {
	out := withStdout(t, func() {
		if code := run(nil); code != 0 {
			t.Errorf("run(nil) = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "Usage:") {
		t.Errorf("run(nil) output = %q, want it to contain \"Usage:\"", out)
	}
}

func TestRunHelp(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"-h"}, {"--help"}} {
		out := withStdout(t, func() {
			if code := run(args); code != 0 {
				t.Errorf("run(%v) = %d, want 0", args, code)
			}
		})
		if !strings.Contains(out, "serve") {
			t.Errorf("run(%v) output = %q, want it to list \"serve\"", args, out)
		}
	}
}

func TestRunVersionDispatch(t *testing.T) {
	origVersionFn := versionFn
	defer func() { versionFn = origVersionFn }()
	called := false
	versionFn = func() int {
		called = true
		return 7
	}

	if code := run([]string{"version"}); code != 7 {
		t.Errorf("run([version]) = %d, want 7", code)
	}
	if !called {
		t.Error("versionFn was not called")
	}
}

func TestRunVersionAliases(t *testing.T) {
	orig := versionFn
	defer func() { versionFn = orig }()
	called := 0
	versionFn = func() int { called++; return 0 }
	for _, args := range [][]string{{"version"}, {"--version"}} {
		if code := run(args); code != 0 {
			t.Fatalf("run(%v) = %d", args, code)
		}
	}
	if called != 2 {
		t.Fatalf("version calls = %d, want 2", called)
	}
}

func TestCommandSuggestionIsConservative(t *testing.T) {
	if got := commandSuggestion("agnts"); got != "agents" {
		t.Errorf("suggestion = %q", got)
	}
	if got := commandSuggestion("definitely-unrelated"); got != "" {
		t.Errorf("unrelated suggestion = %q", got)
	}
}

func TestUsageFits80Columns(t *testing.T) {
	out := withStdout(t, func() { printUsage(os.Stdout) })
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if len(line) > 80 {
			t.Errorf("help line is %d columns: %q", len(line), line)
		}
	}
}

func TestRunServeDispatch(t *testing.T) {
	origServeFn := serveFn
	defer func() { serveFn = origServeFn }()
	var gotArgs []string
	serveFn = func(args []string) int {
		gotArgs = args
		return 42
	}

	if code := run([]string{"serve", "--http", ":9090"}); code != 42 {
		t.Errorf("run([serve ...]) = %d, want 42", code)
	}
	want := []string{"--http", ":9090"}
	if len(gotArgs) != len(want) {
		t.Fatalf("serveFn args = %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Errorf("serveFn args[%d] = %q, want %q", i, gotArgs[i], want[i])
		}
	}
}

func TestRunClientCommandsDispatch(t *testing.T) {
	origAgents, origConnect, origDisconnect, origReconnect, origStatus, origSync, origService, origReindex, origDoctor := agentsFn, connectFn, disconnectFn, reconnectFn, statusFn, syncFn, serviceFn, reindexFn, doctorFn
	defer func() {
		agentsFn, connectFn, disconnectFn, reconnectFn, statusFn, syncFn, serviceFn, reindexFn, doctorFn = origAgents, origConnect, origDisconnect, origReconnect, origStatus, origSync, origService, origReindex, origDoctor
	}()

	cases := []struct {
		cmd string
		set func(fn func([]string) int)
	}{
		{"agents", func(fn func([]string) int) { agentsFn = fn }},
		{"connect", func(fn func([]string) int) { connectFn = fn }},
		{"disconnect", func(fn func([]string) int) { disconnectFn = fn }},
		{"reconnect", func(fn func([]string) int) { reconnectFn = fn }},
		{"status", func(fn func([]string) int) { statusFn = fn }},
		{"sync", func(fn func([]string) int) { syncFn = fn }},
		{"service", func(fn func([]string) int) { serviceFn = fn }},
		{"reindex", func(fn func([]string) int) { reindexFn = fn }},
		{"doctor", func(fn func([]string) int) { doctorFn = fn }},
	}
	for _, tc := range cases {
		var gotArgs []string
		tc.set(func(args []string) int {
			gotArgs = args
			return 5
		})
		if code := run([]string{tc.cmd, "--foo"}); code != 5 {
			t.Errorf("run([%s ...]) = %d, want 5", tc.cmd, code)
		}
		if len(gotArgs) != 1 || gotArgs[0] != "--foo" {
			t.Errorf("%s: args = %v, want [--foo]", tc.cmd, gotArgs)
		}
	}
}

func TestRunUnknownCommand(t *testing.T) {
	if code := run([]string{"bogus"}); code != 2 {
		t.Errorf("run([bogus]) = %d, want 2", code)
	}
}

func TestRunUnknownFlagAtRoot(t *testing.T) {
	if code := run([]string{"--kb", "/some/path"}); code != 2 {
		t.Errorf("run([--kb ...]) = %d, want 2", code)
	}
}

// setHome points os.UserHomeDir at dir on every platform: Go reads $HOME on
// unix and %USERPROFILE% on Windows, so a test that sets only one of the two
// silently keeps the real home directory on the other platform.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

// fileURL turns a local directory into a file:// remote git accepts on every
// platform. On Windows the path starts with a drive letter (C:\...), which
// needs forward slashes and a third slash before the drive, or git reads the
// drive letter as a hostname.
func fileURL(dir string) string {
	s := filepath.ToSlash(dir)
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return "file://" + s
}

// dispatchedCommands reads the string cases of `switch cmd` in run (main.go) with
// go/parser, so the usage table and the dispatch cannot drift without a test
// saying so (D1124). Flag-shaped cases are not commands.
func dispatchedCommands(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing main.go: %v", err)
	}
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "run" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}
			if id, ok := sw.Tag.(*ast.Ident); !ok || id.Name != "cmd" {
				return true
			}
			for _, st := range sw.Body.List {
				cc, _ := st.(*ast.CaseClause)
				for _, e := range cc.List {
					lit, ok := e.(*ast.BasicLit)
					if !ok {
						continue
					}
					name, err := strconv.Unquote(lit.Value)
					if err == nil && !strings.HasPrefix(name, "-") {
						out = append(out, name)
					}
				}
			}
			return false
		})
		return false
	})
	if len(out) == 0 {
		t.Fatal("no case found in the dispatch switch of run(): the check is not doing anything")
	}
	return out
}

// The usage table (subcommands), the dispatch switch of run() and the documented
// commands are three lists kept by hand; this compares them (D1124).
func TestCommandTableMatchesDispatchAndDocs(t *testing.T) {
	const internal = "hook" // dispatched, deliberately absent from the table (D353)

	dispatched := map[string]bool{}
	for _, n := range dispatchedCommands(t) {
		dispatched[n] = true
	}
	table := map[string]bool{}
	for _, c := range subcommands {
		table[c.name] = true
		if !dispatched[c.name] {
			t.Errorf("command %q is in the usage table but run() has no case for it", c.name)
		}
	}
	for n := range dispatched {
		if !table[n] && n != internal {
			t.Errorf("run() dispatches %q but the usage table (main.go) does not list it: add it, "+
				"or — if it is internal — document the exception next to hook", n)
		}
	}

	root, err := repodocs.Root()
	if err != nil {
		t.Fatalf("locating the repository root: %v", err)
	}
	read := func(rel string) string {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("reading %s: %v", rel, err)
		}
		return string(data)
	}
	docs := read("docs/configurator.md") + read("docs/deployment.md")
	for _, c := range subcommands {
		if !regexp.MustCompile(`cartographer ` + regexp.QuoteMeta(c.name) + `\b`).MatchString(docs) {
			t.Errorf("command %q is documented nowhere in docs/configurator.md or docs/deployment.md", c.name)
		}
	}

	files, err := repodocs.MarkdownFiles(root)
	if err != nil {
		t.Fatalf("collecting the markdown files: %v", err)
	}
	span := regexp.MustCompile("`([^`\n]+)`")
	invoke := regexp.MustCompile(`(?:^|[\s(])(?:\./bin/)?cartographer\s+([a-z][a-z0-9-]*)`)
	fenceLine := regexp.MustCompile(`^\s*(?:\$ )?(?:\./bin/)?cartographer\s+([a-z][a-z0-9-]*)`)
	cited := 0
	check := func(rel string, line int, word string) {
		cited++
		if !dispatched[word] {
			t.Errorf("%s:%d cites \"cartographer %s\" which is not a command (the commands are the cases of run() in "+
				"cmd/cartographer/main.go). Fix the text: reword a line that merely mentions the binary.", rel, line, word)
		}
	}
	for _, rel := range files {
		if strings.HasPrefix(rel, "docs/decisions/") { // history names commands since renamed
			continue
		}
		fenced := false
		for i, line := range strings.Split(read(rel), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "```") || strings.HasPrefix(strings.TrimSpace(line), "~~~") {
				fenced = !fenced
				continue
			}
			if fenced {
				if m := fenceLine.FindStringSubmatch(line); m != nil {
					check(rel, i+1, m[1])
				}
				continue
			}
			for _, sp := range span.FindAllStringSubmatch(line, -1) {
				if m := invoke.FindStringSubmatch(sp[1]); m != nil {
					check(rel, i+1, m[1])
				}
			}
		}
	}
	if cited == 0 {
		t.Fatal("no `cartographer <command>` cited anywhere: the check is not doing anything")
	}
}
