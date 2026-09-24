package provisioning

// hookshell.go (D267) — hook command lines that a POSIX shell runs, on Windows
// too. Claude Code on Windows runs every hook command through Git Bash, and an
// OpenCode plugin runs it through `sh -c` (generateOpenCodePlugin): to either
// shell a backslash is an escape, so `C:\Users\user\.claude\hooks\x\run.cmd`
// arrives as `C:Usersuser.claudehooksxrun.cmd` and the hook never runs, with
// nothing on Cartographer's side to say so (#412). Windows accepts forward
// slashes in a path, so the resolved executable is written that way for the
// providers whose hooks go through a POSIX shell. Codex and Antigravity keep
// the host's spelling: neither documents running hooks through one.

import (
	"fmt"
	"os"
	"strings"
)

// hookHostWindows is hostWindows behind a test seam, the way execBitSupported
// indirects execbit.Supported: the Windows spelling of a hook command, and
// recognising the one written before D267, must be pinned from a unix host.
var hookHostWindows = hostWindows

// posixShellHookCommand rewrites the leading token of a resolved hook command
// (resolveHookCommand's output) for a POSIX shell running on Windows: every
// backslash in the executable becomes a forward slash, and the token stays
// quoted when it contains a space. Arguments are left verbatim — they are the
// hook author's, and a backslash there may be a deliberate shell escape. A
// no-op when windows is false, and idempotent.
//
// A backslash in the executable of a command bound for Git Bash on Windows is
// never an escape anyone meant: it is a path separator the shell would delete.
func posixShellHookCommand(command string, windows bool) string {
	if !windows {
		return command
	}
	bin, rest, hasRest := splitHookCommand(command)
	bin = unquoteHookToken(bin)
	if !strings.Contains(bin, `\`) {
		return command
	}
	bin = strings.ReplaceAll(bin, `\`, "/")
	if strings.ContainsAny(bin, " \t") {
		bin = `"` + bin + `"`
	}
	if hasRest {
		return bin + " " + rest
	}
	return bin
}

// unquoteHookToken strips the double quotes resolveHookCommand puts around an
// executable containing a space.
func unquoteHookToken(bin string) string {
	if len(bin) >= 2 && strings.HasPrefix(bin, `"`) && strings.HasSuffix(bin, `"`) {
		return bin[1 : len(bin)-1]
	}
	return bin
}

// hookCommandProblem says why a registered hook command cannot run, or "" when
// nothing visible stops it. Two things are checked, both on the executable
// alone: a backslash in it on Windows (a POSIX shell deletes it — the pre-D267
// registration), and an absolute path to a file that does not exist. A bare
// name is PATH's business and a $VAR path is the shell's: neither is guessed at.
// exists is injected so the check stays pure.
func hookCommandProblem(command string, windows bool, exists func(string) bool) string {
	bin, _, _ := splitHookCommand(strings.TrimSpace(command))
	bin = unquoteHookToken(bin)
	if windows && strings.Contains(bin, `\`) {
		return fmt.Sprintf("command %s has backslashes, which the shell running it removes", bin)
	}
	if isAbsCommandPath(bin) && !exists(bin) {
		return fmt.Sprintf("command %s does not exist", bin)
	}
	return ""
}

// claudeHookCommandProblem checks every Claude Code settings.json command owned
// by hookName (D57) with hookCommandProblem and returns the first problem
// found, "" when there is none — including when nothing is registered, which is
// not this check's finding to make.
func claudeHookCommandProblem(baseDir, hookName string) (string, error) {
	settings, err := loadJSONObject(claudeSettingsPath(baseDir)) // a missing file is {}
	if err != nil {
		return "", err
	}
	for _, command := range ownedHookCommands(settings, hookOwnershipMarker(hookName)) {
		if problem := hookCommandProblem(command, hookHostWindows, fileExists); problem != "" {
			return problem, nil
		}
	}
	return "", nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
