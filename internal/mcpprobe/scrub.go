package mcpprobe

import "strings"

// MinScrubLen is the shortest value that is replaced inside a text. A shorter
// one ("1", "ab") would match unrelated characters and leave the text
// unreadable or, worse, half-scrubbed, so a text that contains one is masked
// whole instead.
const MinScrubLen = 4

const redacted = "<redacted>"

// NewScrub returns a function that removes every value from a text: values of
// MinScrubLen or more are replaced where they occur (longest first, so a value
// that contains another is not left in pieces); a shorter one makes the whole
// text "<redacted>". Empty values are ignored.
func NewScrub(values []string) func(string) string {
	var long, short []string
	seen := map[string]bool{}
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		if len(v) < MinScrubLen {
			short = append(short, v)
		} else {
			long = append(long, v)
		}
	}
	// Longest first.
	for i := 1; i < len(long); i++ {
		for j := i; j > 0 && len(long[j]) > len(long[j-1]); j-- {
			long[j], long[j-1] = long[j-1], long[j]
		}
	}
	return func(s string) string {
		for _, v := range long {
			s = strings.ReplaceAll(s, v, redacted)
		}
		for _, v := range short {
			if strings.Contains(s, v) {
				return redacted
			}
		}
		return s
	}
}
