package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"sort"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/config"
)

// authReload is what serve hands serveHTTP to reload auth on a running server:
// the loader that repeats startup's configuration resolution, and the config
// file to watch (empty: env and flags only, SIGHUP is the only trigger).
type authReload struct {
	Load func() (config.AuthConfig, error)
	Path string
}

// start runs the reloader for the life of the server: SIGHUP, and the poll of
// the file every interval (zero: no poll). The returned function stops it.
func (a *authReload) start(store *auth.TokenStore, started []auth.ScopedToken, mode string, interval time.Duration) func() {
	r := newAuthReloader(store, a.Load, a.Path, started, mode)
	var tick <-chan time.Time
	stopTick := func() {}
	if interval > 0 && a.Path != "" {
		t := time.NewTicker(interval)
		tick, stopTick = t.C, t.Stop
	}
	sig, stopSig := watchReloadSignal()
	stop := make(chan struct{})
	go r.run(tick, sig, stop)
	return func() { close(stop); stopTick(); stopSig() }
}

// authReloader applies a changed auth section (tokens, roles) to a running
// server without a restart. It owns no policy: it re-runs the same loading and
// compile path as startup (loadServeConfig, resolveAuth, scopedTokensWithRoles)
// and swaps the result into the store, so a reloaded token set is exactly what
// a restart would have produced. Every failure keeps the tokens in force.
type authReloader struct {
	store *auth.TokenStore
	// load re-reads the configuration the way startup did (YAML, env, flags,
	// validation).
	load func() (config.AuthConfig, error)
	// path is the config file watched; empty when the server runs on env and
	// flags alone, in which case only SIGHUP reloads.
	path string
	now  func() time.Time

	// mode is the auth mode the server started with; a reload that names
	// another one is refused (switching on/auto/off needs a restart).
	mode string

	mu     sync.Mutex // serialises reloads: a signal racing a tick
	tokens map[string]string
	// fileHash is the content last applied (or found to change nothing); a
	// refused content is not recorded, so the poll retries it. refused is the
	// last refused content, so the retry does not repeat the log line.
	fileHash [sha256.Size]byte
	refused  [sha256.Size]byte
	missing  bool
	other    [sha256.Size]byte
}

// newAuthReloader records the state of the tokens and of the file the server
// started with.
func newAuthReloader(store *auth.TokenStore, load func() (config.AuthConfig, error), path string, started []auth.ScopedToken, mode string) *authReloader {
	r := &authReloader{store: store, load: load, path: path, now: time.Now, mode: mode, tokens: tokenFingerprints(started)}
	r.fileHash, r.other, _ = readConfigHashes(path)
	return r
}

// run reloads on every tick (when the poll is on) and on every signal until
// stop closes. A nil tick or signal channel never fires.
func (r *authReloader) run(tick <-chan time.Time, sig <-chan struct{}, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-tick:
			r.poll()
		case <-sig:
			r.Reload("SIGHUP")
		}
	}
}

// poll reloads when the config file bytes changed since the last look. An
// unreadable or missing file keeps the store and is logged once per change of
// state, not once per tick.
func (r *authReloader) poll() {
	if r.path == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	file, other, err := readConfigHashes(r.path)
	if err != nil {
		if !r.missing {
			log.Printf("auth reload: config file unreadable, keeping the tokens in force: %v", err)
			r.missing = true
		}
		return
	}
	r.missing = false
	if file == r.fileHash {
		return
	}
	r.reloadLocked("config file changed", file, other, false)
}

// Reload applies the configuration now (the SIGHUP path), refused content
// included: it is the operator asking again.
func (r *authReloader) Reload(why string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	file, other, _ := readConfigHashes(r.path)
	r.reloadLocked(why, file, other, true)
}

// refuse logs a refusal and keeps the tokens in force. The poll retries a
// refused content on the next tick without repeating the line; the content is
// retried until it changes, so a half-saved file that is completed applies.
func (r *authReloader) refuse(file [sha256.Size]byte, force bool, why, msg string, args ...any) {
	if force || file != r.refused {
		log.Printf("auth reload (%s) refused, keeping the tokens in force: %s", why, fmt.Sprintf(msg, args...))
	}
	r.refused = file
}

