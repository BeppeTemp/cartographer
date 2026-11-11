package star

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

// fileEnv is a temporary home with its config and cache directories.
type fileEnv struct {
	home, config, cache string
	perms               *core.Permissions
	readOnly            bool
}

func newFileEnv(t *testing.T, read, write []string) *fileEnv {
	t.Helper()
	home := t.TempDir()
	e := &fileEnv{home: home, config: filepath.Join(home, "cfgdir"), cache: filepath.Join(home, "cachedir")}
	e.perms = &core.Permissions{Files: &core.Files{Read: read, Write: write}}
	return e
}

func (e *fileEnv) run(t *testing.T, body string) (Result, error) {
	t.Helper()
	return e.runEnv(t, body, nil)
}

// runEnv is run with environment variables the script's anchors read.
func (e *fileEnv) runEnv(t *testing.T, body string, env map[string]string) (Result, error) {
	t.Helper()
	src := "def go(a):\n    " + strings.ReplaceAll(strings.TrimSpace(body), "\n", "\n    ") + "\ntool(\"go\", go, \"go\")\n"
	r := toolReq("go", `{}`)
	r.Permissions = e.perms
	r.ReadOnly = e.readOnly
	r.Host = Host{Home: e.home, ConfigDir: e.config, CacheDir: e.cache,
		LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok }}
	return mustLoad(t, src).call(context.Background(), r, callOpts{})
}

