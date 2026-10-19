package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The author of a write is whoever wrote it (D904). A client that knows who it
// acts for sends it in the Cartographer-Author request header as
// "Name <email>"; without a valid one the write is authored as the token that
// made it. The committer is always the server, and the token id is recorded in
// the Cartographer-Token trailer, which a client cannot forge.
const (
	authorHeader = "Cartographer-Author"

	commitTokenKey = "Cartographer-Token"

	// maxAuthorHeaderBytes bounds the header: a name and an email, not a document.
	maxAuthorHeaderBytes = 256
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
	name, email, problem := parseAuthorHeader(vals[0])
	return context.WithValue(ctx, callerAuthorKey{}, callerAuthor{name: name, email: email, problem: problem})
}

// parseAuthorHeader validates "Name <email>". TRAP: the value is client text
// going into a commit's author line; control characters (newlines first) and
// the angle brackets inside either part are refused, not cleaned.
func parseAuthorHeader(raw string) (name, email, problem string) {
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		return "", "", "empty"
	case len(s) > maxAuthorHeaderBytes:
		return "", "", "longer than 256 bytes"
	case !utf8.ValidString(s):
		return "", "", "not valid UTF-8"
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r) {
			return "", "", "contains a control character"
		}
	}
	open := strings.LastIndex(s, "<")
	if open < 0 || !strings.HasSuffix(s, ">") {
		return "", "", "not in the form Name <email>"
	}
	name = strings.TrimSpace(s[:open])
	email = s[open+1 : len(s)-1]
	if !hasLetterOrDigit(name) || strings.ContainsAny(name, "<>") {
		return "", "", "the name is missing or holds angle brackets"
	}
	at := strings.Index(email, "@")
	if at <= 0 || at == len(email)-1 || strings.Count(email, "@") != 1 ||
		strings.ContainsAny(email, "<> \t\"") {
		return "", "", "the email is not a plain local@domain"
	}
	return name, email, ""
}

func hasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

// writerAuthor is the author of a write made under ctx: the header's identity
// when valid, else the calling token's id at the server's email, else (no
// authenticated principal, as on stdio) "" so the commit takes the server
// identity.
func writerAuthor(ctx context.Context) (name, email string) {
	if a, _ := ctx.Value(callerAuthorKey{}).(callerAuthor); a.problem == "" && a.name != "" {
		return a.name, a.email
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
// trailer: the real one is appended last by commitTrailerBlock, and a copy
// supplied through a subject or any caller text must not be read in its place.
func stripTokenTrailers(s string) string {
	if !strings.Contains(strings.ToLower(s), strings.ToLower(commitTokenKey)) {
		return s
	}
	lines := strings.Split(s, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if isTokenTrailerLine(l) {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}

// isTokenTrailerLine reports whether git would read l as a Cartographer-Token
// trailer: the key in any case, then optional blanks, then the colon.
func isTokenTrailerLine(l string) bool {
	rest, ok := strings.CutPrefix(strings.ToLower(strings.TrimSpace(l)), strings.ToLower(commitTokenKey))
	return ok && strings.HasPrefix(strings.TrimLeft(rest, " \t"), ":")
}

// appendAuthorWarning tells the caller, in one line, that its
// Cartographer-Author header was ignored and whom the write was authored as.
func appendAuthorWarning(ctx context.Context, res *ToolResult) {
	a, _ := ctx.Value(callerAuthorKey{}).(callerAuthor)
	if a.problem == "" {
		return
	}
	as, _ := writerAuthor(ctx)
	if as == "" {
		as, _ = kb.ServerIdentity()
	}
	b, _ := json.Marshal(map[string]string{
		"warning": fmt.Sprintf("%s header ignored (%s); this write is authored as %s", authorHeader, a.problem, as),
	})
	res.Content = append(res.Content, ContentBlock{Type: "text", Text: string(b)})
}
