package contract

import (
	"strings"
)

// UnquoteHookToken strips the quotes resolveHookCommand puts around an
// executable: single quotes (with '\” for an apostrophe) on a POSIX host,
// double quotes for a space on Windows and in registrations written before.
func UnquoteHookToken(bin string) string {
	if len(bin) >= 2 && strings.HasPrefix(bin, `'`) && strings.HasSuffix(bin, `'`) {
		return strings.ReplaceAll(bin[1:len(bin)-1], `'\''`, `'`)
	}
	if len(bin) >= 2 && strings.HasPrefix(bin, `"`) && strings.HasSuffix(bin, `"`) {
		return bin[1 : len(bin)-1]
	}
	return bin
}
