package mcpbridge

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
)

// authorRetry is how long a folder where git named no author keeps that
// answer: the user may configure an email while the session runs. A found
// identity is kept for the session, like the scope.
const authorRetry = 30 * time.Second

type gitAuthor struct {
	value string
	at    time.Time
}

// author is the Cartographer-Author value for a tools/call (D904), "" to send
// none. The KB is the one the server will pick: the kb argument, else the
// connection's only KB, and with neither nothing is sent, as the server then
// refuses the call. A call that also writes to target_kb carries one author
// for both commits, so it is sent only when both KBs resolve to the same one.
func (b *Bridge) author(scope Scope, params json.RawMessage) string {
	var p struct {
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	_ = json.Unmarshal(params, &p)
	kb := stringArg(p.Arguments, "kb")
	if kb == "" {
		kb = soleKB(scope, b.Cfg.KnownKBs)
	}
	if kb == "" {
		return ""
	}
	a := b.identity(scope, kb)
	if t := stringArg(p.Arguments, "target_kb"); a != "" && t != "" && t != kb && b.identity(scope, t) != a {
		return ""
	}
	return a
}

// identity is the author of a write to kb: the identities entry when one
// decides, else what git resolves in the repository the KB was bound from.
// Several such repositories must agree.
func (b *Bridge) identity(scope Scope, kb string) string {
	if v, found, problem := b.Cfg.Identity(kb); found {
		if problem != "" {
			b.warnIdentity(kb, problem)
		}
		return v
	}
	dirs := scope.Dirs[kb]
	if len(dirs) == 0 {
		dirs = []string{b.Dir}
	}
	agreed := ""
	for i, d := range dirs {
		v := b.gitAuthor(d)
		if v == "" || (i > 0 && v != agreed) {
			return ""
		}
		agreed = v
	}
	return agreed
}

// gitAuthor asks git once per folder. Calls are forwarded concurrently, so
// the cache is guarded; a lookup holds the lock, and git answers in
// milliseconds (gitx bounds it).
func (b *Bridge) gitAuthor(dir string) string {
	b.authorMu.Lock()
	defer b.authorMu.Unlock()
	if c, ok := b.gitAuthors[dir]; ok && (c.value != "" || time.Since(c.at) < authorRetry) {
		return c.value
	}
	resolve := b.workTreeIdent
	if resolve == nil {
		resolve = gitx.WorkTreeIdent
	}
	v := resolve(dir)
	if b.gitAuthors == nil {
		b.gitAuthors = map[string]gitAuthor{}
	}
	b.gitAuthors[dir] = gitAuthor{value: v, at: time.Now()}
	return v
}

// warnIdentity says once per KB that its identities entry sends no author.
func (b *Bridge) warnIdentity(kb, problem string) {
	b.authorMu.Lock()
	defer b.authorMu.Unlock()
	if b.warnedIdentity[kb] {
		return
	}
	if b.warnedIdentity == nil {
		b.warnedIdentity = map[string]bool{}
	}
	b.warnedIdentity[kb] = true
	b.logf("the identities entry that applies to %s is not a valid Name <email> (%s): its writes send no author", kb, problem)
}

// soleKB is the KB a call naming none goes to: the only one of a restricted
// scope, or, when nothing restricts the session, the only one the server
// mounts. "" when several are reachable.
func soleKB(scope Scope, known []string) string {
	switch {
	case !scope.All && len(scope.KBs) == 1:
		return scope.KBs[0]
	case scope.All && len(known) == 1:
		return known[0]
	}
	return ""
}

// stringArg is a string argument as the server reads it, "" when absent or not
// a string.
func stringArg(args map[string]json.RawMessage, key string) string {
	var s string
	if json.Unmarshal(args[key], &s) != nil {
		return ""
	}
	return strings.TrimSpace(s)
}
