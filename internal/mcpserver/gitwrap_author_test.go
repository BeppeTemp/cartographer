package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
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
