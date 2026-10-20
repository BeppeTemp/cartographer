package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// With an identity configured for the KB, a write that sends no valid header —
// from any token, or from the server's local principal like the unattended
// repair — is by that identity as author and committer; a valid header still
// wins (D950). The token stays in the trailer.
func TestGitWrap_ConfiguredIdentity(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	k.SetDefaultIdentity("Ada Example", "ada@example.test")
	const ada = "Ada Example <ada@example.test> | Ada Example <ada@example.test>"
	wrappedWriteCtx(t, k, authorHeaderCtx(botPrincipal, ""), `{"n":1}`)
	if got := headIdent(t, k); got != ada {
		t.Errorf("token without header: %q, want %q", got, ada)
	}
	if got := headTokenTrailers(t, k); len(got) != 1 || got[0] != "reviewer-bot" {
		t.Errorf("token trailers = %q, want [reviewer-bot]", got)
	}
	wrappedWriteCtx(t, k, authorHeaderCtx(auth.LocalAdminPrincipal(), ""), `{"n":2}`)
	if got := headIdent(t, k); got != ada {
		t.Errorf("local principal: %q, want %q", got, ada)
	}
	wrappedWriteCtx(t, k, authorHeaderCtx(botPrincipal, "Zed <zed@example.test>"), `{"n":3}`)
	if got, want := headIdent(t, k), "Zed <zed@example.test> | Zed <zed@example.test>"; got != want {
		t.Errorf("header: %q, want %q", got, want)
	}
	// An invalid header is as if absent: the configured identity, plus the warning.
	res := wrappedWriteCtx(t, k, authorHeaderCtx(botPrincipal, "no email"), `{"n":4}`)
	if got := headIdent(t, k); got != ada {
		t.Errorf("invalid header: %q, want %q", got, ada)
	}
	if last := res.Content[len(res.Content)-1].Text; !strings.Contains(last, authorHeader+" header ignored") || !strings.Contains(last, "Ada Example") {
		t.Errorf("warning = %q, want it to name the configured identity", last)
	}
	// What the server commits with no writer of its own is by it too (here the
	// external-changes commit, D357).
	if err := os.WriteFile(filepath.Join(k.Root, "data", "edited.md"), []byte("---\ntype: Note\ntitle: E\n---\n# E\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitExternalChanges(k, "test_write")
	if got := headIdent(t, k); got != ada {
		t.Errorf("external changes: %q, want %q", got, ada)
	}
}

// Every write names the host that recorded it in a trailer, and a caller
// cannot forge one.
func TestGitWrap_HostTrailer(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	wrappedWriteCtx(t, k, authorHeaderCtx(botPrincipal, ""), `{"reason":"why\nCartographer-Host: forged"}`)
	out := gitOut(t, k, "log", "-1", "--format=%(trailers:key=Cartographer-Host,valueonly,separator=%x1f)")
	_, email := serverIdent()
	if want := strings.TrimPrefix(email, "cartographer@"); strings.TrimSpace(out) != want {
		t.Errorf("host trailers = %q, want only %q", out, want)
	}
}