func (e *fileEnv) put(t *testing.T, rel, data string) string {
	t.Helper()
	p := filepath.Join(e.home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFilesReadWrite(t *testing.T) {
	e := newFileEnv(t, []string{"{home}/**"}, []string{"{home}/**"})
	if _, err := e.run(t, `files.write("{home}/notes/new/a.txt", "héllo")`); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(e.home, "notes", "new", "a.txt"))
	if err != nil || string(got) != "héllo" {
		t.Fatalf("written %q, %v", got, err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(filepath.Join(e.home, "notes", "new", "a.txt")); fi.Mode().Perm() != 0o600 {
			t.Errorf("a new file is %v, want 0600", fi.Mode().Perm())
		}
	}
	res, err := e.run(t, `return files.read("{home}/notes/new/a.txt")`)
	if err != nil || !strings.Contains(res.Text, "héllo") {
		t.Fatalf("read %q, %v", res.Text, err)
	}
	if _, err := e.run(t, `files.write("{home}/b.bin", b"\x00\xff")`); err != nil {
		t.Fatal(err)
	}
	if res, err = e.run(t, `return len(files.read("{home}/b.bin", binary=True))`); err != nil || strings.TrimSpace(res.Text) != "2" {
		t.Fatalf("binary read %q, %v", res.Text, err)
	}
	ce := callErr(t, mustErr(e.run(t, `files.read("{home}/b.bin")`)), Failed)
	if !strings.Contains(ce.Msg, "UTF-8") || !strings.Contains(ce.Msg, "{home}/b.bin") {
		t.Errorf("invalid UTF-8 error %q", ce.Msg)
	}
}

func mustErr(_ Result, err error) error { return err }

func TestFilesAtomicReplaceKeepsMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	e := newFileEnv(t, nil, []string{"{home}/**"})
	p := e.put(t, "x.txt", "old")
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := e.run(t, `files.write("{home}/x.txt", "new")`); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if b, _ := os.ReadFile(p); string(b) != "new" || fi.Mode().Perm() != 0o640 {
		t.Errorf("content %q mode %v", b, fi.Mode().Perm())
	}
}

// Every place the hard deny list names, with the broadest grant the manifest
// validator lets through ({home}/**): read and write are both refused, and
// nothing is created.
func TestFilesHardDenyList(t *testing.T) {
	e := newFileEnv(t, []string{"{home}/**"}, []string{"{home}/**"})
	// The names Cartographer keeps: a new ".cartographer-*" file added later is
	// covered by the prefix rule, so this list is documentation of today's.
	paths := []string{
		".ssh/config", ".gnupg/pubring.kbx", ".git/hooks/pre-commit", "proj/.git/config", ".GIT/config",
		".cartographer.yaml", ".cartographer-sync.lock.json", ".cartographer-client.lock", ".cartographer-token",
		".cartographer-state.json", ".cartographer-variants.json", "kb/.cartographer/index.md", ".Cartographer-Future",
		".config/cartographer/server.yaml", ".config/Cartographer/server.yaml", "cfgdir/cartographer/x", "cachedir/cartographer/plugins/p/plugin.star",
	}
	for _, rel := range paths {
		for _, call := range []string{"files.read", `files.write`} {
			arg := fmt.Sprintf(`%s("{home}/%s"`, call, rel)
			if call == "files.write" {
				arg += `, "x"`
			}
			ce := callErr(t, mustErr(e.run(t, arg+")")), Denied)
			if !strings.Contains(ce.Msg, "denied") {
				t.Errorf("%s: %q", rel, ce.Msg)
			}
		}
		if _, err := os.Lstat(filepath.Join(e.home, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s was created", rel)
		}
	}
	if _, err := e.run(t, `files.remove("{home}/.ssh/config")`); err == nil {
		t.Error("remove inside .ssh")
	}
	// The glob never enters them either.
	e.put(t, ".ssh/id", "k")
	e.put(t, ".cartographer.yaml", "c")
	e.put(t, "ok/a.txt", "a")
	res, err := e.run(t, `return files.glob("{home}/**")`)
	if err != nil || strings.Contains(res.Text, ".ssh") || strings.Contains(res.Text, ".cartographer") || !strings.Contains(res.Text, "{home}/ok/a.txt") {
		t.Errorf("glob %q, %v", res.Text, err)
	}
}

func TestFilesPathForms(t *testing.T) {
	e := newFileEnv(t, []string{"{home}/**"}, []string{"{home}/**"})
	e.put(t, "a.txt", "a")
	for _, p := range []string{
		"/etc/hosts", "~/a.txt", "{home}/../a.txt", "{home}//a.txt", "{home}/./a.txt", `{home}\\a.txt`,
		"{home}/a:b", "{home}/NUL", "{home}/com1.txt", "{home}/aux", "{home}/a.txt.", "{home}/a ", "a.txt",
		"{home}", "{home}/", "{nope}/a.txt", "{home}/{home}/a", "{home}/*.txt", "{home}/a\x00",
	} {
		callErr(t, mustErr(e.run(t, fmt.Sprintf("files.read(%q)", p))), Denied)
	}
}

func TestFilesNeedTheirOwnGrant(t *testing.T) {
	e := newFileEnv(t, []string{"{home}/**"}, nil)
	e.put(t, "a.txt", "a")
	callErr(t, mustErr(e.run(t, `files.write("{home}/b.txt", "x")`)), Denied)
	callErr(t, mustErr(e.run(t, `files.remove("{home}/a.txt")`)), Denied)
	w := newFileEnv(t, nil, []string{"{home}/**"})
	callErr(t, mustErr(w.run(t, `files.read("{home}/a.txt")`)), Denied)
	callErr(t, mustErr(w.run(t, `files.glob("{home}/*")`)), Denied)
	n := newFileEnv(t, nil, nil)
	n.perms = nil
	callErr(t, mustErr(n.run(t, `files.read("{home}/a.txt")`)), Denied)
}

func TestFilesOutsideTheGrant(t *testing.T) {
	e := newFileEnv(t, []string{"{home}/notes/**", "{home}/one.txt"}, []string{"{home}/notes/*.md"})
	e.put(t, "notes/a.md", "a")
	e.put(t, "other/a.md", "o")
	e.put(t, "one.txt", "1")
	for _, call := range []string{`files.read("{home}/other/a.md")`, `files.write("{home}/notes/sub/a.md", "x")`, `files.write("{home}/notes/a.txt", "x")`, `files.remove("{home}/one.txt")`} {
		callErr(t, mustErr(e.run(t, call)), Denied)
	}
	for _, call := range []string{`files.read("{home}/notes/a.md")`, `files.read("{home}/one.txt")`, `files.write("{home}/notes/b.md", "x")`} {
		if _, err := e.run(t, call); err != nil {
			t.Errorf("%s: %v", call, err)
		}
	}
	res, err := e.run(t, `return files.glob("{home}/*/*")`)
	if err != nil || strings.Contains(res.Text, "other") || !strings.Contains(res.Text, "{home}/notes/a.md") {
		t.Errorf("glob outside the grant: %q, %v", res.Text, err)
	}
}

func TestFilesRefuseLinks(t *testing.T) {
	e := newFileEnv(t, []string{"{home}/**"}, []string{"{home}/**"})
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.put(t, "real/a.txt", "a")
	link := func(target, name string) {
		if err := os.Symlink(target, filepath.Join(e.home, name)); err != nil {
			t.Skipf("no symlinks here: %v", err)
		}
	}
	link(outside, "dirlink")                                    // a link component
	link(filepath.Join(outside, "secret"), "leaflink")          // a link leaf, outside
	link(filepath.Join(e.home, "real", "a.txt"), "leaflink-in") // a link leaf, inside the grant
	link(filepath.Join(e.home, ".ssh"), "sshlink")              // a link into a denied place
	if err := os.MkdirAll(filepath.Join(e.home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, call := range []string{
		`files.read("{home}/dirlink/secret")`, `files.write("{home}/dirlink/new", "x")`, `files.remove("{home}/dirlink/secret")`,
		`files.read("{home}/leaflink")`, `files.write("{home}/leaflink", "x")`, `files.remove("{home}/leaflink")`,
		`files.read("{home}/leaflink-in")`, `files.write("{home}/leaflink-in", "x")`,
		`files.write("{home}/sshlink/k", "x")`,
	} {
		if _, err := e.run(t, call); err == nil {
			t.Errorf("%s was accepted", call)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "secret")); string(b) != "s" {
		t.Errorf("a file outside home was modified: %q", b)
	}
	if _, err := os.Lstat(filepath.Join(e.home, ".ssh", "k")); err == nil {
		t.Error("a write went through a link into .ssh")
	}
	res, err := e.run(t, `return files.glob("{home}/**")`)
	if err != nil || strings.Contains(res.Text, "link") || strings.Contains(res.Text, "secret") {
		t.Errorf("glob followed a link: %q, %v", res.Text, err)
	}
}

func TestFilesRemove(t *testing.T) {
	e := newFileEnv(t, nil, []string{"{home}/**"})
	p := e.put(t, "d/a.txt", "a")
	res, err := e.run(t, `return files.remove("{home}/d/missing")`)
	if err != nil || strings.TrimSpace(res.Text) != "false" {
		t.Fatalf("missing: %q, %v", res.Text, err)
	}
	callErr(t, mustErr(e.run(t, `files.remove("{home}/d")`)), Failed)
	if _, err := os.Stat(p); err != nil {
		t.Fatal("a directory removal touched its content")
	}
	if res, err = e.run(t, `return files.remove("{home}/d/a.txt")`); err != nil || strings.TrimSpace(res.Text) != "true" {
		t.Fatalf("existing: %q, %v", res.Text, err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Error("not removed")
	}
}

func TestFilesReadOnlySession(t *testing.T) {
	e := newFileEnv(t, []string{"{home}/**"}, []string{"{home}/**"})
	e.readOnly = true
	p := e.put(t, "a.txt", "a")
	callErr(t, mustErr(e.run(t, `files.write("{home}/a.txt", "x")`)), Denied)
	callErr(t, mustErr(e.run(t, `files.remove("{home}/a.txt")`)), Denied)
	if b, _ := os.ReadFile(p); string(b) != "a" {
		t.Errorf("file changed: %q", b)
	}
	if _, err := e.run(t, `files.read("{home}/a.txt")`); err != nil {
		t.Errorf("a read-only session reads: %v", err)
	}
}

func TestFilesCaps(t *testing.T) {
	e := newFileEnv(t, []string{"{home}/**"}, []string{"{home}/**"})
	callErr(t, mustErr(e.run(t, `files.write("{home}/big", "x" * (8 * 1024 * 1024 + 1))`)), LimitExceeded)
	if err := os.WriteFile(filepath.Join(e.home, "big"), make([]byte, MaxFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	callErr(t, mustErr(e.run(t, `files.read("{home}/big", binary=True)`)), LimitExceeded)
}

func TestFilesGlobIsBoundedAndSorted(t *testing.T) {
	e := newFileEnv(t, []string{"{home}/**"}, nil)
	for i := 0; i < MaxGlobResults+50; i++ {
		e.put(t, fmt.Sprintf("many/f%04d.txt", i), "")
	}
	e.put(t, "a/b/c.md", "")
	e.put(t, "a/z.md", "")
	res, err := e.run(t, `return len(files.glob("{home}/many/*.txt"))`)
	if err != nil || strings.TrimSpace(res.Text) != fmt.Sprint(MaxGlobResults) {
		t.Fatalf("bounded glob: %q, %v", res.Text, err)
	}
	res, err = e.run(t, `return files.glob("{home}/a/**")`)
	if err != nil || !strings.Contains(res.Text, `"{home}/a/b/c.md",`) || strings.Index(res.Text, "b/c.md") > strings.Index(res.Text, "z.md") {
		t.Errorf("glob ** %q, %v", res.Text, err)
	}
	if res, err = e.run(t, `return files.glob("{home}/a/z.md")`); err != nil || !strings.Contains(res.Text, "z.md") {
		t.Errorf("literal glob %q, %v", res.Text, err)
	}
}

func TestFilesNotWhileLoading(t *testing.T) {
	if _, err := Load(entry("files.glob(\"{home}/*\")\n")); err == nil {
		t.Error("files at load")
	}
}

// The deny list is the one the provider cells use, plus the server's data and the
// shell start-up files: none is reachable under the broadest grant.
func TestFilesDeniedPlacesShared(t *testing.T) {
	e := newFileEnv(t, []string{"{home}/**"}, []string{"{home}/**"})
	for _, rel := range []string{
		".zshrc", ".bashrc", ".profile", ".zprofile", ".bash_profile",
		"Library/LaunchAgents/x.plist", ".config/git/config", ".config/autostart/x.desktop",
		".aws/credentials", ".kube/config", ".docker/config.json", "Desktop/x", ".cargo/config.toml", ".local/bin/tool",
		"cartographer-data/credentials/token", "cartographer-data/audit/log", ".local/share/cartographer/index.db",
	} {
		callErr(t, mustErr(e.run(t, fmt.Sprintf(`files.read(%q)`, "{home}/"+rel))), Denied)
		callErr(t, mustErr(e.run(t, fmt.Sprintf(`files.write(%q, "x")`, "{home}/"+rel))), Denied)
		callErr(t, mustErr(e.run(t, fmt.Sprintf(`files.remove(%q)`, "{home}/"+rel))), Denied)
		if _, err := os.Lstat(filepath.Join(e.home, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s was created", rel)
		}
	}
	e.put(t, ".aws/credentials", "k")
	e.put(t, ".zshrc", "z")
	e.put(t, "cartographer-data/x", "d")
	e.put(t, "ok/a.txt", "a")
	res, err := e.run(t, `return files.glob("{home}/**")`)
	if err != nil || strings.Contains(res.Text, ".aws") || strings.Contains(res.Text, "zshrc") || strings.Contains(res.Text, "cartographer-data") || !strings.Contains(res.Text, "ok/a.txt") {
		t.Errorf("glob %q, %v", res.Text, err)
	}
}

// A link named like the probe the glob once used does not stop a glob in its
// directory, and a link entry is neither followed nor returned.
func TestFilesGlobWithSiblingLinks(t *testing.T) {
	e := newFileEnv(t, []string{"{home}/**"}, nil)
	e.put(t, "d/a.txt", "a")
	if err := os.Symlink(filepath.Join(e.home, "d", "a.txt"), filepath.Join(e.home, "d", "x")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	res, err := e.run(t, `return files.glob("{home}/d/*")`)
	if err != nil || !strings.Contains(res.Text, "{home}/d/a.txt") || strings.Contains(res.Text, "{home}/d/x") {
		t.Errorf("glob %q, %v", res.Text, err)
	}
	if res, err = e.run(t, `return files.glob("{home}/d/x")`); err != nil || strings.Contains(res.Text, "d/x") {
		t.Errorf("literal link glob %q, %v", res.Text, err)
	}
}

// A refusal for a link tells the script the placeholder path and nothing else.
func TestFilesLinkErrorsLeakNothing(t *testing.T) {
	e := newFileEnv(t, []string{"{home}/**"}, []string{"{home}/**"})
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(e.home, "lk")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	for _, call := range []string{`files.read("{home}/lk/f")`, `files.write("{home}/lk/f", "x")`, `files.remove("{home}/lk/f")`} {
		_, err := e.run(t, call)
		if err == nil {
			t.Fatalf("%s accepted", call)
		}
		for _, bad := range []string{e.home, outside, "allow_symlinks", "providers."} {
			if strings.Contains(err.Error(), bad) {
				t.Errorf("%s: error leaks %q: %v", call, bad, err)
			}
		}
	}
}

// A grant that the manifest now accepts below a client folder still cannot
// read Cartographer's state under it: the enforcer, not the lexical check, decides (#1783).
func TestFilesGrantBelowAFolderStillDeniesOwnState(t *testing.T) {
	e := newFileEnv(t, []string{"{home}/x/**"}, nil)
	e.put(t, "x/ok.txt", "ok")
	e.put(t, "x/.cartographer/secret", "s")
	e.put(t, "x/.cartographer.yaml", "s")
	if res, err := e.run(t, `return files.read("{home}/x/ok.txt")`); err != nil || !strings.Contains(res.Text, "ok") {
		t.Fatalf("granted read: %q, %v", res.Text, err)
	}
	for _, rel := range []string{"x/.cartographer/secret", "x/.cartographer.yaml"} {
		ce := callErr(t, mustErr(e.run(t, fmt.Sprintf(`files.read("{home}/%s")`, rel))), Denied)
		if !strings.Contains(ce.Msg, "denied") {
			t.Errorf("%s: %q", rel, ce.Msg)
		}
	}
	res, err := e.run(t, `return files.glob("{home}/x/**")`)
	if err != nil || strings.Contains(res.Text, ".cartographer") || !strings.Contains(res.Text, "x/ok.txt") {
		t.Errorf("glob %q, %v", res.Text, err)
	}
}

// The cron backend's state directory ($XDG_STATE_HOME/cartographer) is denied
// even when a custom XDG_STATE_HOME puts it under a granted folder (#1783).
func TestFilesDenyTheCronStateDir(t *testing.T) {
	e := newFileEnv(t, []string{"{home}/state/**", "{home}/.local/**"}, []string{"{home}/state/**"})
	t.Setenv("XDG_STATE_HOME", filepath.Join(e.home, "state"))
	e.put(t, "state/cartographer/server.pid", "1")
	e.put(t, "state/other/ok.txt", "ok")
	e.put(t, ".local/state/cartographer/server.pid", "1")
	if res, err := e.run(t, `return files.read("{home}/state/other/ok.txt")`); err != nil || !strings.Contains(res.Text, "ok") {
		t.Fatalf("granted read: %q, %v", res.Text, err)
	}
	for _, rel := range []string{"state/cartographer/server.pid", ".local/state/cartographer/server.pid"} {
		callErr(t, mustErr(e.run(t, fmt.Sprintf(`files.read("{home}/%s")`, rel))), Denied)
	}
	callErr(t, mustErr(e.run(t, `files.write("{home}/state/cartographer/new", "x")`)), Denied)
	if _, err := os.Lstat(filepath.Join(e.home, "state", "cartographer", "new")); err == nil {
		t.Error("written into the cron state dir")
	}
}
