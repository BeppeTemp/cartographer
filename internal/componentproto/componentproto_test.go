package componentproto

import (
	"reflect"
	"testing"
)

func TestSkewTable(t *testing.T) {
	for _, n := range []int{1, 3} {
		for peer := n - 3; peer <= n+3; peer++ {
			want, wantOK := "", false
			switch peer {
			case n - 1:
				want, wantOK = SkewBehind, true
			case n:
				want, wantOK = SkewNone, true
			case n + 1:
				want, wantOK = SkewAhead, true
			}
			got, ok := skewAgainst(n, peer)
			if got != want || ok != wantOK {
				t.Errorf("N=%d peer=%d: got (%q,%v), want (%q,%v)", n, peer, got, ok, want, wantOK)
			}
		}
	}
	if s, ok := Skew(Protocol + 1); s != SkewAhead || !ok {
		t.Fatalf("Skew(N+1) = %q,%v", s, ok)
	}
	if _, ok := Skew(Protocol + 2); ok {
		t.Fatal("Skew(N+2) must be refused")
	}
	if !Supports(1, 2) || Supports(1, 3) || !Supports(3, 2) || Supports(3, 1) {
		t.Fatal("Supports must be the symmetric window")
	}
}

// TestGolden pins the exported names: a path, kind or state rename is a
// conscious edit of this table and of docs/control-plane.md.
func TestGolden(t *testing.T) {
	got := map[string]any{
		"protocol": Protocol, "min": MinProtocol,
		"paths":  Paths(),
		"legacy": []string{LegacyUsagePath, LegacyClientSettingsPath},
		"kinds":  Kinds(), "states": States(), "tasks": TaskKinds(),
		"codes":    []string{CodeProtocolUnsupported, CodeWrongTokenKind},
		"outcomes": Outcomes(), "alarms": ReportedAlarms(),
	}
	want := map[string]any{
		"protocol": 1, "min": 1,
		"paths":    []string{"/api/component/v1/heartbeat", "/api/component/v1/settings", "/api/component/v1/usage", "/api/component/v1/runs"},
		"legacy":   []string{"/api/usage", "/api/client-settings"},
		"kinds":    []string{"surveyor", "client", "human"},
		"states":   []string{"up", "degraded", "silent", "starting", "undeclared"},
		"tasks":    []string{"doctor", "ingest", "poll", "prompt", "repair", "synthesis"},
		"codes":    []string{"protocol_unsupported", "wrong_token_kind"},
		"outcomes": []string{"ok", "failed", "skipped", "budget_exhausted"},
		"alarms":   []string{"feed_failing", "auth_refused", "model_unreachable"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("contract changed:\n got %v\nwant %v", got, want)
	}
}
