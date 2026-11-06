package main

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/plugin/star"
)

// pluginProgram loads the scripts of a validated tree: (nil, nil) for a plugin
// without any. ReadTree already refused a tree whose scripts do not load, so an
// error here is a tree that bypassed it.
func pluginProgram(p plugin.Plugin) (*star.Program, error) { return star.Load(p.Files) }

// registrationLines are the "command/tool/event" lines of a program with their
// positions: what `plugin validate` and the approval preview print.
func registrationLines(name string, prog *star.Program) []string {
	var out []string
	for _, r := range prog.Sorted() {
		out = append(out, fmt.Sprintf("%s  (%s)", r.Line(name), r.Pos))
	}
	return out
}

// previewScripts prints the Scripts and Registers blocks of the approval
// preview: the files that will run, with their size and hash, and what they
// register. Everything a script supplies is escaped.
func previewScripts(w io.Writer, p plugin.Plugin) {
	prog, err := pluginProgram(p)
	if err != nil {
		fmt.Fprintf(w, "scripts: DO NOT LOAD: %s\n", escapeControl(err.Error()))
		return
	}
	if prog == nil {
		return
	}
	fmt.Fprintln(w, "scripts (they run on this machine, with the permissions above and nothing else):")
	for _, f := range prog.Files {
		fmt.Fprintf(w, "  %s  %d lines  sha256 %s\n", escapeControl(f.Path), f.Lines, f.SHA256)
	}
	fmt.Fprintln(w, "registers:")
	for _, l := range registrationLines(p.Manifest.Name, prog) {
		fmt.Fprintf(w, "  %s\n", escapeControl(l))
	}
}

// printScriptContent prints every script file in full, capped per file like
// the other files a consent covers: the source is what the user approves.
func printScriptContent(w io.Writer, p plugin.Plugin) {
	for _, f := range p.Files {
		if plugin.IsScriptPath(f.Path) {
			printScriptFile(w, f)
		}
	}
}

func printScriptFile(w io.Writer, f plugin.TreeFile) {
	lines := strings.Split(strings.TrimRight(string(f.Content), "\n"), "\n")
	fmt.Fprintf(w, "--- %s ---\n", escapeControl(f.Path))
	for i, l := range lines {
		if i == maxApprovalLines {
			fmt.Fprintf(w, "... %d more line(s) not shown; read the whole file before approving\n", len(lines)-i)
			break
		}
		fmt.Fprintln(w, escapeControl(l))
	}
}

// printScriptChanges prints, after the update diff, the script files that are
// added or changed in full, and the registrations the new version adds or drops.
func printScriptChanges(w io.Writer, old, next plugin.Plugin) {
	prev := map[string][]byte{}
	for _, f := range old.Files {
		prev[f.Path] = f.Content
	}
	for _, f := range next.Files {
		if was, ok := prev[f.Path]; plugin.IsScriptPath(f.Path) && (!ok || string(was) != string(f.Content)) {
			printScriptFile(w, f)
		}
	}
	oldProg, _ := pluginProgram(old)
	newProg, err := pluginProgram(next)
	if err != nil || (oldProg == nil && newProg == nil) {
		return
	}
	lines := func(prog *star.Program) []string {
		if prog == nil {
			return nil
		}
		return prog.Lines(next.Manifest.Name)
	}
	before, after := lines(oldProg), lines(newProg)
	for _, l := range after {
		if !slices.Contains(before, l) {
			fmt.Fprintf(w, "  registers, added: %s\n", escapeControl(l))
		}
	}
	for _, l := range before {
		if !slices.Contains(after, l) {
			fmt.Fprintf(w, "  registers, removed: %s\n", escapeControl(l))
		}
	}
}
