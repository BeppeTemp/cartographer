package main

import (
	"os"

	xterm "github.com/charmbracelet/x/term"
)

// isInteractive reports whether both stdin and stdout are terminals. Bare
// `cartographer` prints its short status only then; connect and approve use
// it to decide whether they may prompt. Anything else (pipes, redirection,
// CI) gets the plain, non-interactive behaviour.
func isInteractive() bool {
	return xterm.IsTerminal(os.Stdout.Fd()) && xterm.IsTerminal(os.Stdin.Fd())
}
