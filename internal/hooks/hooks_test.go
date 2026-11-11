package hooks

import (
	"strings"
	"testing"
	"time"
)

func valid() Hook {
	return Hook{Name: "acme/review", Point: PointWriteVeto, Plugin: "acme", URL: "https://hooks.example.com/veto", Credential: "acme.token", FailurePolicy: FailClosed}
}

func TestNormalizeDefaults(t *testing.T) {
	h := valid()
	if err := h.Normalize(); err != nil {
		t.Fatal(err)
	}
	if h.Timeout != DefaultTimeout || h.Mode != ModeEnforce {
		t.Fatalf("defaults: %+v", h)
	}
	s := valid()
	s.Point, s.Name = PointAuditSink, "acme/sink"
	if err := s.Normalize(); err != nil || s.BatchSize != 100 {
		t.Fatalf("sink: %v %+v", err, s)
	}
}

func TestNormalizeRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		mut  func(*Hook)
		want string
	}{
		"name":           {func(h *Hook) { h.Name = "review" }, "<plugin>/<name>"},
		"namespace":      {func(h *Hook) { h.Plugin = "other" }, "namespace"},
		"point":          {func(h *Hook) { h.Point = "allow_all" }, "point"},
		"no policy":      {func(h *Hook) { h.FailurePolicy = "" }, "failure_policy is required"},
		"bad policy":     {func(h *Hook) { h.FailurePolicy = "ignore" }, "failure_policy"},
		"mode":           {func(h *Hook) { h.Mode = "shadow" }, "mode"},
		"timeout":        {func(h *Hook) { h.Timeout = 11 * time.Second }, "timeout"},
		"http remote":    {func(h *Hook) { h.URL = "http://hooks.example.com/v" }, "loopback"},
		"http lookalike": {func(h *Hook) { h.URL = "http://127.0.0.1.example.com/v" }, "loopback"},
		"scheme":         {func(h *Hook) { h.URL = "ftp://hooks.example.com/v" }, "scheme"},
		"userinfo":       {func(h *Hook) { h.URL = "https://user:pw@hooks.example.com/v" }, "credentials"},
		"credential":     {func(h *Hook) { h.Credential = "../etc/passwd" }, "credential"},
		"no host":        {func(h *Hook) { h.URL = "https://:443/v" }, "no host"},
		"no credential":  {func(h *Hook) { h.Credential = "" }, "credential"},
		"kbs on sink":    {func(h *Hook) { h.Point, h.KBs = PointAuditSink, []string{"kb-a"} }, "kbs"},
		"batch on veto":  {func(h *Hook) { h.BatchSize = 5 }, "audit_sink only"},
		"ttl on veto":    {func(h *Hook) { h.CacheTTL = time.Second }, "cache_ttl"},
		"ttl too long":   {func(h *Hook) { h.Point, h.CacheTTL = PointIdentity, time.Hour }, "cache_ttl"},
		"resolver open":  {func(h *Hook) { h.Point, h.FailurePolicy = PointSecretResolver, FailOpen }, "fail_closed"},
	} {
		t.Run(name, func(t *testing.T) {
			h := valid()
			tc.mut(&h)
			err := h.Normalize()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestLoopbackHTTPAccepted(t *testing.T) {
	for _, u := range []string{"http://127.0.0.1:9/v", "http://localhost/v", "http://[::1]:8080/v"} {
		h := valid()
		h.URL = u
		if err := h.Normalize(); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func TestNormalizeAllDuplicate(t *testing.T) {
	err := NormalizeAll([]Hook{valid(), valid()})
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("got %v", err)
	}
}

func TestAppliesTo(t *testing.T) {
	h := valid()
	if !h.AppliesTo("kb-a") {
		t.Fatal("no kbs means every KB")
	}
	h.KBs = []string{"kb-b"}
	if h.AppliesTo("kb-a") || !h.AppliesTo("kb-b") {
		t.Fatal("kbs narrows")
	}
}

func TestCacheTTLDefaultsForConsumers(t *testing.T) {
	for _, p := range []string{PointSecretResolver, PointIdentity} {
		h := valid()
		h.Point, h.Name = p, "acme/x"
		h.FailurePolicy = FailClosed
		if err := h.Normalize(); err != nil || h.CacheTTL != DefaultCacheTTL {
			t.Errorf("%s: %v %s", p, err, h.CacheTTL)
		}
	}
	h := valid()
	if err := h.Normalize(); err != nil || h.CacheTTL != 0 {
		t.Fatalf("a veto has no cache: %v %s", err, h.CacheTTL)
	}
}
