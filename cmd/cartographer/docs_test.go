package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/docskb"
	"github.com/BeppeTemp/cartographer/internal/testtree"
)

// runDocs runs `cartographer docs` with a private $HOME and $TMPDIR, so the
// tests also prove where it writes.
func runDocs(t *testing.T, stdin string, args ...string) (int, string, string) {
	t.Helper()
	old := mcpStdin
	mcpStdin = strings.NewReader(stdin)
	t.Cleanup(func() { mcpStdin = old })
	var code int
	var errOut string
	out := withStdout(t, func() { errOut = withStderr(t, func() { code = run(append([]string{"docs"}, args...)) }) })
	return code, out, errOut
}

// sharedDocs is one runner for the tests that do not check where `docs`
// writes: every invocation would otherwise materialize the whole tree again,
// which is slow on Windows. Its Close is a no-op; TestMain removes it.
var sharedDocs struct {
	once sync.Once
	r    *docskb.Runner
	err  error
}

// docsBaseTmp is $TMPDIR before any test redirects it: the shared tree must
// outlive the test that happens to build it.
var docsBaseTmp = os.TempDir()

type sharedRunner struct{ r *docskb.Runner }

func (s sharedRunner) Raw(ctx context.Context, m string, p map[string]any) (json.RawMessage, error) {
	return s.r.Raw(ctx, m, p)
}
func (sharedRunner) Close() error { return nil }

func closeSharedDocs() {
	if sharedDocs.r != nil {
		_ = sharedDocs.r.Close()
	}
}

func useSharedDocs(t *testing.T) {
	t.Helper()
	sharedDocs.once.Do(func() {
		for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
			old, had := os.LookupEnv(k)
			os.Setenv(k, docsBaseTmp)
			defer func() {
				if had {
					os.Setenv(k, old)
				} else {
					os.Unsetenv(k)
				}
			}()
		}
		sharedDocs.r, sharedDocs.err = docskb.NewRunner(version)
	})
	if sharedDocs.err != nil {
		t.Fatal(sharedDocs.err)
	}
	old := newDocsRunner
	newDocsRunner = func(string) (docsRunner, error) { return sharedRunner{sharedDocs.r}, nil }
	t.Cleanup(func() { newDocsRunner = old })
}

// docsEnv gives a test a private $HOME and $TMPDIR and the shared runner.
func docsEnv(t *testing.T) (home, tmp string) {
	t.Helper()
	useSharedDocs(t)
	home, tmp = t.TempDir(), t.TempDir()
	setHome(t, home)
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, tmp)
	}
	return home, tmp
}

