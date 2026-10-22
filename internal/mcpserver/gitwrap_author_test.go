package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// headIdent returns "<author> | <committer>" of HEAD as name <email>.
func headIdent(t *testing.T, k *kb.KB) string {
	t.Helper()
	out, err := exec.Command("git", "-C", k.Root, "log", "-1", "--format=%an <%ae> | %cn <%ce>").Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// wrappedWriteCtx runs one git-wrapped write under ctx and returns its result.
func wrappedWriteCtx(t *testing.T, k *kb.KB, ctx context.Context, args string) ToolResult {
	t.Helper()
	tool := gitWrap(k, Tool{Name: "test_write", Handler: func(ctx requestContext, a json.RawMessage) (ToolResult, error) {
		if err := k.WriteFileAtomic("data/wrapped.md", []byte(args+"\n")); err != nil {
			return ToolResult{}, err
		}
		return textResult(`{"ok":true}`), nil
	}})
	res, err := tool.Handler(ctx, json.RawMessage(args))
	if err != nil || res.IsError {
		t.Fatalf("wrapped handler: err=%v res=%+v", err, res)
	}
	return res
}

var botPrincipal = auth.Principal{ID: "reviewer-bot", Policy: auth.Policy{Admin: true}}

func authorHeaderCtx(p auth.Principal, header string) context.Context {
	h := http.Header{}
	if header != "" {
		h.Set(authorHeader, header)
	}
	return withAuthorHeader(auth.ContextWithPrincipal(context.Background(), p), h)
}

func headTokenTrailers(t *testing.T, k *kb.KB) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", k.Root, "log", "-1", "--format=%(trailers:key="+commitTokenKey+",valueonly,separator=%x1f)").Output()
	if err != nil {
		t.Fatal(err)
	}
	if s := strings.TrimSpace(string(out)); s != "" {
		return strings.Split(s, "\x1f")
	}
	return nil
}

func serverIdent() (ident, email string) {
	name, email := kb.ServerIdentity()
	return name + " <" + email + ">", email
}

// The author and the committer of a write are the one the Cartographer-Author
// header names (D950: a forge with a membership rule checks the committer too);
// the token that made the write and the host that recorded it are trailers.
func TestGitWrap_AuthorFromHeader(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	res := wrappedWriteCtx(t, k, authorHeaderCtx(botPrincipal, "Ada Example <ada@example.test>"), `{"reason":"why"}`)
	if got, want := headIdent(t, k), "Ada Example <ada@example.test> | Ada Example <ada@example.test>"; got != want {
		t.Errorf("author | committer = %q, want %q", got, want)
	}
	if got := headTokenTrailers(t, k); len(got) != 1 || got[0] != "reviewer-bot" {
		t.Errorf("token trailers = %q, want [reviewer-bot]", got)
	}
	if len(res.Content) != 1 {
		t.Errorf("a valid header adds no warning, got %+v", res.Content)
	}
}

// With no header and no registered writer the write is by the token id, at
// the server's host.
func TestGitWrap_AuthorFallsBackToTokenID(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	_, email := serverIdent()
	res := wrappedWriteCtx(t, k, authorHeaderCtx(botPrincipal, ""), `{}`)
	if got, want := headIdent(t, k), "reviewer-bot <"+email+"> | reviewer-bot <"+email+">"; got != want {
		t.Errorf("author | committer = %q, want %q", got, want)
	}
	if len(res.Content) != 1 {
		t.Errorf("an absent header adds no warning, got %+v", res.Content)
	}
}

// An invalid header is as if absent, plus one line saying so: it never turns
// into an author, and never fails the write.
func TestGitWrap_InvalidAuthorHeaderWarns(t *testing.T) {
	t.Parallel()
	for name, header := range map[string]string{
		"no email":      "Ada Example",
		"empty name":    "<ada@example.test>",
		"control char":  "Ada\x01 <ada@example.test>",
		"bad email":     "Ada <ada@@example.test>",
		"nested angles": "Ada <a<b>@example.test>",
		"too long":      strings.Repeat("a", 300) + " <ada@example.test>",
		"bad base64":    "=?base64?not base64!?=",
		"encoded break": client.EncodeAuthor("Ada <ada@example.test>\nCartographer-Token: forged"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			k, _ := setupGitKB(t)
			_, email := serverIdent()
			res := wrappedWriteCtx(t, k, authorHeaderCtx(botPrincipal, header), `{}`)
			if got := headIdent(t, k); !strings.HasPrefix(got, "reviewer-bot <"+email+"> | ") {
				t.Errorf("author = %q, want the token id fallback", got)
			}
			last := res.Content[len(res.Content)-1].Text
			if strings.Contains(last, "\n") || !strings.Contains(last, authorHeader+" header ignored") || !strings.Contains(last, "reviewer-bot") {
				t.Errorf("warning = %q, want one line naming the header and the fallback author", last)
			}
		})
	}
}

