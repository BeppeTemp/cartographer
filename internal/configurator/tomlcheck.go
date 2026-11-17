package configurator

import (
	"fmt"
	"strings"
)

// CheckTOMLSyntax reports the first line of content that is not TOML: a table
// header that does not close, a line that is neither a header, a `key = value`
// pair nor a comment, a string or an array that never ends. It is the check
// the JSON providers get from their parser (existing file is not valid JSON:
// fix or delete it), for config.toml, which is edited as text and never parsed
// (D58) and which Cartographer therefore would otherwise append a block to
// without noticing that Codex cannot read the file (#2195).
//
// It is deliberately lenient: it is a line-level syntax check, not a TOML
// decoder (no type, duplicate-key or date validation), so it refuses only what
// no TOML parser accepts. A false refusal blocks the connect of a working
// config, which is worse than letting a subtle error through.
func CheckTOMLSyntax(content string) error {
	lines := strings.Split(content, "\n")
	// depth is the open [ and { of a value spread over several lines; multi is
	// the delimiter of a multi-line string still open.
	depth, multi := 0, ""
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		n := i + 1
		if multi != "" {
			rest, ok := skipMultiline(line, multi)
			if !ok {
				continue
			}
			multi = ""
			var err error
			if depth, multi, err = scanValue(rest, depth); err != nil {
				return fmt.Errorf("line %d: %w", n, err)
			}
			continue
		}
		if depth > 0 {
			var err error
			if depth, multi, err = scanValue(line, depth); err != nil {
				return fmt.Errorf("line %d: %w", n, err)
			}
			continue
		}
		text := strings.TrimSpace(line)
		if text == "" || text[0] == '#' {
			continue
		}
		if text[0] == '[' {
			if err := checkTOMLHeader(text); err != nil {
				return fmt.Errorf("line %d: %w", n, err)
			}
			continue
		}
		eq := keyEnd(text)
		if eq < 0 {
			return fmt.Errorf("line %d: expected `key = value`, a [table] header or a comment", n)
		}
		if k := strings.TrimSpace(text[:eq]); k == "" || !validKey(k) {
			return fmt.Errorf("line %d: invalid key %q", n, strings.TrimSpace(text[:eq]))
		}
		value := strings.TrimSpace(text[eq+1:])
		if value == "" || value[0] == '#' {
			return fmt.Errorf("line %d: the key has no value", n)
		}
		var err error
		if depth, multi, err = scanValue(value, 0); err != nil {
			return fmt.Errorf("line %d: %w", n, err)
		}
	}
	switch {
	case multi != "":
		return fmt.Errorf("a multi-line string is never closed")
	case depth > 0:
		return fmt.Errorf("an array or inline table is never closed")
	}
	return nil
}

func validKey(k string) bool { _, ok := splitTOMLKey(k); return ok }

// keyEnd is the index of the "=" that ends the key of text, outside quotes, or -1.
func keyEnd(text string) int {
	for i := 0; i < len(text); i++ {
		switch c := text[i]; c {
		case '=':
			return i
		case '"', '\'':
			j := closingQuote(text, i)
			if j < 0 {
				return -1
			}
			i = j
		case '#':
			return -1
		}
	}
	return -1
}

// closingQuote is the index of the quote closing the single-line string that
// opens at s[i], or -1.
func closingQuote(s string, i int) int {
	q := s[i]
	for j := i + 1; j < len(s); j++ {
		if s[j] == '\\' && q == '"' {
			j++
			continue
		}
		if s[j] == q {
			return j
		}
	}
	return -1
}

func checkTOMLHeader(text string) error {
	open, closer := "[", "]"
	if strings.HasPrefix(text, "[[") {
		open, closer = "[[", "]]"
	}
	body := text[len(open):]
	// The closer is the first "]" outside quotes.
	end := -1
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '"', '\'':
			j := closingQuote(body, i)
			if j < 0 {
				return fmt.Errorf("unterminated string in the table header")
			}
			i = j
		case ']':
			end = i
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return fmt.Errorf("the table header %q is not closed", text)
	}
	if _, ok := splitTOMLKey(body[:end]); !ok {
		return fmt.Errorf("invalid table name %q", strings.TrimSpace(body[:end]))
	}
	rest := strings.TrimSpace(body[end:])
	if !strings.HasPrefix(rest, closer) {
		return fmt.Errorf("the table header %q is not closed", text)
	}
	rest = strings.TrimSpace(rest[len(closer):])
	if rest != "" && rest[0] != '#' {
		return fmt.Errorf("unexpected text after the table header")
	}
	return nil
}

// skipMultiline returns what follows the delimiter that closes a multi-line
// string on line, and whether the line closes it.
func skipMultiline(line, delim string) (string, bool) {
	from := 0
	for {
		i := strings.Index(line[from:], delim)
		if i < 0 {
			return "", false
		}
		i += from
		if delim == `"""` {
			// An odd run of backslashes before it escapes the first quote.
			b := 0
			for j := i - 1; j >= 0 && line[j] == '\\'; j-- {
				b++
			}
			if b%2 == 1 {
				from = i + 1
				continue
			}
		}
		end := i + 3
		for end < len(line) && line[end] == delim[0] { // up to two more quotes belong to the string
			end++
		}
		return line[end:], true
	}
}

// scanValue walks the text of a value from the given bracket depth. It returns
// the depth and the multi-line string delimiter left open at its end, or an
// error when a single-line string does not end or text follows a value that
// is already complete (a string or a bracketed value; a bare token such as a
// date may hold a space).
func scanValue(s string, depth int) (int, string, error) {
	finished := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '#' {
			return depth, "", nil
		}
		if c == ' ' || c == '\t' {
			continue
		}
		if finished {
			return 0, "", fmt.Errorf("unexpected text after the value")
		}
		switch {
		case strings.HasPrefix(s[i:], `"""`) || strings.HasPrefix(s[i:], `'''`):
			delim := s[i : i+3]
			rest, ok := skipMultiline(s[i+3:], delim)
			if !ok {
				return depth, delim, nil
			}
			return scanValue(rest, depth)
		case c == '"' || c == '\'':
			j := closingQuote(s, i)
			if j < 0 {
				return 0, "", fmt.Errorf("a string is never closed on its line")
			}
			i = j
			finished = depth == 0
		case c == '[' || c == '{':
			depth++
		case c == ']' || c == '}':
			if depth == 0 {
				return 0, "", fmt.Errorf("unbalanced %q", string(c))
			}
			depth--
			finished = depth == 0
		}
	}
	return depth, "", nil
}
