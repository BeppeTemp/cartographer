package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/plugin/star"
)

// pluginProgram loads the scripts of a validated tree: (nil, nil) for a plugin
// without any. ReadTree already refused a tree whose scripts do not load, so an
// error here is a tree that bypassed it.
func pluginProgram(p plugin.Plugin) (*star.Program, error) { return star.Load(p.Files) }

// pluginToolNames are the names of the tools a plugin registers, sorted: the
// `tools` hint recorded at approval. A tree that does not load yields none.
func pluginToolNames(p plugin.Plugin) []string {
	prog, err := pluginProgram(p)
	if err != nil || prog == nil {
		return nil
	}
	var out []string
	for _, r := range prog.Sorted() {
		if r.Kind == star.KindTool {
			out = append(out, p.Manifest.Name+"_"+r.Name)
		}
	}
	return out
}

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

// printScriptContent prints every script file in full, never truncated: the
// approved digest covers the whole file, and plugin.MaxBytes bounds its size.
func printScriptContent(w io.Writer, p plugin.Plugin) {
	for _, f := range p.Files {
		if plugin.IsScriptPath(f.Path) {
			printScriptFile(w, f)
		}
	}
}

// printScriptFile prints one script file in full.
func printScriptFile(w io.Writer, f plugin.TreeFile) {
	lines := strings.Split(strings.TrimRight(string(f.Content), "\n"), "\n")
	fmt.Fprintf(w, "--- %s ---\n", escapeControl(f.Path))
	for _, l := range lines {
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
	// A registration is compared whole: its line (kind, name, writes) and
	// everything else the user approved (help, usage, params schema).
	index := func(prog *star.Program) (keys []string, detail map[string]string, line map[string]string) {
		detail, line = map[string]string{}, map[string]string{}
		if prog == nil {
			return
		}
		for _, r := range prog.Sorted() {
			k := r.Kind + " " + r.Name
			keys = append(keys, k)
			line[k] = r.Line(next.Manifest.Name)
			detail[k] = fmt.Sprintf("%s\x00%q\x00%q\x00%s", line[k], r.Help, r.Usage, r.Params)
		}
		return
	}
	beforeKeys, beforeD, beforeL := index(oldProg)
	afterKeys, afterD, afterL := index(newProg)
	for _, k := range afterKeys {
		switch d, ok := beforeD[k]; {
		case !ok:
			fmt.Fprintf(w, "  registers, added: %s\n", escapeControl(afterL[k]))
		case d != afterD[k]:
			fmt.Fprintf(w, "  registers, changed: %s\n", escapeControl(afterL[k]))
		}
	}
	for _, k := range beforeKeys {
		if _, ok := afterD[k]; !ok {
			fmt.Fprintf(w, "  registers, removed: %s\n", escapeControl(beforeL[k]))
		}
	}
}
