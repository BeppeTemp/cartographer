package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/kbtest"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
)

const (
	secretsRecipA = "age1" + "qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
	secretsRecipB = "age1" + "pppppppppppppppppppppppppppppppppppppppppppppppppppppppppp"
)

type secretsEnv struct {
	c       toolCaller
	k       *kb.KB
	credDir string
	tmp     string
}

// newSecretsEnv serves a KB whose age key resolves live from a temp
// credentials directory, as the server does.
func newSecretsEnv(t *testing.T) secretsEnv {
	t.Helper()
	k := kbtest.New(t)
	credDir := filepath.Join(t.TempDir(), "credentials")
	k.SetSopsAgeKeyResolver(func() string { return resolveSopsAgeKeyFile(credDir, "kb-a") })
	multi := mcpserver.NewMultiKBServer("test")
	multi.MountKB("kb-a", func(s *mcpserver.Server) { mcpserver.RegisterKBTools(s, k, mcpserver.Deps{}) })
	h := multi.Handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.ContextWithPrincipal(r.Context(), auth.Principal{ID: "test", Policy: auth.Policy{Admin: true}})
		h.ServeHTTP(w, r.WithContext(ctx))
	}))
	t.Cleanup(srv.Close)
	cl := client.New(srv.URL+"/mcp", "")
	cl.HTTP.Timeout = gitxtest.Deadline(cl.HTTP.Timeout)
	return secretsEnv{c: targetCaller{c: cl}, k: k, credDir: credDir, tmp: t.TempDir()}
}

