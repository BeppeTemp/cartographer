package plugin

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// PreviewInfo is what a preview needs besides the tree: where it came from and
// what the machine already has. Source must already be redacted by the caller.
type PreviewInfo struct {
	Source, Ref, SHA string
	// Have reports the version of a configured plugin, for `depends`.
	Have func(name string) (string, bool)
}

// esc makes a plugin-supplied string safe to print on a terminal: a rune that
// is not printable (a forged newline, an ESC, a bidi override) shows as its
// escape. cmd/cartographer's escapeControl does the same for approvals; this
// package cannot import cmd.
func esc(s string) string {
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsPrint(r) && r != '\t' {
			q := strconv.QuoteToASCII(string(r))
			b.WriteString(q[1 : len(q)-1])
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func escAll(ss []string) string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = esc(s)
	}
	return strings.Join(out, ", ")
}

// Preview prints what installing p would do, in plain words: every
// plugin-supplied string goes through esc, and each point prints its own
// contributions. The printed block is the record of what the user consented to.
func Preview(w io.Writer, p Plugin, in PreviewInfo, opts Options) {
	m := p.Manifest
	fmt.Fprintf(w, "plugin %s %s  (%s)\n", esc(m.Name), esc(m.Version), esc(m.DisplayName))
	fmt.Fprintf(w, "  %s\n", esc(m.Description))
	src := esc(in.Source)
	if in.Ref != "" {
		src += " @ " + esc(in.Ref)
	}
	if in.SHA != "" {
		src += " (" + esc(in.SHA)[:min(12, len(in.SHA))] + ")"
	}
	fmt.Fprintf(w, "  source: %s\n  digest: %s\n", src, p.Digest[:12])
	met := "unknown"
	if ok, err := RangeMet(m.Cartographer, opts.RunningVersion); err == nil {
		met = map[bool]string{true: "met", false: "NOT met"}[ok]
	}
	fmt.Fprintf(w, "  needs Cartographer %s: %s by this build\n", esc(m.Cartographer), met)
	for _, n := range slices.Sorted(maps.Keys(m.Depends)) {
		state := "not installed"
		if v, ok := in.Have(n); ok {
			state = "installed " + esc(v) + map[bool]string{true: ", met", false: ", NOT met"}[RangeMetOK(m.Depends[n], v)]
		}
		fmt.Fprintf(w, "  depends on %s %s: %s\n", esc(n), esc(m.Depends[n]), state)
	}

	fmt.Fprintln(w, "permissions:")
	pm, n := m.Permissions, 0
	say := func(what string, vals ...string) {
		if len(vals) > 0 {
			fmt.Fprintf(w, "  %s%s\n", what, escAll(vals))
			n++
		}
	}
	if pm != nil {
		if kb := map[string]string{"read": "may read KB content", "write": "may read and write KB content"}[pm.KB]; kb != "" {
			fmt.Fprintln(w, "  "+kb)
			n++
		}
		say("may reach hosts: ", pm.Net...)
		say("may read environment variables: ", pm.Env...)
		if pm.Files != nil {
			say("may read files: ", pm.Files.Read...)
			say("may write files: ", pm.Files.Write...)
		}
	}
	if n == 0 {
		fmt.Fprintln(w, "  none asked")
	}
	if len(m.Settings) > 0 {
		var keys []string
		for _, k := range slices.Sorted(maps.Keys(m.Settings)) {
			keys = append(keys, k+" ("+m.Settings[k].Type+")")
		}
		fmt.Fprintf(w, "settings: %s\n", escAll(keys))
	}

	var runs []string
	for _, pt := range RegisteredPoints() {
		cs := p.Contributions[pt.Dir]
		if len(cs) == 0 {
			continue
		}
		fmt.Fprintf(w, "%s/:\n", pt.Dir)
		for _, c := range cs {
			pt.Preview(w, c)
			for _, f := range c.Executable {
				runs = append(runs, fmt.Sprintf("%s %q: %q", c.Kind, c.Name, f))
			}
		}
	}
	if len(runs) > 0 {
		fmt.Fprintln(w, "runs code on this machine (files with the execute bit):")
		for _, r := range runs {
			fmt.Fprintf(w, "  %s\n", r)
		}
	}
}

// RangeMetOK is RangeMet with a malformed range counting as not met.
func RangeMetOK(rng, v string) bool {
	ok, err := RangeMet(rng, v)
	return err == nil && ok
}