func (r *authReloader) reloadLocked(why string, file, other [sha256.Size]byte, force bool) {
	cfg, err := r.load()
	if err != nil {
		r.refuse(file, force, why, "%v", err)
		return
	}
	on, err := resolveAuth(cfg)
	if err != nil {
		r.refuse(file, force, why, "%v", err)
		return
	}
	if !on {
		r.refuse(file, force, why, "the new configuration turns authentication off; that needs a restart")
		return
	}
	if cfg.Mode != r.mode {
		r.refuse(file, force, why, "auth.mode is %q, the running server started with %q; changing the mode needs a restart", cfg.Mode, r.mode)
		return
	}
	// A token whose expires_at has passed since startup is not an error: the
	// store already refuses it at lookup, and the edit that removes a leaked
	// token must not be blocked by one that merely ran out. It is dropped
	// from the new set, by id.
	cfg.Tokens = r.dropExpired(cfg.Tokens, !force && file == r.refused)
	scoped := scopedTokensWithRoles(cfg.Tokens, cfg.Roles)
	next := tokenFingerprints(scoped)
	added, removed, changed := diffFingerprints(r.tokens, next)
	if added+removed+changed == 0 {
		if other != r.other {
			log.Printf("auth reload (%s): only non-auth keys changed, restart required for non-auth keys", why)
			r.other = other
		}
		r.fileHash, r.refused = file, [sha256.Size]byte{}
		return
	}
	if err := r.store.Replace(scoped); err != nil {
		r.refuse(file, force, why, "%v", err)
		return
	}
	r.tokens = next
	r.other = other
	r.fileHash, r.refused = file, [sha256.Size]byte{}
	log.Printf("auth reloaded (%s): %d token(s) added, %d removed, %d changed; %d in force", why, added, removed, changed, r.store.TokenCount())
	logExpiringTokens(scoped, r.now())
}

// dropExpired removes the tokens already past expires_at, logging each id.
func (r *authReloader) dropExpired(specs []config.TokenSpec, quiet bool) []config.TokenSpec {
	now := r.now()
	kept := make([]config.TokenSpec, 0, len(specs))
	for i, t := range specs {
		if t.ExpiresAt != "" {
			if exp, err := time.Parse(time.RFC3339, t.ExpiresAt); err == nil && !exp.After(now) {
				id := t.ID
				if id == "" {
					id = fmt.Sprintf("#%d", i)
				}
				if !quiet {
					log.Printf("auth reload: token %q expired at %s, left out of the new set", id, exp.UTC().Format(time.RFC3339))
				}
				continue
			}
		}
		kept = append(kept, t)
	}
	return kept
}

// tokenFingerprints maps each token's id to a digest of everything it grants
// (value included, hashed), so a reload can count additions, removals and
// changes without ever printing a value. A token with no id is keyed by the
// digest of its value, as the store names it.
func tokenFingerprints(tokens []auth.ScopedToken) map[string]string {
	out := make(map[string]string, len(tokens))
	for _, t := range tokens {
		sum := sha256.Sum256([]byte(fmt.Sprintf("%#v", t)))
		id := t.Principal
		if id == "" {
			v := sha256.Sum256([]byte(t.Token))
			id = fmt.Sprintf("token-%x", v[:8])
		}
		out[id] = fmt.Sprintf("%x", sum)
	}
	return out
}

func diffFingerprints(old, next map[string]string) (added, removed, changed int) {
	for id, fp := range next {
		prev, ok := old[id]
		switch {
		case !ok:
			added++
		case prev != fp:
			changed++
		}
	}
	for id := range old {
		if _, ok := next[id]; !ok {
			removed++
		}
	}
	return
}

// readConfigHashes returns the digest of the file bytes and the digest of the
// file without its auth section, so a change outside auth is told apart.
func readConfigHashes(path string) (file, other [sha256.Size]byte, err error) {
	if path == "" {
		return file, other, fmt.Errorf("no config file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return file, other, err
	}
	file = sha256.Sum256(data)
	var doc map[string]any
	if yaml.Unmarshal(data, &doc) != nil {
		other = file // unparsable: any change counts as outside auth
		return file, other, nil
	}
	delete(doc, "auth")
	keys := make([]string, 0, len(doc))
	for k := range doc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	for _, k := range keys {
		b, _ := yaml.Marshal(map[string]any{k: doc[k]})
		buf.Write(b)
	}
	other = sha256.Sum256(buf.Bytes())
	return file, other, nil
}
