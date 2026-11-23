package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// directEnv is a KB in a data dir with a server file, and no service.
type directEnv struct {
	base, data, config, root string
}

func newDirectEnv(t *testing.T, httpAddr string) directEnv {
	t.Helper()
	if !hasGitBinary() {
		t.Skip("git not in PATH")
	}
	base := t.TempDir()
	e := directEnv{base: base, data: filepath.Join(base, "data"), config: filepath.Join(base, "config.yaml")}
	e.root = filepath.Join(e.data, "kb-a")
	if _, err := kb.Init(e.root); err != nil {
		t.Fatal(err)
	}
	yaml := "http: \"" + httpAddr + "\"\ndata: '" + e.data + "'\ngit:\n  identities:\n    default: \"" + reauthorAda + "\"\n"
	if err := os.WriteFile(e.config, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e directEnv) run(recipient string, force bool) (int, string, string) {
	scfg, err := loadReauthorConfig(e.config)
	if err != nil {
		return 1, "", err.Error()
	}
	var out, errOut bytes.Buffer
	d := &directRuleCaller{name: "kb-a", dataDir: e.data, cfg: scfg, errOut: &errOut}
	code := runKBSecretsInit(d, "kb-a", recipient, "", filepath.Join(e.base, "credentials"), force, &out, &errOut)
	return code, out.String(), errOut.String()
}

func (e directEnv) commits(t *testing.T) string {
	return reauthorGit(t, e.root, "log", "--format=%an <%ae>|%s", "-n", "1")
}

func (e directEnv) count(t *testing.T) string {
	return reauthorGit(t, e.root, "rev-list", "--count", "HEAD")
}

func TestKBSecretsInitDirectWritesCommitsAndIsIdempotent(t *testing.T) {
	e := newDirectEnv(t, "127.0.0.1:0")
	code, out, errOut := e.run(secretsRecipA, false)
	if code != 0 || !strings.Contains(out, "rule: created") {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	if rule, err := os.ReadFile(filepath.Join(e.root, ".sops.yaml")); err != nil || !strings.Contains(string(rule), secretsRecipA) {
		t.Fatalf(".sops.yaml: %v %q", err, rule)
	}
	if got := e.commits(t); got != reauthorAda+"|"+secretsRuleSubject {
		t.Fatalf("commit = %q", got)
	}
	if st := reauthorGit(t, e.root, "status", "--porcelain"); st != "" {
		t.Fatalf("dirty after the commit: %q", st)
	}
	n := e.count(t)
	// no remote: committed, nothing to push, no failure; a rerun is unchanged and silent
	code, out, _ = e.run(secretsRecipA, false)
	if code != 0 || !strings.Contains(out, "rule: unchanged") || e.count(t) != n {
		t.Fatalf("rerun: code %d out %q commits %s -> %s", code, out, n, e.count(t))
	}
	// a different rule is refused without --force, replaced with it
	code, _, errOut = e.run(secretsRecipB, false)
	if code != 1 || !strings.Contains(errOut, "different .sops.yaml") || e.count(t) != n {
		t.Fatalf("different: code %d err %q", code, errOut)
	}
	code, out, _ = e.run(secretsRecipB, true)
	if code != 0 || !strings.Contains(out, "rule: replaced") || e.count(t) == n {
		t.Fatalf("force: code %d out %q", code, out)
	}
}

func TestKBSecretsInitDirectRefusalsWriteNothing(t *testing.T) {
	t.Run("not a git repository", func(t *testing.T) {
		e := newDirectEnv(t, "127.0.0.1:0")
		if err := os.RemoveAll(filepath.Join(e.root, ".git")); err != nil {
			t.Fatal(err)
		}
		code, _, errOut := e.run(secretsRecipA, false)
		if code != 1 || !strings.Contains(errOut, "not a git repository") {
			t.Fatalf("code %d err %q", code, errOut)
		}
		if _, err := os.Stat(filepath.Join(e.root, ".sops.yaml")); err == nil {
			t.Fatal("a rule was written")
		}
	})
	t.Run("missing KB", func(t *testing.T) {
		e := newDirectEnv(t, "127.0.0.1:0")
		if err := os.RemoveAll(e.root); err != nil {
			t.Fatal(err)
		}
		if code, _, errOut := e.run(secretsRecipA, false); code != 1 || !strings.Contains(errOut, `no KB named "kb-a" in `) {
			t.Fatalf("code %d err %q", code, errOut)
		}
	})
	t.Run("lock held", func(t *testing.T) {
		e := newDirectEnv(t, "127.0.0.1:0")
		k, err := kb.Open(e.root)
		if err != nil {
			t.Fatal(err)
		}
		release, err := k.AcquireProcessLock(0, "test holder")
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		saved := reauthorLockTimeout
		reauthorLockTimeout = 0
		defer func() { reauthorLockTimeout = saved }()
		code, _, errOut := e.run(secretsRecipA, false)
		if code != 1 || !strings.Contains(errOut, "locked") {
			t.Fatalf("code %d err %q", code, errOut)
		}
		if _, err := os.Stat(filepath.Join(e.root, ".sops.yaml")); err == nil {
			t.Fatal("a rule was written under the lock")
		}
	})
}

// A refused connection is the only proof that no service runs: the command
// then writes the rule itself, the key is installed as before. An answer, a
// 401 included, keeps the server path and its error.
func TestKBSecretsInitFallsBackOnlyOnARefusedConnection(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := l.Addr().String()
		l.Close()
		e := newDirectEnv(t, addr)
		key := filepath.Join(e.base, "id.txt")
		if err := os.WriteFile(key, []byte("# public key: "+secretsRecipA+"\nAGE-SECRET-KEY-1PLACEHOLDER\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		code := cmdKBSecretsInit([]string{"kb-a", "--recipient", secretsRecipA, "--key-file", key, "--config", e.config, "--data", e.data})
		if code != 0 {
			t.Fatalf("code %d", code)
		}
		if _, err := os.Stat(filepath.Join(e.root, ".sops.yaml")); err != nil {
			t.Fatalf("no rule: %v", err)
		}
		if got := e.commits(t); got != reauthorAda+"|"+secretsRuleSubject {
			t.Fatalf("commit = %q", got)
		}
		if _, err := os.Stat(filepath.Join(e.data, "credentials", "kb-a.age")); err != nil {
			t.Fatalf("key not installed: %v", err)
		}
	})
	t.Run("a server answering 401", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "no", http.StatusUnauthorized)
		}))
		defer srv.Close()
		e := newDirectEnv(t, strings.TrimPrefix(srv.URL, "http://"))
		code := cmdKBSecretsInit([]string{"kb-a", "--recipient", secretsRecipA, "--config", e.config, "--data", e.data})
		if code != 1 {
			t.Fatalf("code %d", code)
		}
		if _, err := os.Stat(filepath.Join(e.root, ".sops.yaml")); err == nil {
			t.Fatal("fell back on an answering server")
		}
	})
}

