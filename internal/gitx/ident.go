package gitx

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxIdentBytes bounds an identity: a name and an email, not a document.
const MaxIdentBytes = 256

// ParseIdent validates the "Name <email>" identity a client sends as the
// author of a write (D904); problem says why a value is refused. The server
// applies it to the Cartographer-Author header and the client to what it is
// about to send, so a value one refuses the other never sends. TRAP: the value
// ends up in a commit's author line; control characters (newlines first) and
// angle brackets inside either part are refused, not cleaned.
func ParseIdent(raw string) (name, email, problem string) {
	s := strings.TrimSpace(raw)
	switch {
	case s == "":
		return "", "", "empty"
	case len(s) > MaxIdentBytes:
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
