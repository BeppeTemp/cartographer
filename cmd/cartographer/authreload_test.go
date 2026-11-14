package main

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/config"
)

const reloadBase = "http: \"127.0.0.1:9090\"\nauth:\n  mode: on\n  tokens:\n    - token: tok-aaaa-0123456789\n      id: a\n      scopes: [kb:kb-a:rw]\n"

// reloadFixture starts a store from a temp config file the way serve does and
// returns the reloader, the store and the file path.
func reloadFixture(t *testing.T, body string) (*authReloader, *auth.TokenStore, string) {
	t.Helper()
	t.Setenv("CARTOGRAPHER_TOKENS", "")
	t.Setenv("CARTOGRAPHER_AUTH", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(body)
	load := func() (config.AuthConfig, error) {
		c, err := loadServeConfigAt(serveFlagSet(), config.FlagOverrides{}, path, true)
		if err != nil {
			return config.AuthConfig{}, err
		}
		return c.Auth, nil
	}
	cfg, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path)
	if err != nil {
		t.Fatal(err)
	}
	scoped := scopedTokensWithRoles(cfg.Auth.Tokens, cfg.Auth.Roles)
	proxy := proxyConfigFrom(cfg.Auth.ProxyIdentity, cfg.Auth.Roles)
	store := auth.NewScopedTokenStore(scoped).SetProxy(proxy).RequireAuth()
	return newAuthReloader(store, load, path, scoped, proxy, cfg.Auth.Mode), store, path
}

func reloadLog(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	return &buf
}

func TestAuthReloadAppliesAddRemoveAndKeepsOnInvalid(t *testing.T) {
	r, store, path := reloadFixture(t, reloadBase)
	buf := reloadLog(t)
	rewrite := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Add a token: the next tick applies it.
	rewrite(reloadBase + "    - token: tok-bbbb-0123456789\n      id: b\n      scopes: [kb:kb-a:r]\n")
	r.poll()
	if id, ok := store.Validate("tok-bbbb-0123456789"); !ok || id != "b" {
		t.Fatalf("the added token = %q, %v\n%s", id, ok, buf)
	}
	if !strings.Contains(buf.String(), "1 token(s) added, 0 removed, 0 changed") {
		t.Errorf("the log does not count the change:\n%s", buf)
	}
	if strings.Contains(buf.String(), "tok-bbbb") {
		t.Error("the log shows a token value")
	}

	// An unchanged file is not reloaded.
	buf.Reset()
	r.poll()
	if buf.Len() != 0 {
		t.Errorf("an unchanged file logged:\n%s", buf)
	}

	// An invalid file keeps the store.
	rewrite(reloadBase + "    - token: tok-cccc\n      id: c\n      scopes: [bogus]\n")
	r.poll()
	if _, ok := store.Validate("tok-bbbb-0123456789"); !ok {
		t.Error("an invalid file dropped the tokens in force")
	}
	if !strings.Contains(buf.String(), "refused, keeping the tokens in force") {
		t.Errorf("the refusal is not logged:\n%s", buf)
	}

	// Removing a token refuses it.
	rewrite(reloadBase)
	r.poll()
	if _, ok := store.Validate("tok-bbbb-0123456789"); ok {
		t.Error("the removed token still validates")
	}
	if _, ok := store.Validate("tok-aaaa-0123456789"); !ok {
		t.Error("the kept token was refused")
	}

	// Deleting the file keeps the store.
	buf.Reset()
	os.Remove(path)
	r.poll()
	r.poll()
	if _, ok := store.Validate("tok-aaaa-0123456789"); !ok {
		t.Error("a deleted file dropped the tokens")
	}
	if strings.Count(buf.String(), "unreadable") != 1 {
		t.Errorf("a missing file should be logged once:\n%s", buf)
	}
}

