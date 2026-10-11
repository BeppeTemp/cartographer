package mcpserver

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

const uiSecretFixture = `demo:
    username: ENC[AES256_GCM,data:AAAA,iv:BBBB,tag:CCCC,type:str]
    ports:
        - ENC[AES256_GCM,data:DDDD,iv:EEEE,tag:FFFF,type:int]
        - ENC[AES256_GCM,data:GGGG,iv:HHHH,tag:IIII,type:int]
a/b:
    c~d: ENC[AES256_GCM,data:JJJJ,iv:KKKK,tag:LLLL,type:str]
api_token: ENC[AES256_GCM,data:MMMM,iv:NNNN,tag:OOOO,type:str]
sops:
    age:
        - enc: |
            -----BEGIN AGE ENCRYPTED FILE-----
            ZmFrZQ==
            -----END AGE ENCRYPTED FILE-----
          recipient: age1fakefakefakefakefakefakefakefakefakefakefakefakefakefakefake
    lastmodified: "2026-01-01T00:00:00Z"
    mac: ENC[AES256_GCM,data:PPPP,iv:QQQQ,tag:RRRR,type:str]
    version: 3.9.0
`

func secretsUIHandler(t *testing.T) (http.Handler, string) {
	t.Helper()
	k := uiFixtureKB(t, "docs")
	if err := os.MkdirAll(filepath.Join(k.Root, "secrets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(k.Root, "secrets", "demo.sops.yaml"), []byte(uiSecretFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.sops.yaml")
	if err := os.WriteFile(outside, []byte(uiSecretFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(k.Root, "secrets", "link.sops.yaml")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	svc := "---\ntype: Service\ntitle: Svc\nsecret_refs:\n  - USER=secrets/demo.sops.yaml#/demo/username\n  - PORT=secrets/demo.sops.yaml#/demo/ports/1\n---\nBody\n"
	if err := os.WriteFile(filepath.Join(k.DataRoot(), "visible", "svc.md"), []byte(svc), 0o644); err != nil {
		t.Fatal(err)
	}
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "whole", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs"}}}},
		{Token: "narrow", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "docs", Maps: []string{"visible"}}}}},
	})
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	return ts.Middleware(multi.Handler()), outside
}

func TestUIAPI_SecretsStructure(t *testing.T) {
	h, _ := secretsUIHandler(t)
	rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/secrets", "whole")
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var out struct{ Files []uiSecretFile }
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Files) != 1 || out.Files[0].Path != "secrets/demo.sops.yaml" {
		t.Fatalf("symlinked file must not be listed: %+v", out.Files)
	}
	f := out.Files[0]
	if len(f.Recipients) != 1 || len(f.AgeStanzas) != 1 || f.LastModified != "2026-01-01T00:00:00Z" {
		t.Fatalf("metadata: %+v", f)
	}
	want := map[string]struct {
		aad string
		by  int
	}{
		"/demo/username": {"demo:username:", 1},
		"/demo/ports/0":  {"demo:ports:", 0},
		"/demo/ports/1":  {"demo:ports:", 1},
		"/a~1b/c~0d":     {"a/b:c~d:", 0},
		"/api_token":     {"api_token:", 0},
	}
	if len(f.Entries) != len(want) {
		t.Fatalf("entries: %+v", f.Entries)
	}
	for _, e := range f.Entries {
		w, ok := want[e.Pointer]
		if !ok || e.AADPath != w.aad || len(e.ReferencedBy) != w.by || !strings.HasPrefix(e.Enc, "ENC[AES256_GCM,") {
			t.Errorf("entry %+v, want %+v", e, w)
		}
	}
}

func TestUIAPI_SecretsHiddenFromNarrowToken(t *testing.T) {
	h, _ := secretsUIHandler(t)
	if rr := getUI(t, h, UIAPIPrefix+"/kbs/docs/secrets", "narrow"); rr.Code != http.StatusNotFound {
		t.Fatalf("narrow token: %d", rr.Code)
	}
	rr := getUI(t, h, UIAPIPrefix+"/kbs", "narrow")
	if strings.Contains(rr.Body.String(), `"secrets":true`) {
		t.Fatalf("flag leaked: %s", rr.Body.String())
	}
}
