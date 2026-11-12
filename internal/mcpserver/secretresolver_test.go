package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/hooks"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

const resolverValue = "v4lue-from-the-vault"

type resolverHarness struct {
	k     *kb.KB
	s     *Server
	hits  atomic.Int32
	audit string

	mu       sync.Mutex
	payloads []map[string]any
	logs     []string
}

// resolverServer wires a KB server to a secret_resolver hook (acme/vault) that
// answers resolverValue. ageKey is the KB's age key ("" for none).
func resolverServer(t *testing.T, ageKey string) *resolverHarness {
	t.Helper()
	h := &resolverHarness{k: setupServiceTestKB(t, ageKey)}
	h.k.AuthName = "kb-a"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.hits.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		h.mu.Lock()
		h.payloads = append(h.payloads, body)
		h.mu.Unlock()
		fmt.Fprintf(w, `{"version":1,"value":%q}`, resolverValue)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "acme.token"), []byte("tok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := hooks.NewRegistry([]hooks.Hook{{
		Name: "acme/vault", Point: hooks.PointSecretResolver, Plugin: "acme", URL: srv.URL, Credential: "acme.token", FailurePolicy: hooks.FailClosed,
	}}, hooks.Options{CredentialsDir: dir, Approved: func(string, string) bool { return true }, Logf: func(f string, a ...any) {
		h.mu.Lock()
		h.logs = append(h.logs, fmt.Sprintf(f, a...))
		h.mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	h.audit = filepath.Join(t.TempDir(), "audit.jsonl")
	l, err := audit.OpenWithOptions(h.audit, audit.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	h.s = New("test")
	RegisterKBTools(h.s, h.k, Deps{})
	h.s.SetAuditLog(l)
	h.s.SetKBName("kb-a")
	h.s.SetHooks(reg)
	return h
}

func (h *resolverHarness) call(p auth.Principal, tool string, args map[string]any) ToolResult {
	raw, _ := json.Marshal(args)
	return h.s.callTool(auth.ContextWithPrincipal(context.Background(), p), tool, raw)
}

var resolverAdmin = auth.Principal{ID: "user", Policy: auth.Policy{Admin: true}}

func TestSecretResolver_ResolvesRedactedAndRevealed(t *testing.T) {
	t.Parallel()
	h := resolverServer(t, "") // no age key: a concept whose refs are all hook refs needs none
	writeSecretConcept(t, h.k.Root, "dossier", "type: Dossier\nsecret_refs:\n  - API=hook:acme/vault#prod/api\n")
	res := h.call(resolverAdmin, "secret_resolve", map[string]any{"concept_id": "dossier"})
	if res.IsError || resultText(res) != "API=<redacted>" {
		t.Fatalf("redacted = %q", resultText(res))
	}
	res = h.call(resolverAdmin, "secret_resolve", map[string]any{"concept_id": "dossier", "reveal": true})
	if res.IsError || resultText(res) != "API="+resolverValue {
		t.Fatalf("revealed = %q", resultText(res))
	}
	h.mu.Lock()
	p := h.payloads[0]
	h.mu.Unlock()
	if p["kb"] != "kb-a" || p["concept_id"] != "dossier" || p["name"] != "API" || p["key"] != "prod/api" || p["principal"].(map[string]any)["id"] != "user" {
		t.Fatalf("payload = %v", p)
	}
	// service_get goes through the same resolution.
	writeSecretConcept(t, h.k.Root, "svc-vault", "type: Service\nsecret_refs:\n  - API=hook:acme/vault#prod/api\n")
	res = h.call(resolverAdmin, "service_get", map[string]any{"service_id": "svc-vault", "resolve_secrets": true})
	if res.IsError || !strings.Contains(resultText(res), "API=<redacted>") {
		t.Fatalf("service_get = %q", resultText(res))
	}
	// The audit names the resolver, never the value nor the key.
	b, err := os.ReadFile(h.audit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"secret_resolver":"acme/vault"`) {
		t.Errorf("the audit does not name the resolver:\n%s", b)
	}
	if strings.Contains(string(b), resolverValue) || strings.Contains(string(b), "prod/api") {
		t.Errorf("a value or a key reached the audit:\n%s", b)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if strings.Contains(strings.Join(h.logs, "\n"), resolverValue) {
		t.Error("a value reached the log")
	}
}

func TestSecretResolver_MixedWithSOPS(t *testing.T) {
	fakeSecretSOPS(t)
	h := resolverServer(t, "/age/key")
	writeSecretConcept(t, h.k.Root, "mixed", "type: Dossier\nsecret_refs:\n  - TOKEN=secrets/test.sops.yaml#/allowed\n  - API=hook:acme/vault#prod/api\n")
	res := h.call(resolverAdmin, "secret_resolve", map[string]any{"concept_id": "mixed", "reveal": true})
	if res.IsError || resultText(res) != "API="+resolverValue+"\nTOKEN=only-this" {
		t.Fatalf("mixed = %q", resultText(res))
	}
	// names: a sops name alone never asks the hook, a hook name alone never runs sops.
	before := h.hits.Load()
	res = h.call(resolverAdmin, "secret_resolve", map[string]any{"concept_id": "mixed", "names": []string{"TOKEN"}, "reveal": true})
	if res.IsError || resultText(res) != "TOKEN=only-this" || h.hits.Load() != before {
		t.Fatalf("sops name = %q, hook hits %d -> %d", resultText(res), before, h.hits.Load())
	}
	res = h.call(resolverAdmin, "secret_resolve", map[string]any{"concept_id": "mixed", "names": []string{"API", "NOPE"}})
	if !res.IsError || !strings.Contains(resultText(res), `unknown name "NOPE"`) {
		t.Fatalf("unknown name = %q", resultText(res))
	}
	// A NAME declared twice across the two forms is refused.
	writeSecretConcept(t, h.k.Root, "dup", "type: Dossier\nsecret_refs:\n  - API=secrets/test.sops.yaml#/allowed\n  - API=hook:acme/vault#prod/api\n")
	if res = h.call(resolverAdmin, "secret_resolve", map[string]any{"concept_id": "dup"}); !res.IsError || !strings.Contains(resultText(res), "invalid secret_refs") {
		t.Fatalf("duplicate name = %q", resultText(res))
	}
}

func TestSecretResolver_UnknownHookIsABrokenReference(t *testing.T) {
	t.Parallel()
	h := resolverServer(t, "")
	writeSecretConcept(t, h.k.Root, "other", "type: Dossier\nsecret_refs:\n  - API=hook:acme/other#prod/api\n")
	res := h.call(resolverAdmin, "secret_resolve", map[string]any{"concept_id": "other"})
	if !res.IsError || !strings.Contains(resultText(res), `reference API: secret resolver "acme/other" is not configured`) || h.hits.Load() != 0 {
		t.Fatalf("unknown hook = %q (hits %d)", resultText(res), h.hits.Load())
	}
	// A server without hooks reads the same reference as broken too.
	k := setupServiceTestKB(t, "")
	writeSecretConcept(t, k.Root, "other", "type: Dossier\nsecret_refs:\n  - API=hook:acme/vault#prod/api\n")
	args, _ := json.Marshal(map[string]any{"concept_id": "other"})
	got, _ := toolSecretResolve(k).Handler(authLocalContext(), args)
	if !got.IsError || !strings.Contains(resultText(got), "is not configured") {
		t.Fatalf("no hooks = %q", resultText(got))
	}
}

// secrets: none is the core's refusal, before any hook is asked.
func TestSecretResolver_SecretsNoneNeverReachesTheHook(t *testing.T) {
	t.Parallel()
	h := resolverServer(t, "")
	writeSecretConcept(t, h.k.Root, "dossier", "type: Dossier\nsecret_refs:\n  - API=hook:acme/vault#prod/api\n")
	none := auth.Principal{ID: "user", Policy: auth.Policy{Permissions: []auth.Permission{{KB: "kb-a", Write: true, NoSecrets: true}}}}
	res := h.call(none, "secret_resolve", map[string]any{"concept_id": "dossier"})
	if !res.IsError || !strings.Contains(resultText(res), "secrets_restricted") || h.hits.Load() != 0 {
		t.Fatalf("secrets: none = %q (hits %d)", resultText(res), h.hits.Load())
	}
}