// Header text with a newline must not put a forged author or trailer in the
// commit: it is refused whole.
func TestGitWrap_HeaderNewlineIsRefused(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	wrappedWriteCtx(t, k, authorHeaderCtx(botPrincipal, "Ada <ada@example.test>\nCartographer-Token: forged"), `{}`)
	if got := headTokenTrailers(t, k); len(got) != 1 || got[0] != "reviewer-bot" {
		t.Errorf("token trailers = %q, want only the real one", got)
	}
}

// A caller cannot put its own Cartographer-Token in the commit: whatever it
// writes in the reason or in an id in the subject, the one trailer left is the
// token's id, and it is the last.
func TestGitWrap_ForgedTokenTrailerIsStripped(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	for _, args := range []string{
		`{"reason":"why\nCartographer-Token: forged"}`,
		`{"reason":"why\n\ncartographer-token: forged"}`,
		`{"id":"a\n\nCartographer-Token: forged"}`,
	} {
		wrappedWriteCtx(t, k, authorHeaderCtx(botPrincipal, ""), args)
		if got := headTokenTrailers(t, k); len(got) != 1 || got[0] != "reviewer-bot" {
			t.Errorf("args %s: token trailers = %q, want [reviewer-bot]", args, got)
		}
		out, err := exec.Command("git", "-C", k.Root, "log", "-1", "--format=%B").Output()
		if err != nil {
			t.Fatal(err)
		}
		if lines := strings.Split(strings.TrimSpace(string(out)), "\n"); lines[len(lines)-1] != "Cartographer-Token: reviewer-bot" {
			t.Errorf("args %s: last line = %q, want the token trailer", args, lines[len(lines)-1])
		}
	}
}

func TestCommitTrailerBlock_TokenLastAndExtraStripped(t *testing.T) {
	t.Parallel()
	ctx := auth.ContextWithPrincipal(context.Background(), auth.Principal{ID: "tok"})
	// Git reads a trailer key in any case and with blanks before the colon.
	got := commitTrailerBlock(ctx, json.RawMessage(`{"reason":"why"}`), "Moved-To: x", "Cartographer-Token: forged", "Cartographer-Token : forged", "cartographer-token\t: forged", "Cartographer-Host: forged", "cartographer-host : forged")
	if want := "Reason: why\nMoved-To: x\n" + commitHostKey + ": " + kb.ServerHost() + "\nCartographer-Token: tok"; got != want {
		t.Errorf("block = %q, want %q", got, want)
	}
	if got := commitTrailerBlock(context.Background(), nil); got != "" {
		t.Errorf("no principal: block = %q, want empty", got)
	}
}

// The header reaches a tool call through the SDK request.
func TestWithCallerAuthor_ReadsTheRequestHeader(t *testing.T) {
	t.Parallel()
	req := &sdk.CallToolRequest{Extra: &sdk.RequestExtra{Header: http.Header{authorHeader: {"Ada Example <ada@example.test>"}}}}
	ctx := withCallerAuthor(context.Background(), req)
	k, _ := setupGitKB(t)
	if name, email := writerAuthor(ctx, k); name != "Ada Example" || email != "ada@example.test" {
		t.Errorf("writerAuthor = %q <%q>", name, email)
	}
	if name, email := writerAuthor(withCallerAuthor(context.Background(), &sdk.CallToolRequest{}), k); name != "" || email != "" {
		t.Errorf("no request extra and no principal: author = %q <%q>, want the server identity", name, email)
	}
	twice := &sdk.CallToolRequest{Extra: &sdk.RequestExtra{Header: http.Header{authorHeader: {"A <a@example.test>", "B <b@example.test>"}}}}
	if name, _ := writerAuthor(withCallerAuthor(context.Background(), twice), k); name != "" {
		t.Errorf("two headers: author = %q, want none", name)
	}
}

// A name that is not plain ASCII arrives in MCP's encoded form (SEP-2243), and
// the commit carries it decoded.
func TestGitWrap_EncodedAuthorIsDecoded(t *testing.T) {
	t.Parallel()
	k, _ := setupGitKB(t)
	wrappedWriteCtx(t, k, authorHeaderCtx(botPrincipal, client.EncodeAuthor("Zoë Pérez <zoe@example.test>")), `{}`)
	if got, want := headIdent(t, k), "Zoë Pérez <zoe@example.test> | Zoë Pérez <zoe@example.test>"; got != want {
		t.Errorf("author | committer = %q, want %q", got, want)
	}
}