func (e secretsEnv) keyFile(t *testing.T, name, recipient string) string {
	t.Helper()
	p := filepath.Join(e.tmp, name)
	if err := os.WriteFile(p, []byte("# public key: "+recipient+"\nAGE-SECRET-KEY-1PLACEHOLDER\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (e secretsEnv) run(recipient, keyFile string, force bool) (int, string, string) {
	var out, errOut bytes.Buffer
	code := runKBSecretsInit(e.c, "kb-a", recipient, keyFile, e.credDir, force, &out, &errOut)
	return code, out.String(), errOut.String()
}

func (e secretsEnv) head(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "-C", e.k.Root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

func (e secretsEnv) secretsState(t *testing.T) string {
	t.Helper()
	var st struct {
		Capabilities map[string]struct {
			State string `json:"state"`
		} `json:"capabilities"`
	}
	if err := callInto(e.c, "kb_status", map[string]any{}, &st); err != nil {
		t.Fatal(err)
	}
	return st.Capabilities["secrets"].State
}

func TestKBSecretsInitFullRunAndRerun(t *testing.T) {
	e := newSecretsEnv(t)
	key := e.keyFile(t, "id.txt", secretsRecipA)
	if got := e.secretsState(t); got != "disabled" {
		t.Fatalf("before: %s", got)
	}
	code, out, errOut := e.run(secretsRecipA, key, false)
	if code != 0 || !strings.Contains(out, "rule: created") || !strings.Contains(out, "key: installed") || !strings.Contains(out, "no restart needed") {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
	rule, err := os.ReadFile(filepath.Join(e.k.Root, ".sops.yaml"))
	if err != nil || !strings.Contains(string(rule), secretsRecipA) {
		t.Fatalf(".sops.yaml: %v %q", err, rule)
	}
	target := filepath.Join(e.credDir, "kb-a.age")
	if runtime.GOOS != "windows" {
		if fi, err := os.Stat(target); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("key file: %v %v", fi, err)
		}
	}
	if got := e.secretsState(t); got != "enabled" {
		t.Fatalf("after, same server: %s", got)
	}
	head := e.head(t)
	code, out, errOut = e.run(secretsRecipA, key, false)
	if code != 0 || !strings.Contains(out, "rule: unchanged") || !strings.Contains(out, "key: unchanged") {
		t.Fatalf("rerun: code %d out %q err %q", code, out, errOut)
	}
	if e.head(t) != head {
		t.Fatal("a rerun committed")
	}
}

func TestKBSecretsInitRefusesDifferentRuleAndKeyWithoutForce(t *testing.T) {
	e := newSecretsEnv(t)
	if code, _, errOut := e.run(secretsRecipA, e.keyFile(t, "a.txt", secretsRecipA), false); code != 0 {
		t.Fatal(errOut)
	}
	head := e.head(t)
	before, _ := os.ReadFile(filepath.Join(e.credDir, "kb-a.age"))
	keyB := e.keyFile(t, "b.txt", secretsRecipB)

	// Different key, same rule recipient B would also be a different rule: use A's rule
	// with B's key file only through the recipient check, so refuse on the rule.
	code, _, errOut := e.run(secretsRecipB, keyB, false)
	if code != 1 || !strings.Contains(errOut, "already holds a different key") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if e.head(t) != head {
		t.Fatal("refusal committed")
	}
	// A different rule with the key already in place: refused by the server.
	code, _, errOut = e.run(secretsRecipB, "", false)
	if code != 1 || !strings.Contains(errOut, "different .sops.yaml") {
		t.Fatalf("rule: code %d err %q", code, errOut)
	}
	if after, _ := os.ReadFile(filepath.Join(e.credDir, "kb-a.age")); !bytes.Equal(before, after) {
		t.Fatal("key touched")
	}

	code, out, errOut := e.run(secretsRecipB, keyB, true)
	if code != 0 || !strings.Contains(out, "rule: replaced") || !strings.Contains(out, "key: replaced") || !strings.Contains(out, "sops updatekeys") {
		t.Fatalf("force: code %d out %q err %q", code, out, errOut)
	}
	if got, _ := os.ReadFile(filepath.Join(e.credDir, "kb-a.age")); !strings.Contains(string(got), secretsRecipB) {
		t.Fatalf("key not replaced: %q", got)
	}
}

func TestKBSecretsInitKeyFileWithoutTheRecipientStopsBeforeAnyCall(t *testing.T) {
	e := newSecretsEnv(t)
	head := e.head(t)
	code, _, errOut := e.run(secretsRecipA, e.keyFile(t, "b.txt", secretsRecipB), false)
	if code != 1 || !strings.Contains(errOut, "does not hold the recipient") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	if e.head(t) != head {
		t.Fatal("committed")
	}
	if _, err := os.Stat(filepath.Join(e.credDir, "kb-a.age")); err == nil {
		t.Fatal("key written")
	}
	bad := filepath.Join(e.tmp, "junk")
	_ = os.WriteFile(bad, []byte("hello"), 0o600)
	if code, _, errOut = e.run(secretsRecipA, bad, false); code != 1 || !strings.Contains(errOut, "not an age identity") {
		t.Fatalf("junk: code %d err %q", code, errOut)
	}
	if code, _, _ = e.run("not-a-key", "", false); code != 1 {
		t.Fatalf("bad recipient: %d", code)
	}
}

func TestKBSecretsInitRefusesASymlinkedTargetKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	e := newSecretsEnv(t)
	if err := os.MkdirAll(e.credDir, 0o700); err != nil {
		t.Fatal(err)
	}
	real := e.keyFile(t, "real.txt", secretsRecipA)
	if err := os.Symlink(real, filepath.Join(e.credDir, "kb-a.age")); err != nil {
		t.Fatal(err)
	}
	head := e.head(t)
	code, _, errOut := e.run(secretsRecipA, e.keyFile(t, "id.txt", secretsRecipA), true)
	if code != 1 || !strings.Contains(errOut, "not a regular file") || e.head(t) != head {
		t.Fatalf("code %d err %q", code, errOut)
	}
}

func TestKBSecretsInitWithoutKeyFileSaysWhatIsMissing(t *testing.T) {
	e := newSecretsEnv(t)
	code, out, errOut := e.run(secretsRecipA, "", false)
	if code != 0 || !strings.Contains(out, "rule: created") || !strings.Contains(out, "key: not given") || !strings.Contains(out, "kb-a.age") || strings.Contains(out, "no restart needed\n") && strings.Contains(out, "secrets are enabled") {
		t.Fatalf("code %d out %q err %q", code, out, errOut)
	}
}
