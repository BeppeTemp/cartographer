package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// D1096: /health follows the caller's token.

func healthAs(t *testing.T, h http.Handler, bearer string) (raw string, body map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("/health status = %d, want 200 for everyone", rr.Code)
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid /health JSON: %v", err)
	}
	return rr.Body.String(), body
}

func healthKBNames(t *testing.T, body map[string]any) []string {
	t.Helper()
	list, ok := body["kbs"].([]any)
	if !ok {
		t.Fatalf("kbs = %#v, want a list (never null)", body["kbs"])
	}
	names := []string{}
	for _, e := range list {
		names = append(names, e.(map[string]any)["name"].(string))
	}
	sort.Strings(names)
	return names
}

func TestHealth_FollowsTheCallersToken(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "work-kb")
	f.multi.SetLatestVersionSource(func() string { return "v9.9.9" })

	// Anonymous and invalid-token probes get exactly the six keys.
	for _, bearer := range []string{"", "not-a-token"} {
		raw, body := healthAs(t, f.handler, bearer)
		keys := make([]string, 0, len(body))
		for k := range body {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		want := []string{"mount_mode", "ready", "routed_path", "started_at", "status", "version"}
		if !reflect.DeepEqual(keys, want) {
			t.Errorf("bearer %q: keys = %v, want %v", bearer, keys, want)
		}
		if body["status"] != "ok" || body["ready"] != true {
			t.Errorf("bearer %q: liveness contract broken: %v", bearer, body)
		}
		for _, leak := range []string{"kb-a", "work-kb", "latest_version", "audit", "capabilities"} {
			if strings.Contains(raw, leak) {
				t.Errorf("bearer %q: anonymous /health contains %q: %s", bearer, leak, raw)
			}
		}
	}

	// A scoped token sees its KB only, with the entry intact, and latest_version.
	_, admin := healthAs(t, f.handler, tokAdmin)
	_, ro := healthAs(t, f.handler, tokRO)
	if got := healthKBNames(t, ro); !reflect.DeepEqual(got, []string{"kb-a"}) {
		t.Errorf("scoped token kbs = %v, want [kb-a]", got)
	}
	if got := healthKBNames(t, admin); !reflect.DeepEqual(got, []string{"kb-a", "work-kb"}) {
		t.Errorf("admin kbs = %v, want both", got)
	}
	adminA := admin["kbs"].([]any)[0]
	roA := ro["kbs"].([]any)[0]
	if !reflect.DeepEqual(adminA, roA) {
		t.Errorf("a visible KB's entry changed with the caller:\n%v\n%v", adminA, roA)
	}
	if ro["latest_version"] != "v9.9.9" || admin["latest_version"] != "v9.9.9" {
		t.Errorf("latest_version missing for identified callers: %v %v", ro["latest_version"], admin["latest_version"])
	}

	// A token with no grant sees [], never null.
	raw, none := healthAs(t, f.handler, tokNone)
	if got := healthKBNames(t, none); len(got) != 0 || !strings.Contains(raw, `"kbs":[]`) {
		t.Errorf("no-grant token: kbs = %v raw=%s, want []", got, raw)
	}
}

func TestHealth_AuthOffBodyIsUnchanged(t *testing.T) {
	t.Parallel()
	f := newViewFixture(t, nil, "kb-a", "work-kb")
	f.multi.SetLatestVersionSource(func() string { return "v9.9.9" })
	off := auth.NewTokenStore(nil).Middleware(f.multi.Handler())
	_, body := healthAs(t, off, "")
	if got := healthKBNames(t, body); !reflect.DeepEqual(got, []string{"kb-a", "work-kb"}) {
		t.Errorf("auth off kbs = %v, want both", got)
	}
	for _, k := range []string{"status", "version", "started_at", "ready", "mount_mode", "routed_path", "latest_version"} {
		if _, ok := body[k]; !ok {
			t.Errorf("auth off body lacks %q: %v", k, body)
		}
	}
}
