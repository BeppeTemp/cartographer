package mcpserver

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/hooks"
)

type hookRegistryKey struct{}

// withHookRegistry hands the server's hooks to a handler: a tool is built from
// its KB alone, and the resolver point is reached from inside the handler
// (secret_resolve, service_get). callToolChecked sets it only when hooks are
// configured, so a server without hooks runs as before.
func withHookRegistry(ctx context.Context, r *hooks.Registry) context.Context {
	return context.WithValue(ctx, hookRegistryKey{}, r)
}

func hookRegistryFrom(ctx context.Context) *hooks.Registry {
	r, _ := ctx.Value(hookRegistryKey{}).(*hooks.Registry)
	return r
}

// hookPrincipal is the caller as a hook sees it: id, kind, via and groups,
// never a token or a policy.
func hookPrincipal(ctx context.Context) map[string]any {
	p := auth.PrincipalFromContext(ctx)
	kind := string(p.Kind)
	if kind == "" && p.ID != "" {
		kind = string(auth.KindHuman)
	}
	return map[string]any{"id": p.ID, "kind": kind, "via": p.Via, "groups": p.Groups}
}

// maxSecretHookKey bounds the opaque key of a hook reference.
const maxSecretHookKey = 512

// parseHookSecretRef reads the target of NAME=hook:<plugin>/<name>#<key>. The
// hook must be a configured secret_resolver, else the reference is broken.
func parseHookSecretRef(entry, name, target, key string, reg *hooks.Registry) (hooks.SecretRef, error) {
	hook := strings.TrimPrefix(target, hooks.SecretRefPrefix)
	if key == "" || len(key) > maxSecretHookKey || strings.TrimSpace(key) != key || strings.IndexFunc(key, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		return hooks.SecretRef{}, fmt.Errorf("invalid secret_refs entry %q; expected NAME=hook:<plugin>/<name>#<key>", entry)
	}
	if !reg.IsResolver(hook) {
		return hooks.SecretRef{}, fmt.Errorf("reference %s: secret resolver %q is not configured on this server", name, hook)
	}
	return hooks.SecretRef{Name: name, Hook: hook, Key: key}, nil
}

// resolverAuditValue names the resolver hooks a resolution asked, sorted and
// comma-separated, for the audit's secret_resolver resource: the hook names,
// never a key or a value.
func resolverAuditValue(refs []hooks.SecretRef) string {
	seen := map[string]bool{}
	var names []string
	for _, r := range refs {
		if !seen[r.Hook] {
			seen[r.Hook] = true
			names = append(names, r.Hook)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
