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
// when it holds only letters, digits, inner spaces and . - _ @ < > + ' ( ) ,
// and else base64 in the SEP-2243 form. That is stricter than SEP-2243, which
// leaves any visible ASCII plain: the value sync writes goes through a client
// config, and those expand "${VAR}", "$VAR", "$(cmd)" (Crush) or "{env:VAR}"
// (OpenCode) in a header, so only text no config can read as a template
// stays plain. The encoded form itself is never plain, so a value that
// already reads as it is encoded again.
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
	if s[0] == ' ' || s[len(s)-1] == ' ' {
		return true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		plain := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.IndexByte(" .-_@<>+'(),", c) >= 0
		if !plain {
			return true
		}
	}
	return false
}
