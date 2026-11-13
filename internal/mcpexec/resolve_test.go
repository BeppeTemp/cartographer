package mcpexec

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// planted is built at run time so the file holds no secret-shaped literal (D1202).
var planted = "tok" + "-" + strings.Repeat("Z9", 6)

type fakeDeps struct {
	desc      string
	b64       bool
	secrets   map[string]string // "concept#NAME" -> value
	approved  string
	env       []string
	down      error
	secretHit int
	callErr   string
	tokenEnv  string
}

func (f *fakeDeps) Call(_ context.Context, tool string, args map[string]any) (string, error) {
	if f.down != nil {
		return "", f.down
	}
	switch tool {
	case "artifact_read":
		c, enc := f.desc, "text"
		if f.b64 {
			c, enc = base64.StdEncoding.EncodeToString([]byte(f.desc)), "base64"
		}
		b, _ := json.Marshal(map[string]string{"content": c, "encoding": enc})
		return string(b), nil
	case "secret_resolve":
		f.secretHit++
		if f.callErr != "" {
			return "", errors.New(f.callErr)
		}
		name := args["names"].([]string)[0]
		v, ok := f.secrets[args["concept_id"].(string)+"#"+name]
		if !ok {
			return "", errors.New("unknown name " + name)
		}
		return name + "=" + v, nil
	}
	return "", errors.New("unexpected tool " + tool)
}

func (f *fakeDeps) Approved(string, string) (string, bool) { return f.approved, f.approved != "" }
func (f *fakeDeps) Environ() []string                      { return f.env }
func (f *fakeDeps) TokenEnv() string                       { return f.tokenEnv }

func hashOf(slug, desc string) string {
	return contract.HashArtifactFiles([]contract.ArtifactFile{{Path: slug + ".json", Content: []byte(desc)}})
}

func newDeps(desc string) *fakeDeps {
	return &fakeDeps{desc: desc, approved: hashOf("svc", desc), env: []string{"HOME=/h", "HOST_VAR=hv"},
		secrets: map[string]string{"services/svc#TOKEN": planted, "services/svc#MULTI": "a\nb\n", "services/svc#EMPTY": ""}}
}

const stdioDesc = `{"type":"stdio","command":"srv","args":["-x"],"env":{"API_TOKEN":"${secret:services/svc#TOKEN}","HOSTV":"${HOST_VAR}"},"env_literal":{"NODE_OPTIONS":"--use-system-ca"}}`

func envOf(l *Launch, k string) (string, bool) { return environValue(l.Env, k) }

func TestResolveApproved(t *testing.T) {
	for _, b64 := range []bool{false, true} {
		d := newDeps(stdioDesc)
		d.b64 = b64
		l, err := Resolve(context.Background(), d, "kb-a/svc")
		if err != nil {
			t.Fatal(err)
		}
		if v, _ := envOf(l, "API_TOKEN"); v != planted {
			t.Fatalf("token not in env: %q", v)
		}
		if v, _ := envOf(l, "HOSTV"); v != "hv" {
			t.Fatalf("HOSTV = %q", v)
		}
		if v, _ := envOf(l, "NODE_OPTIONS"); v != "--use-system-ca" {
			t.Fatalf("literal = %q", v)
		}
		if v, _ := envOf(l, "HOME"); v != "/h" || l.Command != "srv" {
			t.Fatalf("inherited env or command lost")
		}
		for _, out := range []string{fmt.Sprintf("%+v", l), fmt.Sprintf("%#v", *l), fmt.Sprintf("%+v", l.Redacted()), l.String()} {
			if strings.Contains(out, planted) {
				t.Fatalf("a value leaked: %s", out)
			}
		}
		if got := strings.Join(l.Redacted().Env, " "); !strings.Contains(got, "API_TOKEN=<redacted>") || !strings.Contains(got, "NODE_OPTIONS=--use-system-ca") || strings.Contains(got, "HOME") {
			t.Fatalf("redacted env = %s", got)
		}
	}
}

func TestResolveRefusesChangedOrUnapproved(t *testing.T) {
	d := newDeps(stdioDesc)
	d.approved = hashOf("svc", stdioDesc+" ")
	if _, err := Resolve(context.Background(), d, "kb-a/svc"); err == nil || !strings.Contains(err.Error(), "cartographer approve mcp svc --kb kb-a") {
		t.Fatalf("changed: %v", err)
	}
	d.approved = ""
	if _, err := Resolve(context.Background(), d, "kb-a/svc"); err == nil {
		t.Fatal("unapproved accepted")
	}
	if d.secretHit != 0 {
		t.Fatal("a secret was resolved for an unapproved descriptor")
	}
}

