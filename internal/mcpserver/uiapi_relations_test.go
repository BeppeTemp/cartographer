package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// kbListRows serves GET /kbs for ctx over the KBs of f and returns each row's
// raw JSON by KB name.
func (f *relFixture) kbListRows(t *testing.T, ctx context.Context) map[string]json.RawMessage {
	t.Helper()
	return f.kbListRowsWith(t, ctx, nil)
}

// kbListRowsWith is kbListRows after mutate has changed the mounted server.
func (f *relFixture) kbListRowsWith(t *testing.T, ctx context.Context, mutate func(*MultiKBServer)) map[string]json.RawMessage {
	t.Helper()
	multi := NewMultiKBServer("test")
	for name, k := range f.byName {
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{BundleFS: fstest.MapFS{}}) })
	}
	if mutate != nil {
		mutate(multi)
	}
	rr := httptest.NewRecorder()
	multi.uiListKBs(rr, httptest.NewRequest(http.MethodGet, UIAPIPrefix+"/kbs", nil).WithContext(ctx))
	if rr.Code != http.StatusOK {
		t.Fatalf("GET /kbs: %d %s", rr.Code, rr.Body)
	}
	var out struct {
		KBs []json.RawMessage `json:"kbs"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	rows := map[string]json.RawMessage{}
	for _, raw := range out.KBs {
		var n struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &n); err != nil {
			t.Fatal(err)
		}
		rows[n.Name] = raw
	}
	return rows
}

// rowField is the raw value of one key of a row, and whether it is there.
func rowField(t *testing.T, row json.RawMessage, key string) (json.RawMessage, bool) {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(row, &m); err != nil {
		t.Fatal(err)
	}
	v, ok := m[key]
	return v, ok
}

// AC-kbrel-16
func TestKBListRelations(t *testing.T) {
	t.Parallel()
	f := newRelFixture(t, []string{"client-kb-1", "core-kb", "lone-kb"}, map[string][][2]string{
		"client-kb-1": {{"core-kb", "core-kb"}},
	})
	rows := f.kbListRows(t, adminCtx)
	if v, ok := rowField(t, rows["client-kb-1"], "relations"); !ok || !strings.Contains(string(v), `"resolved":["core-kb"]`) {
		t.Errorf("client-kb-1 relations = %s (present %v)", v, ok)
	}
	for _, n := range []string{"core-kb", "lone-kb"} {
		if v, ok := rowField(t, rows[n], "relations"); ok {
			t.Errorf("%s declares none but carries relations %s", n, v)
		}
	}

	// A caller with a grant on the client KB only: the target is named as
	// declared, never as a row and never as resolved.
	rows = f.kbListRows(t, readsOnly("client-kb-1"))
	if _, ok := rows["core-kb"]; ok {
		t.Error("a KB the caller has no grant on is listed")
	}
	v, _ := rowField(t, rows["client-kb-1"], "relations")
	if !strings.Contains(string(v), `"reason":"not_mounted"`) || strings.Contains(string(v), `"resolved":["core-kb"]`) {
		t.Errorf("relations for a caller who cannot read the target = %s", v)
	}
}

// AC-kbrel-17
func TestKBListAndSyncPullAgree(t *testing.T) {
	t.Parallel()
	f := newRelFixture(t, []string{"client-kb-1", "core-kb", "base-kb"}, map[string][][2]string{
		"client-kb-1": {{"core-kb", "core-kb"}, {"other-kb", "other-kb"}},
		"core-kb":     {{"base-kb", "base-kb"}},
	})
	for name, ctx := range map[string]context.Context{
		"admin":       adminCtx,
		"both":        readsOnly("client-kb-1", "core-kb"),
		"client only": readsOnly("client-kb-1"),
	} {
		list, _ := rowField(t, f.kbListRows(t, ctx)["client-kb-1"], "relations")
		pull, text, isErr := f.pullRelations(t, ctx, "client-kb-1")
		if isErr {
			t.Fatalf("%s: %s", name, text)
		}
		if string(list) != string(pull) {
			t.Errorf("%s: /kbs = %s, sync_pull = %s", name, list, pull)
		}
	}
}

// AC-kbrel-18
func TestKBListRelationsUnavailable(t *testing.T) {
	t.Parallel()
	f := newRelFixture(t, []string{"client-kb-1", "core-kb", "lone-kb"}, map[string][][2]string{
		"client-kb-1": {{"core-kb", "core-kb"}},
	})
	// No list before the failure: a read within relationsMaxAge would be
	// answered from memory and hide it.
	f.byName["core-kb"].NormalizedOrigin()
	if err := os.RemoveAll(filepath.Join(f.byName["core-kb"].Root, ".git")); err != nil {
		t.Fatal(err)
	}
	rows := f.kbListRows(t, adminCtx)
	if v, ok := rowField(t, rows["client-kb-1"], "relations_unavailable"); !ok || string(v) != "true" {
		t.Errorf("client-kb-1 row = %s", rows["client-kb-1"])
	}
	if _, ok := rowField(t, rows["client-kb-1"], "relations"); ok {
		t.Errorf("an unavailable row still carries relations: %s", rows["client-kb-1"])
	}
	for _, key := range []string{"relations", "relations_unavailable"} {
		if v, ok := rowField(t, rows["lone-kb"], key); ok {
			t.Errorf("an unrelated row carries %s = %s", key, v)
		}
	}
	if v, _ := rowField(t, rows["lone-kb"], "ready"); string(v) != "true" {
		t.Errorf("an unrelated row = %s", rows["lone-kb"])
	}
}

// AC-kbrel-27
func TestKBListRelationsWholeOnly(t *testing.T) {
	t.Parallel()
	partial := func(kbName string) context.Context {
		return auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "u", Policy: auth.Policy{
			Permissions: []auth.Permission{{KB: kbName, Maps: []string{"visible"}}, {KB: "core-kb"}},
		}})
	}
	deps := map[string][][2]string{"client-kb-1": {{"core-kb", "core-kb"}}}
	for name, names := range map[string][]string{
		"mounted":   {"client-kb-1", "core-kb", "lone-kb"},
		"unmounted": {"client-kb-1", "lone-kb"},
	} {
		f := newRelFixture(t, names, deps)
		for _, n := range []string{"client-kb-1", "lone-kb"} {
			if err := f.byName[n].CreateMap("visible", "Visible", "map", nil); err != nil {
				t.Fatal(err)
			}
		}
		check := func(label string) {
			t.Helper()
			rows := f.kbListRows(t, partial("client-kb-1"))
			if _, ok := rows["client-kb-1"]; !ok {
				t.Fatalf("%s/%s: a partial reader's row is not listed", name, label)
			}
			for _, key := range []string{"relations", "relations_unavailable"} {
				if v, ok := rowField(t, rows["client-kb-1"], key); ok {
					t.Errorf("%s/%s: partial reader's row has %s = %s", name, label, key, v)
				}
			}
		}
		check("ok")
		if name == "mounted" {
			f.byName["core-kb"].NormalizedOrigin()
			if err := os.RemoveAll(filepath.Join(f.byName["core-kb"].Root, ".git")); err != nil {
				t.Fatal(err)
			}
			check("failing read")
		}
	}
}

// A row that is not ready never computes its relations: a failing read would
// repeat its git calls on every poll.
func TestKBListRelationsOnlyForReadyKBs(t *testing.T) {
	t.Parallel()
	f := newRelFixture(t, []string{"client-kb-1", "core-kb"}, map[string][][2]string{
		"client-kb-1": {{"core-kb", "core-kb"}},
	})
	f.byName["core-kb"].NormalizedOrigin()
	if err := os.RemoveAll(filepath.Join(f.byName["core-kb"].Root, ".git")); err != nil {
		t.Fatal(err)
	}
	rows := f.kbListRowsWith(t, adminCtx, func(m *MultiKBServer) {
		for i := range m.kbs {
			m.kbs[i].Status = "degraded"
		}
	})
	for _, n := range []string{"client-kb-1", "core-kb"} {
		for _, key := range []string{"relations", "relations_unavailable"} {
			if v, ok := rowField(t, rows[n], key); ok {
				t.Errorf("not-ready row %s carries %s = %s", n, key, v)
			}
		}
	}
}

// A KB with a remote whose default branch is not known yet cannot read its
// relations, even when it declares none: the row says so, deliberately.
func TestKBListRelationsUnavailableWithoutDefaultBranch(t *testing.T) {
	t.Parallel()
	f := newRelFixture(t, []string{"lone-kb"}, nil)
	relGit(t, f.byName["lone-kb"], "symbolic-ref", "--delete", "refs/remotes/origin/HEAD")
	rows := f.kbListRows(t, adminCtx)
	if v, ok := rowField(t, rows["lone-kb"], "relations_unavailable"); !ok || string(v) != "true" {
		t.Errorf("row = %s", rows["lone-kb"])
	}
	if _, ok := rowField(t, rows["lone-kb"], "relations"); ok {
		t.Errorf("row carries relations: %s", rows["lone-kb"])
	}
}

// Two callers on one KB: the one who cannot read the target gets the bytes of
// a KB whose target is not mounted at all.
func TestKBListRelationsReaderAndNonReader(t *testing.T) {
	t.Parallel()
	deps := map[string][][2]string{"client-kb-1": {{"core-kb", "core-kb"}}}
	withCore := newRelFixture(t, []string{"client-kb-1", "core-kb"}, deps)
	without := newRelFixture(t, []string{"client-kb-1"}, deps)

	seen, _ := rowField(t, withCore.kbListRows(t, readsOnly("client-kb-1", "core-kb"))["client-kb-1"], "relations")
	if !strings.Contains(string(seen), `"resolved":["core-kb"]`) {
		t.Errorf("reader of the target: %s", seen)
	}
	blind, _ := rowField(t, withCore.kbListRows(t, readsOnly("client-kb-1"))["client-kb-1"], "relations")
	absent, _ := rowField(t, without.kbListRows(t, readsOnly("client-kb-1"))["client-kb-1"], "relations")
	if string(blind) != string(absent) || !strings.Contains(string(blind), `"reason":"not_mounted"`) {
		t.Errorf("non-reader = %s, unmounted = %s", blind, absent)
	}
}