func TestAuthReloadRefusesAuthOffAndNonAuthOnly(t *testing.T) {
	r, store, path := reloadFixture(t, reloadBase)
	buf := reloadLog(t)

	// A change outside auth: ignored with one line.
	if err := os.WriteFile(path, []byte(reloadBase+"update_check: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.poll()
	if !strings.Contains(buf.String(), "restart required for non-auth keys") {
		t.Errorf("the non-auth change is not reported:\n%s", buf)
	}

	// Auth turned off: refused.
	buf.Reset()
	if err := os.WriteFile(path, []byte("http: \"127.0.0.1:9090\"\nauth:\n  mode: off\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.poll()
	if _, ok := store.Validate("tok-aaaa-0123456789"); !ok || !strings.Contains(buf.String(), "turns authentication off") {
		t.Errorf("auth off was not refused:\n%s", buf)
	}
}

// The loop reloads on a tick (injected) and on the reload signal, and stops.
func TestAuthReloadRunTickAndSignal(t *testing.T) {
	r, store, path := reloadFixture(t, reloadBase)
	reloadLog(t)
	tick := make(chan time.Time)
	sig := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { r.run(tick, sig, stop); close(done) }()

	if err := os.WriteFile(path, []byte(reloadBase+"    - token: tok-bbbb-0123456789\n      id: b\n      scopes: [kb:kb-a:r]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tick <- time.Now()
	sig <- struct{}{} // the loop took the tick, so it has finished the reload before this send returns
	if _, ok := store.Validate("tok-bbbb-0123456789"); !ok {
		t.Error("the tick did not apply the file")
	}
	close(stop)
	<-done
}

// The expiry warning is repeated on a reload, not only at startup.
func TestAuthReloadWarnsOfExpiringTokens(t *testing.T) {
	r, _, path := reloadFixture(t, reloadBase)
	buf := reloadLog(t)
	soon := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	body := reloadBase + "    - token: tok-dddd-0123456789\n      id: d\n      kind: client\n      expires_at: \"" + soon + "\"\n      scopes: [kb:kb-a:r]\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	r.poll()
	if !strings.Contains(buf.String(), `token "d" expires at`) {
		t.Errorf("no expiry warning on reload:\n%s", buf)
	}
}

// A token that expired after startup does not block the edit that removes
// another one (the leaked-token case), and is left out with a log line.
func TestAuthReloadSurvivesATokenExpiredSinceStartup(t *testing.T) {
	soon := time.Now().Add(2 * time.Second).UTC().Format(time.RFC3339)
	entry := "    - token: tok-eeee-0123456789\n      id: e\n      kind: client\n      expires_at: \"" + soon + "\"\n      scopes: [kb:kb-a:r]\n"
	leaked := "    - token: tok-ffff-0123456789\n      id: f\n      scopes: [kb:kb-a:rw]\n"
	r, store, path := reloadFixture(t, reloadBase+entry+leaked)
	buf := reloadLog(t)
	r.now = func() time.Time { return time.Now().Add(time.Hour) } // the entry has run out since startup
	if err := os.WriteFile(path, []byte(reloadBase+entry), 0o600); err != nil {
		t.Fatal(err)
	}
	r.poll()
	if _, ok := store.Validate("tok-ffff-0123456789"); ok {
		t.Errorf("the leaked token was not removed:\n%s", buf)
	}
	if !strings.Contains(buf.String(), `token "e" expired`) {
		t.Errorf("the expired entry is not named:\n%s", buf)
	}
}

// A refused content is retried by the poll (without repeating the line), so a
// half-saved file that is then completed applies.
func TestAuthReloadRetriesRefusedContent(t *testing.T) {
	r, store, path := reloadFixture(t, reloadBase)
	buf := reloadLog(t)
	bad := reloadBase + "    - token: tok-gggg-0123456789\n      id: g\n      scopes: [bogus]\n"
	os.WriteFile(path, []byte(bad), 0o600)
	r.poll()
	r.poll()
	if strings.Count(buf.String(), "refused") != 1 {
		t.Errorf("the refusal should be logged once:\n%s", buf)
	}
	// The failure was environmental, not the content: the same bytes apply
	// once the loader accepts them.
	good := r.load
	r.load = func() (config.AuthConfig, error) {
		cfg, err := good()
		if err == nil {
			return cfg, nil
		}
		return config.AuthConfig{Mode: "on", Tokens: []config.TokenSpec{{Token: "tok-aaaa-0123456789", ID: "a", Scopes: []string{"kb:kb-a:rw"}}, {Token: "tok-gggg-0123456789", ID: "g", Scopes: []string{"kb:kb-a:r"}}}}, nil
	}
	r.poll()
	if _, ok := store.Validate("tok-gggg-0123456789"); !ok {
		t.Errorf("a refused content was never retried:\n%s", buf)
	}
}

// Switching the auth mode (on to auto) is refused: it needs a restart.
func TestAuthReloadRefusesAModeChange(t *testing.T) {
	r, store, path := reloadFixture(t, reloadBase)
	buf := reloadLog(t)
	os.WriteFile(path, []byte(strings.Replace(reloadBase, "mode: on", "mode: auto", 1)+"    - token: tok-hhhh-0123456789\n      id: h\n      scopes: [kb:kb-a:r]\n"), 0o600)
	r.poll()
	if _, ok := store.Validate("tok-hhhh-0123456789"); ok || !strings.Contains(buf.String(), "changing the mode needs a restart") {
		t.Errorf("a mode change was applied or not explained:\n%s", buf)
	}
}

// A signal reload racing a poll: serialised by the mutex, run under -race.
func TestAuthReloadRacingTriggers(t *testing.T) {
	r, store, path := reloadFixture(t, reloadBase)
	reloadLog(t)
	os.WriteFile(path, []byte(reloadBase+"    - token: tok-iiii-0123456789\n      id: i\n      scopes: [kb:kb-a:r]\n"), 0o600)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); r.poll() }()
		go func() { defer wg.Done(); r.Reload("SIGHUP") }()
	}
	wg.Wait()
	if _, ok := store.Validate("tok-iiii-0123456789"); !ok || store.TokenCount() != 2 {
		t.Errorf("racing reloads left %d token(s)", store.TokenCount())
	}
}

// With a refused content retried every tick, the "expired, left out" line is
// logged once too, not once per tick.
func TestAuthReloadLogsAnExpiredDropOnce(t *testing.T) {
	soon := time.Now().Add(2 * time.Second).UTC().Format(time.RFC3339)
	entry := "    - token: tok-jjjj-0123456789\n      id: j\n      kind: client\n      expires_at: \"" + soon + "\"\n      scopes: [kb:kb-a:r]\n"
	r, _, path := reloadFixture(t, reloadBase+entry)
	buf := reloadLog(t)
	r.now = func() time.Time { return time.Now().Add(time.Hour) }
	// Only the expired entry is left: Replace refuses an empty set, every tick.
	os.WriteFile(path, []byte("http: \"127.0.0.1:9090\"\nauth:\n  mode: on\n  tokens:\n"+entry), 0o600)
	for i := 0; i < 3; i++ {
		r.poll()
	}
	if n := strings.Count(buf.String(), `token "j" expired`); n != 1 {
		t.Errorf("the drop line was logged %d times:\n%s", n, buf)
	}
}

const reloadProxy = `http: "127.0.0.1:9090"
auth:
  mode: on
  roles:
    - name: reader
      rules: [{kb: kb-a, access: r}]
    - name: editor
      rules: [{kb: kb-a, access: rw}]
  proxy_identity:
    trusted_addresses: [192.0.2.0/24]
    user_header: X-User
    groups_header: X-Groups
    group_roles: {GROUPS}
  tokens:
    - {token: tok-sso-0123456789, id: sso, proxy: true, roles: [editor]}
`

func proxyReq(store *auth.TokenStore, user string) (int, auth.Principal) {
	var got auth.Principal
	h := store.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = auth.PrincipalFromContext(r.Context())
	}))
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.RemoteAddr = "192.0.2.10:5555"
	req.Header.Set("Authorization", "Bearer tok-sso-0123456789")
	req.Header.Set("X-User", user)
	req.Header.Set("X-Groups", "staff")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, got
}