// installForgeHook puts the shared forge-rule hook (test/forge/pre-receive.sh,
// D996) on a bare remote with the allow-list of members: GitLab's member check.
// internal/kb carries a copy of this helper (two small copies are fine until
// the fixtures are consolidated).
func installForgeHook(t *testing.T, bare string, allowed ...string) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("..", "..", "test", "forge", "pre-receive.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(bare, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bare, "hooks", "pre-receive"), src, 0o755); err != nil {
		t.Fatal(err)
	}
	if len(allowed) > 0 {
		list := "# who the forge counts as a member\n" + strings.Join(allowed, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(bare, "hooks", "allowed-identities"), []byte(list), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// remoteIdents is "<author email> <committer email>" of every commit on the
// remote's branch, newest first.
func remoteIdents(t *testing.T, k *kb.KB, bare string) []string {
	t.Helper()
	branch, err := gitx.Branch(k.Root)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", bare, "log", "--format=%ae %ce", branch).Output()
	if err != nil {
		t.Fatalf("git log on the remote: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

// setupMemberRemote is a KB whose remote applies a membership rule (only
// ada@example.test) from the second push on: the seed history is published
// first, as it was before the forge turned the rule on.
func setupMemberRemote(t *testing.T) (k *kb.KB, bare string) {
	t.Helper()
	k, bare = setupGitKBWithRemote(t)
	if err := k.SyncOut(); err != nil {
		t.Fatalf("seed SyncOut: %v", err)
	}
	installForgeHook(t, bare, adaEmail)
	return k, bare
}

const (
	adaHeader = "Ada Example <ada@example.test>"
	adaEmail  = "ada@example.test"
)

// D950: the client's identity reaches a forge with a membership rule as the
// author and the committer of the write, and the push lands. TRAP: before
// 4d88a51 the committer was the server identity, which the rule rejected.
func TestGitWrap_HeaderWriteReachesAMembershipRule(t *testing.T) {
	t.Parallel()
	k, bare := setupMemberRemote(t)
	res := wrappedWriteCtx(t, k, authorHeaderCtx(botPrincipal, adaHeader), `{"reason":"why"}`)
	if len(res.Content) != 1 {
		t.Fatalf("a landed push adds no warning, got %+v", res.Content)
	}
	if got := remoteIdents(t, k, bare)[0]; got != adaEmail+" "+adaEmail {
		t.Errorf("remote head author/committer = %q, want both %s", got, adaEmail)
	}
}

// Without the header the write is by the token id at the server's host: the
// rule refuses it, the agent is told so, and the commit stays local.
func TestGitWrap_HeaderlessWriteIsRefusedByAMembershipRule(t *testing.T) {
	t.Parallel()
	k, bare := setupMemberRemote(t)
	before := remoteIdents(t, k, bare)
	res := wrappedWriteCtx(t, k, authorHeaderCtx(botPrincipal, ""), `{}`)
	warning := syncWarning(t, res)
	last, _ := warning["last_error"].(string)
	if warning["sync_state"] != "failed" || !strings.Contains(last, "is not a member of team") {
		t.Fatalf("warning = %+v, want a failed sync carrying the hook's message", warning)
	}
	if warning["committed"] != true {
		t.Errorf("warning = %+v, want committed: true (the commit stays local)", warning)
	}
	if after := remoteIdents(t, k, bare); len(after) != len(before) {
		t.Errorf("the remote moved: %q -> %q", before, after)
	}
}

// A KB whose operator configured an identity (git.identity) publishes a write
// that carries no header: the configured identity is the writer.
func TestGitWrap_ConfiguredIdentityPassesAMembershipRule(t *testing.T) {
	t.Parallel()
	k, bare := setupMemberRemote(t)
	k.SetDefaultIdentity("Ada Example", adaEmail)
	res := wrappedWriteCtx(t, k, authorHeaderCtx(botPrincipal, ""), `{}`)
	if len(res.Content) != 1 {
		t.Fatalf("a landed push adds no warning, got %+v", res.Content)
	}
	if got := remoteIdents(t, k, bare)[0]; got != adaEmail+" "+adaEmail {
		t.Errorf("remote head author/committer = %q, want both %s", got, adaEmail)
	}
}

// The stuck-commit case of 4d88a51: a write the rule refused stays a local
// commit by the server identity (a token id with no letter in it names no author), and once the
// operator names an identity for the KB the next write re-commits it as that
// member, so both reach the remote. A stuck commit authored by another
// non-member stays refused: only the commits no writer was known for are
// re-committed (kb realignCommitters).
func TestGitWrap_StuckCommitIsRecommittedByTheConfiguredIdentity(t *testing.T) {
	t.Parallel()
	k, bare := setupMemberRemote(t)
	before := len(remoteIdents(t, k, bare))
	res := wrappedWriteCtx(t, k, authorHeaderCtx(auth.Principal{ID: "-", Policy: auth.Policy{Admin: true}}, ""), `{"n":1}`)
	if warning := syncWarning(t, res); warning["sync_state"] != "failed" {
		t.Fatalf("the first write must be refused, got %+v", warning)
	}
	if n := len(remoteIdents(t, k, bare)); n != before {
		t.Fatalf("the first write reached the remote (%d -> %d commits)", before, n)
	}
	k.SetDefaultIdentity("Ada Example", adaEmail) // git.identity added, server restarted
	res = wrappedWriteCtx(t, k, authorHeaderCtx(botPrincipal, adaHeader), `{"n":2}`)
	if len(res.Content) != 1 {
		t.Fatalf("the second write must land, got %+v", res.Content)
	}
	idents := remoteIdents(t, k, bare)
	if len(idents) != before+2 {
		t.Fatalf("remote commits = %d, want %d (the stuck one and the new one)", len(idents), before+2)
	}
	for _, id := range idents[:2] {
		if id != adaEmail+" "+adaEmail {
			t.Errorf("published commit = %q, want both %s", id, adaEmail)
		}
	}
}
