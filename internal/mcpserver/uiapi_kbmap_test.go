package mcpserver

import (
	"net/http"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
)

// revisionFixture mounts kb-a (links to kb-b), kb-b and kb-c (unrelated) and
// returns a handler with a token that reads all three whole, one that cannot
// read kb-b and one that reads kb-b only in part.
func revisionFixture(t *testing.T) (h http.Handler, a, b, c *kb.KB, rev func(token, name string) string) {
	t.Helper()
	// Not parallel: it narrows the poll's trust window, a package variable.
	setRevisionMaxAge(t, 0)
	a, b, c = uiFixtureKB(t, "kb-a"), uiFixtureKB(t, "kb-b"), uiFixtureKB(t, "kb-c")
	m := kb.NewMounts(map[string]*kb.KB{"kb-a": a, "kb-b": b, "kb-c": c}, []string{"kb-a", "kb-b", "kb-c"}, nil)
	for _, k := range []*kb.KB{a, b, c} {
		k.SetMounts(m)
	}
	putThroughServer(t, a, "visible/linker", "See [[kb:kb-b/visible/alpha]].\n")
	whole := func(names ...string) []auth.Permission {
		var p []auth.Permission
		for _, n := range names {
			p = append(p, auth.Permission{KB: n})
		}
		return p
	}
	ts := auth.NewScopedTokenStore([]auth.ScopedToken{
		{Token: "admin", Policy: auth.Policy{Permissions: whole("kb-a", "kb-b", "kb-c")}},
		{Token: "noB", Policy: auth.Policy{Permissions: whole("kb-a", "kb-c")}},
		{Token: "partB", Policy: auth.Policy{Permissions: append(whole("kb-a", "kb-c"), auth.Permission{KB: "kb-b", Maps: []string{"visible"}})}},
	})
	multi := NewMultiKBServer("test")
	for name, k := range map[string]*kb.KB{"kb-a": a, "kb-b": b, "kb-c": c} {
		multi.MountKB(name, func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	}
	multi.EnableWeb(nil)
	h = ts.Middleware(multi.Handler())
	rev = func(token, name string) string {
		t.Helper()
		return decodeUI(t, getUI(t, h, UIAPIPrefix+"/kbs/"+name+"/revision", token))["revision"].(string)
	}
	return
}

// AC-atlas-kb-map-2
func TestRevisionFollowsLinkedKBs(t *testing.T) {
	_, _, b, c, rev := revisionFixture(t)
	base := rev("admin", "kb-a")
	if !strings.Contains(base, "-x") {
		t.Fatalf("a cross-linked KB has no suffix: %q", base)
	}
	if again := rev("admin", "kb-a"); again != base {
		t.Fatalf("revision moved with nothing changed: %q -> %q", base, again)
	}
	putThroughServer(t, b, "visible/added", "new page\n")
	afterAdd := rev("admin", "kb-a")
	if afterAdd == base {
		t.Fatal("revision of kb-a did not move when linked kb-b gained a page")
	}
	putThroughServer(t, b, "visible/added", "")
	if rev("admin", "kb-a") == afterAdd {
		t.Fatal("revision of kb-a did not move when linked kb-b lost a page")
	}
	// The other direction: kb-b gains a link to kb-a; kb-a is linked from it.
	before := rev("admin", "kb-a")
	putThroughServer(t, b, "visible/back", "Back to [[kb:kb-a/visible/alpha]].\n")
	if rev("admin", "kb-a") == before {
		t.Fatal("revision of kb-a did not move when kb-b gained a link to it")
	}
	// An unrelated KB moves nothing.
	before = rev("admin", "kb-a")
	putThroughServer(t, c, "visible/other", "unrelated\n")
	if rev("admin", "kb-a") != before {
		t.Fatal("revision of kb-a moved for an unrelated KB")
	}
}

// AC-atlas-kb-map-3
func TestRevisionBlindToUnreadableKB(t *testing.T) {
	_, _, b, _, rev := revisionFixture(t)
	for _, tok := range []string{"noB", "partB"} {
		before := rev(tok, "kb-a")
		if strings.Contains(before, "-x") {
			t.Fatalf("%s: suffix for a KB it cannot read whole: %q", tok, before)
		}
		putThroughServer(t, b, "visible/added", "new page\n")
		putThroughServer(t, b, "visible/back", "Back to [[kb:kb-a/visible/alpha]].\n")
		if after := rev(tok, "kb-a"); after != before {
			t.Fatalf("%s: revision moved for kb-b: %q -> %q", tok, before, after)
		}
		putThroughServer(t, b, "visible/added", "")
		putThroughServer(t, b, "visible/back", "")
	}
	// A KB with no cross link has today's token, no suffix.
	if tok := rev("admin", "kb-c"); strings.Contains(tok, "-x") {
		t.Fatalf("kb-c has no cross link yet carries a suffix: %q", tok)
	}
}