// The proxy identity block reloads with tokens and roles, and a reload that
// breaks it keeps the previous mapping (the D179 fail-closed rule).
func TestAuthReloadAppliesProxyIdentity(t *testing.T) {
	r, store, path := reloadFixture(t, strings.Replace(reloadProxy, "{GROUPS}", "{staff: [reader]}", 1))
	buf := reloadLog(t)
	if _, p := proxyReq(store, "alice"); p.ID != "user:alice" || p.Policy.HasKBAccess("kb-a", true) {
		t.Fatalf("before reload: %+v", p)
	}
	// Only the group map changes: tokens are untouched, the mapping must apply.
	os.WriteFile(path, []byte(strings.Replace(reloadProxy, "{GROUPS}", "{staff: [editor]}", 1)), 0o600)
	r.poll()
	if _, p := proxyReq(store, "alice"); !p.Policy.HasKBAccess("kb-a", true) {
		t.Fatalf("a changed group map was not applied:\n%s", buf)
	}
	// An unknown role in the map is refused and the mapping in force stays.
	os.WriteFile(path, []byte(strings.Replace(reloadProxy, "{GROUPS}", "{staff: [nope]}", 1)), 0o600)
	r.poll()
	if code, p := proxyReq(store, "alice"); code != 200 || !p.Policy.HasKBAccess("kb-a", true) {
		t.Fatalf("a refused reload changed the mapping: %d %+v", code, p)
	}
}