// A commit that fails leaves the rule written: the rerun finds it unchanged
// and must still commit it.
func TestKBSecretsInitDirectRerunCommitsARuleLeftUncommitted(t *testing.T) {
	e := newDirectEnv(t, "127.0.0.1:0")
	// a stale ref lock makes the commit's ref update fail (hooks do not run)
	branch := reauthorGit(t, e.root, "branch", "--show-current")
	hook := filepath.Join(e.root, ".git", "refs", "heads", branch+".lock")
	if err := os.WriteFile(hook, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	n := e.count(t)
	code, _, errOut := e.run(secretsRecipA, false)
	if code != 1 || !strings.Contains(errOut, "not committed") {
		t.Fatalf("first run: code %d err %q", code, errOut)
	}
	if _, err := os.Stat(filepath.Join(e.root, ".sops.yaml")); err != nil || e.count(t) != n {
		t.Fatalf("expected a written, uncommitted rule: %v", err)
	}
	if err := os.Remove(hook); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := e.run(secretsRecipA, false)
	if code != 0 || !strings.Contains(out, "rule: unchanged") {
		t.Fatalf("rerun: code %d out %q err %q", code, out, errOut)
	}
	if got := e.commits(t); got != reauthorAda+"|"+secretsRuleSubject {
		t.Fatalf("the rerun did not commit the rule: %q", got)
	}
}

// #2284 item 1: an unknown KB on the direct path answers like the server path,
// naming the KB and listing the KBs of the data dir.
func TestKBSecretsInitDirectUnknownKBListsTheKnownOnes(t *testing.T) {
	e := newDirectEnv(t, "127.0.0.1:0")
	scfg, err := loadReauthorConfig(e.config)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	d := &directRuleCaller{name: "nope", dataDir: e.data, cfg: scfg, errOut: &errOut}
	code := runKBSecretsInit(d, "nope", secretsRecipA, "", filepath.Join(e.base, "credentials"), false, &out, &errOut)
	if code == 0 || !strings.Contains(errOut.String(), `no KB named "nope" in `+e.data+`; mounted: kb-a`) || strings.Contains(errOut.String(), "not an OKF KB") {
		t.Fatalf("code %d err %q", code, errOut.String())
	}
}

// #2284 item 2: a 401 names the source the token was read from and says the
// env var is not read by this command.
func TestKBSecretsInitUnauthorizedNamesTheTokenSource(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	clientPointingAt(t, "http://127.0.0.1:1")
	t.Setenv("CARTOGRAPHER_TOKENS", "placeholder-token")
	data := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "srv.yaml")
	if err := os.WriteFile(cfg, []byte("http: "+strings.TrimPrefix(srv.URL, "http://")+"\ndata: "+filepath.ToSlash(data)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var code int
	stderr := withStderr(t, func() {
		code = cmdKBSecretsInit([]string{"kb-a", "--recipient", secretsRecipA, "--config", cfg, "--data", data, "--local"})
	})
	dir, _ := clientconfig.TargetDir()
	if code == 0 || !strings.Contains(stderr, "no bearer token was sent") || !strings.Contains(stderr, clientconfig.Path(dir)) || !strings.Contains(stderr, "not from $CARTOGRAPHER_TOKENS") {
		t.Errorf("code=%d stderr=%q", code, stderr)
	}
}
