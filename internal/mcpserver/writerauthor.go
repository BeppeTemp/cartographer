package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The writer of a write is its author and its committer (D904, D950). A
// client that knows who it acts for sends it in the Cartographer-Author request
// header as "Name <email>" (base64 in the SEP-2243 form when it is not plain
// ASCII, client.EncodeAuthor); without a valid one the write is by the identity
// configured for the KB in the server file, else by the token that made it. The
// token id is recorded in the Cartographer-Token trailer, which a client cannot
// forge, and the instance in the Cartographer-Host trailer.
const (
	authorHeader = client.AuthorHeader

	commitTokenKey = "Cartographer-Token"
	// commitHostKey names the instance that recorded a write (D950): the
	// committer is the writer, so the host lives in the trailer.
	commitHostKey = "Cartographer-Host"
)

type callerAuthorKey struct{}

// callerAuthor is what the request said about its author: a valid identity, or
// the reason the header was ignored.
type callerAuthor struct {
	name, email string
	problem     string
}

// withCallerAuthor records on ctx the Cartographer-Author header of the request
// (the HTTP transport; stdio has none), validated.
func withCallerAuthor(ctx context.Context, req any) context.Context {
	type headered interface {
		GetExtra() *sdk.RequestExtra
	}
	r, ok := req.(headered)
	if !ok {
		return ctx
	}
	extra := r.GetExtra()
	if extra == nil {
		return ctx
	}
	return withAuthorHeader(ctx, extra.Header)
}

func withAuthorHeader(ctx context.Context, h http.Header) context.Context {
	vals := h.Values(authorHeader)
	if len(vals) == 0 {
		return ctx
	}
	if len(vals) > 1 {
		return context.WithValue(ctx, callerAuthorKey{}, callerAuthor{problem: "sent more than once"})
	}
	value, ok := client.DecodeAuthor(vals[0])
	if !ok {
		return context.WithValue(ctx, callerAuthorKey{}, callerAuthor{problem: "not valid base64 in its encoded form"})
	}
	name, email, problem := gitx.ParseIdent(value)
	return context.WithValue(ctx, callerAuthorKey{}, callerAuthor{name: name, email: email, problem: problem})
}

func hasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// writerAuthor is the writer of a write made under ctx on k, its author and
// committer (D950): the header's identity when valid; else the identity the
// operator configured for k (git.identity, git.identities), which is what makes
// a client that cannot send the header publishable; else the calling token's id
// at the server's email; else (a context with no principal id) "" so the commit
// takes the server identity. The Cartographer-Token trailer names the token
// whichever way the author was decided.
func writerAuthor(ctx context.Context, k *kb.KB) (name, email string) {
	if a, _ := ctx.Value(callerAuthorKey{}).(callerAuthor); a.problem == "" && a.name != "" {
		return a.name, a.email
	}
	if n, e, ok := k.ConfiguredIdentity(); ok {
		return n, e
	}
	id := tokenName(auth.PrincipalFromContext(ctx).ID)
	if id == "" {
		return "", ""
	}
	_, email = kb.ServerIdentity()
	return id, email
}

// tokenName makes a token id fit the author line: whitespace collapsed, angle
// brackets dropped, bounded; "" when nothing usable is left.
func tokenName(id string) string {
	id = strings.NewReplacer("<", "", ">", "").Replace(id)
	id = truncateUTF8(strings.Join(strings.Fields(id), " "), maxClientFieldLen)
	if !hasLetterOrDigit(id) {
		return ""
	}
	return id
}

// commitToken is the value of the Cartographer-Token trailer: the id of the
// token that made the write, on one line.
func commitToken(ctx context.Context) string {
	return truncateUTF8(strings.Join(strings.Fields(auth.PrincipalFromContext(ctx).ID), " "), maxClientFieldLen)
}

// stripTokenTrailers drops every line that would read as a Cartographer-Token
// or Cartographer-Host trailer: the real ones are appended last by
// commitTrailerBlock, and a copy supplied through a subject or any caller text
// must not be read in their place.
func stripTokenTrailers(s string) string {
	lower := strings.ToLower(s)
	if !strings.Contains(lower, strings.ToLower(commitTokenKey)) && !strings.Contains(lower, strings.ToLower(commitHostKey)) {
		return s
	}
	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if isTrailerLine(l, commitTokenKey) || isTrailerLine(l, commitHostKey) {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}

// isTokenTrailerLine reports whether git would read l as a Cartographer-Token
// trailer: the key in any case, then optional blanks, then the colon.
func isTokenTrailerLine(l string) bool { return isTrailerLine(l, commitTokenKey) }

// isTrailerLine reports whether git would read l as a key trailer.
func isTrailerLine(l, key string) bool {
	rest, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(l)), strings.ToLower(key))
	return ok && strings.HasPrefix(strings.TrimLeft(rest, " \t"), ":")
}

// appendAuthorWarning tells the caller, in one line, that its
// Cartographer-Author header was ignored and whom the write was authored as.
func appendAuthorWarning(ctx context.Context, k *kb.KB, res *ToolResult) {
	a, _ := ctx.Value(callerAuthorKey{}).(callerAuthor)
	if a.problem == "" {
		return
	}
	as, _ := writerAuthor(ctx, k)
	if as == "" {
		as, _ = k.DefaultIdentity()
	}
	b, _ := json.Marshal(map[string]string{
		"warning": fmt.Sprintf("%s header ignored (%s); this write is authored as %s", authorHeader, a.problem, as),
	})
	res.Content = append(res.Content, ContentBlock{Type: "text", Text: string(b)})
}
