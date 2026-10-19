package client

import (
	"encoding/base64"
	"strings"
)

// The Cartographer-Author value travels in MCP's form for a header value that
// is not plain ASCII (SEP-2243): "=?base64?<the UTF-8, base64>?=". A name is
// often not ASCII, and a raw one breaks a Node client outright: it refuses a
// header value beyond Latin-1 and sends Latin-1 for the rest.
const (
	authorBase64Prefix = "=?base64?"
	authorBase64Suffix = "?="
)

// EncodeAuthor is author as the header value every sender writes (D904): as is
// when it is visible ASCII with no surrounding blank, else base64 in the
// SEP-2243 form. A value with "$" or "{" is encoded too: a client config
// expands "${VAR}" (and OpenCode "{env:VAR}") in a header it reads, so the
// literal written by sync would not be what reaches the server. So is a
// value that already reads as the encoded form.
func EncodeAuthor(author string) string {
	if author == "" || !authorNeedsBase64(author) {
		return author
	}
	return authorBase64Prefix + base64.StdEncoding.EncodeToString([]byte(author)) + authorBase64Suffix
}

// DecodeAuthor is the author a header value carries: the decoded text of the
// SEP-2243 form, else the value itself. ok is false when the form's base64 is
// invalid. The result is not yet a valid identity: the caller still parses it.
func DecodeAuthor(value string) (author string, ok bool) {
	inner, found := strings.CutPrefix(value, authorBase64Prefix)
	if !found {
		return value, true
	}
	inner, found = strings.CutSuffix(inner, authorBase64Suffix)
	if !found {
		return value, true
	}
	raw, err := base64.StdEncoding.DecodeString(inner)
	if err != nil {
		return "", false
	}
	return string(raw), true
}

func authorNeedsBase64(s string) bool {
	if s[0] == ' ' || s[0] == '\t' || s[len(s)-1] == ' ' || s[len(s)-1] == '\t' {
		return true
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c > 0x7E || c == '$' || c == '{' {
			return true
		}
	}
	return strings.HasPrefix(s, authorBase64Prefix) && strings.HasSuffix(s, authorBase64Suffix)
}