// Unpinning a plugin while its token stays in the file revokes that token on
// reload and still applies the rest of the file (another token's removal);
// the reload is not refused as a whole.
func TestAuthReloadDropsTheTokenOfAnUnpinnedPlugin(t *testing.T) {
	pins := "plugins_dir: '" + t.TempDir() + "'\nplugins:\n  acme:\n    digest: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n"
	tokens := reloadBase +
		"    - token: tok-bbbb-0123456789\n      id: b\n      scopes: [kb:kb-a:r]\n" +
		"    - token: tok-pppp-0123456789\n      id: p\n      kind: client\n      plugin: acme\n      expires_at: \"2999-01-01T00:00:00Z\"\n      scopes: [kb:kb-a:r]\n"
	r, store, path := reloadFixture(t, pins+tokens)
	buf := reloadLog(t)
	if _, ok := store.Validate("tok-pppp-0123456789"); !ok {
		t.Fatal("the plugin token is not in force at start")
	}
	// Unpin the plugin and remove token b in the same edit.
	rest := strings.Replace(tokens, "    - token: tok-bbbb-0123456789\n      id: b\n      scopes: [kb:kb-a:r]\n", "", 1)
	if err := os.WriteFile(path, []byte(rest), 0o600); err != nil {
		t.Fatal(err)
	}
	r.poll()
	if _, ok := store.Validate("tok-pppp-0123456789"); ok {
		t.Errorf("the token of the unpinned plugin still validates\n%s", buf)
	}
	if _, ok := store.Validate("tok-bbbb-0123456789"); ok {
		t.Errorf("the other revocation in the file was dropped\n%s", buf)
	}
	if _, ok := store.Validate("tok-aaaa-0123456789"); !ok {
		t.Error("the kept token was refused")
	}
	if !strings.Contains(buf.String(), "plugin acme") || strings.Contains(buf.String(), "tok-pppp") {
		t.Errorf("the drop is not logged by id and plugin (or shows a value):\n%s", buf)
	}
}

// The pin check judges the effective token set: a plugin token in the file that
// CARTOGRAPHER_TOKENS replaces is not checked, and one that stays unpinned
// refuses the start.
func TestPluginTokenPinIsJudgedOnTheEffectiveSet(t *testing.T) {
	body := reloadBase + "    - token: tok-pppp-0123456789\n      id: p\n      kind: client\n      plugin: acme\n      expires_at: \"2999-01-01T00:00:00Z\"\n      scopes: [kb:kb-a:r]\n"
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARTOGRAPHER_TOKENS", "")
	if _, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path); err == nil || !strings.Contains(err.Error(), `plugin "acme" is not pinned`) {
		t.Fatalf("an unpinned plugin token must refuse the start: %v", err)
	}
	t.Setenv("CARTOGRAPHER_TOKENS", "tok-env-0123456789")
	if _, err := loadServeConfig(serveFlagSet(), config.FlagOverrides{}, path); err != nil {
		t.Fatalf("the replaced token set must not be judged: %v", err)
	}
}
