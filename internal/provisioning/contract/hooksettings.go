package contract

import (
	"strings"
)

// HookSpec is the hook.json format (KB, hooks/<name>/hook.json): the Claude
// Code event (e.g. "PostToolUse", "SessionStart"), an optional matcher and the
// command to run — relative to the hook's directory unless absolute (see
// resolveHookCommand).
type HookSpec struct {
	Event   string `json:"event"`
	Matcher string `json:"matcher,omitempty"`
	Command string `json:"command"`
}

// SplitHookCommand splits a hook command line into its leading executable token
// and the rest, verbatim. The token ends at the first space, unless it opens with
// a double quote, in which case it ends at the closing quote — that is the case
// resolveHookCommand's own previous output falls into.
func SplitHookCommand(command string) (bin, rest string, hasRest bool) {
	if strings.HasPrefix(command, `'`) {
		// A single-quoted token ends at the first quote that is not the middle
		// of an escaped apostrophe ('\'').
		for i := 1; i < len(command); i++ {
			if command[i] != '\'' {
				continue
			}
			if strings.HasPrefix(command[i:], `'\''`) {
				i += 3
				continue
			}
			bin, rest = command[:i+1], strings.TrimPrefix(command[i+1:], " ")
			return bin, rest, rest != ""
		}
	}
	if strings.HasPrefix(command, `"`) {
		if end := strings.Index(command[1:], `"`); end >= 0 {
			bin, rest = command[:end+2], strings.TrimPrefix(command[end+2:], " ")
			return bin, rest, rest != ""
		}
	}
	bin, rest, hasRest = strings.Cut(command, " ")
	return bin, rest, hasRest
}
