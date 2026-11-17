package gitx

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits of the remote's words once they are quoted in an error: the text
// reaches /health, MCP results, the Atlas and the CLI, so a remote cannot make
// it long or carry anything a terminal or a reader would act on.
const (
	// MaxRemoteLineRunes caps one quoted line.
	MaxRemoteLineRunes = 200
	// MaxRemoteTextBytes caps the whole quoted text.
	MaxRemoteTextBytes = 1024
)

// BoundRemoteText makes the output of a git remote safe to quote in an error:
// ANSI escape sequences are dropped, a carriage return ends a line, every other
// control character (C0, DEL, C1) is replaced by U+FFFD, every Unicode format
// character (category Cf: bidirectional controls, zero-width, tag characters) is
// dropped, each line is cut at MaxRemoteLineRunes and the whole at
// MaxRemoteTextBytes, a cut marked with an ellipsis. Line breaks are kept.
//
// Redaction comes first: callers pass text already through RedactRemoteText,
// because a credential cut by the caps could no longer be recognised.
func BoundRemoteText(text string) string {
	lines := strings.Split(sanitizeRemoteText(text), "\n")
	for i, l := range lines {
		lines[i] = capRunes(l, MaxRemoteLineRunes)
	}
	return capBytes(strings.Join(lines, "\n"), MaxRemoteTextBytes)
}

// BoundRemoteLines is the first n non-empty lines of a remote's output, trimmed
// and joined on one line with " | ", with BoundRemoteText's guarantees.
func BoundRemoteLines(text string, n int) string {
	var kept []string
	for _, l := range strings.Split(sanitizeRemoteText(text), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			kept = append(kept, capRunes(l, MaxRemoteLineRunes))
			if len(kept) == n {
				break
			}
		}
	}
	return capBytes(strings.Join(kept, " | "), MaxRemoteTextBytes)
}

func sanitizeRemoteText(s string) string {
	s = strings.ToValidUTF8(s, "�")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\x1b':
			i = skipEscape(rs, i)
		case r == '\x9b': // C1 CSI
			i = skipCSI(rs, i+1)
		case r == '\n' || r == '\r' || r == ' ' || r == ' ':
			b.WriteByte('\n')
		case r == '\t':
			b.WriteByte(' ')
		case unicode.IsControl(r):
			b.WriteRune('\uFFFD')
		case unicode.Is(unicode.Cf, r):
			// Format characters (bidi controls, zero-width, BOM, tags) are dropped.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// skipEscape returns the index of the last rune of the escape sequence that
// starts at rs[i] (an ESC).
func skipEscape(rs []rune, i int) int {
	if i+1 >= len(rs) {
		return i
	}
	switch rs[i+1] {
	case '[':
		return skipCSI(rs, i+2)
	case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC: up to BEL or ST
		for j := i + 2; j < len(rs); j++ {
			if rs[j] == '\a' || rs[j] == '\x9c' {
				return j
			}
			if rs[j] == '\x1b' && j+1 < len(rs) && rs[j+1] == '\\' {
				return j + 1
			}
		}
		return len(rs) - 1
	}
	// Two-character escape; nF sequences (ESC, intermediates, final) too.
	j := i + 1
	for j < len(rs) && rs[j] >= 0x20 && rs[j] <= 0x2f {
		j++
	}
	if j >= len(rs) {
		return len(rs) - 1
	}
	return j
}

// skipCSI returns the index of the final byte of the CSI whose parameters
// start at rs[j].
func skipCSI(rs []rune, j int) int {
	for ; j < len(rs); j++ {
		if rs[j] >= 0x40 && rs[j] <= 0x7e {
			return j
		}
		if rs[j] < 0x20 || rs[j] > 0x3f && rs[j] < 0x40 {
			return j - 1 // malformed: stop before the offender
		}
	}
	return len(rs) - 1
}

func capRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func capBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// sanitizeLine is one line of live output, redacted by the caller: sanitized
// and cut at MaxRemoteLineRunes. Its terminator, if any, is returned apart.
func sanitizeLine(line string) (body, term string) {
	body = strings.TrimRight(line, "\r\n")
	term = line[len(body):]
	body = strings.ReplaceAll(sanitizeRemoteText(body), "\n", " ")
	return capRunes(body, MaxRemoteLineRunes), term
}

// remoteOut marks an argument of remoteErrorf as a remote's output: the error's
// text carries it bounded, its RemoteText whole.
type remoteOut string

// remoteOutputError is an error whose text quotes a remote's output bounded,
// and keeps the unbounded (but redacted) text for classification.
type remoteOutputError struct {
	msg, full string
	err       error
}

func (e *remoteOutputError) Error() string      { return e.msg }
func (e *remoteOutputError) Unwrap() error      { return e.err }
func (e *remoteOutputError) RemoteText() string { return e.full }

// remoteErrorf is fmt.Errorf where each remoteOut argument is bounded in the
// message and kept whole in RemoteText.
func remoteErrorf(format string, args ...any) error {
	bounded := make([]any, len(args))
	whole := make([]any, len(args))
	for i, a := range args {
		if r, ok := a.(remoteOut); ok {
			bounded[i], whole[i] = BoundRemoteText(string(r)), string(r)
		} else {
			bounded[i], whole[i] = a, a
		}
	}
	msg := fmt.Errorf(format, bounded...)
	return &remoteOutputError{msg: msg.Error(), full: fmt.Errorf(format, whole...).Error(), err: errors.Unwrap(msg)}
}

// RemoteText is the text of err to classify a remote's refusal on: the whole
// redacted output when the error (or one it wraps) quotes a bounded one, else
// its message. Never display it: Error() is the bounded form.
func RemoteText(err error) string {
	if err == nil {
		return ""
	}
	var r interface{ RemoteText() string }
	if errors.As(err, &r) {
		return r.RemoteText()
	}
	return err.Error()
}