func TestDocsListDescribeSearchAndRead(t *testing.T) {
	docsEnv(t)
	code, out, e := runDocs(t, "", "--list")
	if code != 0 || !strings.Contains(out, "search\t") || !strings.Contains(out, "graph_context\t") || strings.Contains(out, "concept_write") {
		t.Fatalf("--list: %d %q %q", code, out, e)
	}
	code, out, e = runDocs(t, "", "--describe", "search,concept_read")
	var arr []map[string]any
	if code != 0 || json.Unmarshal([]byte(out), &arr) != nil || len(arr) != 2 {
		t.Fatalf("--describe: %d %q %q", code, out, e)
	}
	if code, _, e = runDocs(t, "", "--describe", "nope"); code != 2 || !strings.Contains(e, "cartographer docs --list") {
		t.Errorf("--describe nope: %d %q", code, e)
	}

	code, out, e = runDocs(t, "", "search", "query=workspace bind", "limit=3", "--output", "json")
	if code != 0 {
		t.Fatalf("search: %d %q", code, e)
	}
	var res struct {
		Content []struct{ Text string } `json:"content"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || len(res.Content) == 0 {
		t.Fatalf("--output json is not an MCP result: %v %q", err, out)
	}
	code, out, e = runDocs(t, "", "graph_context", "query=how does workspace binding work?")
	if code != 0 || !strings.Contains(out, "configurator") {
		t.Fatalf("graph_context: %d %q %q", code, out, e)
	}
	code, out, e = runDocs(t, "", "concept_read", "id=using-cartographer/configurator")
	if code != 0 || out == "" {
		t.Fatalf("concept_read: %d %q %q", code, out, e)
	}
}

func TestDocsRefusesWritesAndUnknownTools(t *testing.T) {
	docsEnv(t)
	code, _, e := runDocs(t, "", "concept_write", "id=x", "content=y")
	if code == 0 || !strings.Contains(e, "read_only") {
		t.Errorf("a write tool: exit %d %q, want a refusal", code, e)
	}
	if code, _, _ = runDocs(t, "", "no_such_tool"); code == 0 {
		t.Error("an unknown tool exited 0")
	}
	if code, _, _ = runDocs(t, "", "--list", "--describe", "search"); code != 2 {
		t.Errorf("two modes: exit %d, want 2", code)
	}
	if code, _, _ = runDocs(t, "", "--json", "{}"); code != 2 {
		t.Errorf("--json without a tool: exit %d, want 2", code)
	}
}

func TestDocsBatch(t *testing.T) {
	docsEnv(t)
	in := `{"tool":"search","arguments":{"query":"workspace bind","limit":2},"id":"a"}` + "\n" +
		`{"tool":"concept_write","arguments":{"id":"x"},"id":"b"}` + "\n"
	code, out, e := runDocs(t, in, "--batch", "-")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if code == 0 || len(lines) != 2 || !strings.Contains(lines[0], `"ok":true`) || strings.Contains(lines[1], `"ok":true`) {
		t.Fatalf("batch: %d %q %q", code, out, e)
	}
}

func TestDocsNoArgumentsPrintsTheStartHere(t *testing.T) {
	docsEnv(t)
	code, out, _ := runDocs(t, "")
	if code != 0 || !strings.Contains(out, `cartographer docs graph_context query="<question>"`) || !strings.Contains(out, "concept_read id=<id>") {
		t.Errorf("%d %q", code, out)
	}
}

func TestDocsExport(t *testing.T) {
	docsEnv(t)
	base := t.TempDir()
	for _, dir := range []string{filepath.Join(base, "missing"), filepath.Join(base, "empty")} {
		if filepath.Base(dir) == "empty" {
			if err := os.Mkdir(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		code, out, e := runDocs(t, "", "export", dir)
		if code != 0 || !strings.Contains(out, dir) {
			t.Fatalf("export %s: %d %q %q", dir, code, out, e)
		}
		if _, err := os.Stat(filepath.Join(dir, "data")); err != nil {
			t.Errorf("no data tree in %s: %v", dir, err)
		}
	}
	code, _, e := runDocs(t, "", "export", filepath.Join(base, "empty")) // now non-empty
	if code != 2 || !strings.Contains(e, "not empty") {
		t.Errorf("non-empty dir: %d %q", code, e)
	}
	if code, _, _ = runDocs(t, "", "export"); code != 2 {
		t.Errorf("export without a dir: %d", code)
	}
}

// Weaker than TestCallWritesNothing, by design (D-new-docs-kb): the tools keep
// state beside a KB that opens on a directory, so a run writes its own temp
// directory, and nothing is left of it. $HOME stays untouched, even read-only,
// and no network is reached (the proxy variables point nowhere).
func TestDocsWritesOnlyItsTempDir(t *testing.T) {
	home, tmp := docsEnv(t)
	// The one test on the real path: a runner of its own per invocation.
	newDocsRunner = func(v string) (docsRunner, error) { return docskb.NewRunner(v) }
	cwd := t.TempDir()
	t.Chdir(cwd)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	snap := func() []testtree.Tree {
		var all []testtree.Tree
		for _, d := range []string{home, cwd, tmp} {
			tr, err := testtree.Capture(d)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, tr)
		}
		return all
	}
	before := snap()
	for _, args := range [][]string{{"--list"}, {"search", "query=workspace"}, {"concept_write", "id=x"}, {"--describe", "nope"}, {"--bogus"}} {
		runDocs(t, "", args...)
	}
	if !reflect.DeepEqual(before, snap()) {
		t.Error("cartographer docs left a file under $HOME, the working directory or $TMPDIR")
	}
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("the read-only HOME half needs POSIX permissions and a non-root user")
	}
	if err := os.Chmod(home, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o755) })
	if code, _, e := runDocs(t, "", "search", "query=workspace"); code != 0 {
		t.Errorf("read-only HOME: exit %d: %s", code, e)
	}
}