func TestResolveUnsetVar(t *testing.T) {
	d := newDeps(`{"type":"stdio","command":"srv","env":{"A":"${NOPE_VAR}"}}`)
	d.approved = hashOf("svc", d.desc)
	_, err := Resolve(context.Background(), d, "kb-a/svc")
	if err == nil || !strings.Contains(err.Error(), "NOPE_VAR") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveUnreachableIsNeverCached(t *testing.T) {
	d := newDeps(stdioDesc)
	if _, err := Resolve(context.Background(), d, "kb-a/svc"); err != nil {
		t.Fatal(err)
	}
	first := d.secretHit
	if _, err := Resolve(context.Background(), d, "kb-a/svc"); err != nil || d.secretHit != 2*first {
		t.Fatalf("second call did not ask the server again (%d hits, err %v)", d.secretHit, err)
	}
	d.down = errors.New("connection refused")
	_, err := Resolve(context.Background(), d, "kb-a/svc")
	if err == nil || !strings.Contains(err.Error(), "connection refused") || !strings.Contains(err.Error(), "never cached") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveMultilineAndOrder(t *testing.T) {
	d := newDeps(`{"type":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer ${secret:services/svc#TOKEN}","X-M":"${secret:services/svc#MULTI}|${secret:services/svc#TOKEN}"}}`)
	d.approved = hashOf("svc", d.desc)
	l, err := Resolve(context.Background(), d, "kb-a/svc")
	if err != nil {
		t.Fatal(err)
	}
	if l.Headers["Authorization"] != "Bearer "+planted || l.Headers["X-M"] != "a\nb\n|"+planted {
		t.Fatalf("headers = %q", l.Headers)
	}
	if r := l.Redacted().Headers; r["Authorization"] != "<redacted>" || r["X-M"] != "<redacted>" {
		t.Fatalf("redacted headers = %v", r)
	}
}

func TestResolveFailuresCarryNoValue(t *testing.T) {
	d := newDeps(stdioDesc)
	d.callErr = "boom " + planted // a server that echoed a value must still be scrubbed after the first success
	// the first reference resolves nothing, so nothing to scrub yet: the error is relayed and escaped
	if _, err := Resolve(context.Background(), d, "kb-a/svc"); err == nil {
		t.Fatal("expected an error")
	}
	for _, desc := range []string{
		`{"type":"stdio","command":"srv","env":{"A":"${secret:services/svc#EMPTY}"}}`,
		`{"type":"stdio","command":"srv","env":{"A":"${secret:services/svc#GONE}"}}`,
	} {
		d := newDeps(desc)
		d.approved = hashOf("svc", desc)
		_, err := Resolve(context.Background(), d, "kb-a/svc")
		if err == nil || strings.Contains(err.Error(), planted) {
			t.Fatalf("err = %v", err)
		}
	}
	// A value resolved earlier in the same launch is scrubbed from a later error.
	desc := `{"type":"stdio","command":"srv","env":{"A":"${secret:services/svc#TOKEN}","B":"${secret:services/svc#GONE}"}}`
	d2 := newDeps(desc)
	d2.approved = hashOf("svc", desc)
	d2.secrets["services/svc#GONE"] = ""
	d2.callErr = ""
	r := &resolver{d: d2, kb: "kb-a", secrets: []string{planted}}
	d2.down = errors.New("x " + planted)
	if _, err := r.call(context.Background(), "secret_resolve", map[string]any{}); err == nil || strings.Contains(err.Error(), planted) {
		t.Fatalf("not scrubbed: %v", err)
	}
}

func TestParseRef(t *testing.T) {
	for _, bad := range []string{"", "svc", "a/b/c", "/svc", "a/", "../svc", "a/.."} {
		if _, _, err := ParseRef(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if kb, slug, err := ParseRef("kb-a/svc"); err != nil || kb != "kb-a" || slug != "svc" {
		t.Fatal(kb, slug, err)
	}
}

func TestResolveKeepsTheCartographerTokenFromTheChild(t *testing.T) {
	d := newDeps(stdioDesc)
	d.tokenEnv = "CARTOGRAPHER_TOKEN"
	d.env = append(d.env, "CARTOGRAPHER_TOKEN="+planted)
	l, err := Resolve(context.Background(), d, "kb-a/svc")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := envOf(l, "CARTOGRAPHER_TOKEN"); ok {
		t.Fatal("the child inherits the Cartographer token")
	}
	if v, _ := envOf(l, "HOME"); v != "/h" {
		t.Fatal("the rest of the environment was lost")
	}
	for _, desc := range []string{
		`{"type":"stdio","command":"srv","env":{"X":"${CARTOGRAPHER_TOKEN}"}}`,
		`{"type":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer ${CARTOGRAPHER_TOKEN}"}}`,
	} {
		d := newDeps(desc)
		d.tokenEnv = "CARTOGRAPHER_TOKEN"
		d.env = append(d.env, "CARTOGRAPHER_TOKEN="+planted)
		_, err := Resolve(context.Background(), d, "kb-a/svc")
		if err == nil || !strings.Contains(err.Error(), "CARTOGRAPHER_TOKEN") || strings.Contains(err.Error(), planted) {
			t.Fatalf("want a refusal naming the variable only, got %v", err)
		}
	}
}

func TestResolveDropsInheritedKeysTheDescriptorSets(t *testing.T) {
	d := newDeps(stdioDesc)
	d.env = append(d.env, "API_TOKEN=stale", "NODE_OPTIONS=--inspect", "HOSTV=old")
	l, err := Resolve(context.Background(), d, "kb-a/svc")
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, e := range l.Env {
		k, _, _ := strings.Cut(e, "=")
		count[k]++
	}
	for _, k := range []string{"API_TOKEN", "NODE_OPTIONS", "HOSTV"} {
		if count[k] != 1 {
			t.Fatalf("%s appears %d times in the child env", k, count[k])
		}
	}
	if v, _ := envOf(l, "API_TOKEN"); v != planted {
		t.Fatalf("API_TOKEN = %q", v)
	}
	seen := map[string]int{}
	for _, e := range l.Redacted().Env {
		k, _, _ := strings.Cut(e, "=")
		seen[k]++
	}
	for k, n := range seen {
		if n != 1 {
			t.Fatalf("Redacted lists %s %d times", k, n)
		}
	}
}
