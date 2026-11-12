package plugin

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/BeppeTemp/cartographer/internal/plugin/core"
)

// PreviewInfo is what a preview needs besides the tree: where it came from and
// what the machine already has. Source must already be redacted by the caller.
type PreviewInfo struct {
	Source, Ref, SHA string
	// Side is the component the preview is for: only its sdk levels,
	// permissions and contributions are shown, and the other sides are named
	// as not loaded. Empty shows every side (plugin validate).
	Side core.Side
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
func Preview(w io.Writer, p core.Plugin, in PreviewInfo, opts core.Options) {
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
	if ok, err := core.RangeMet(m.Cartographer, opts.RunningVersion); err == nil {
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

	if isSchema2(p) {
		previewSides(w, p, in)
	} else {
		previewPermissions(w, "permissions:", m.Permissions)
	}
	if len(m.Settings) > 0 {
		var keys []string
		for _, k := range slices.Sorted(maps.Keys(m.Settings)) {
			keys = append(keys, k+" ("+m.Settings[k].Type+")")
		}
		fmt.Fprintf(w, "settings: %s\n", escAll(keys))
	}

	var runs []string
	for _, pt := range core.RegisteredPoints() {
		cs := p.Contributions[pt.Dir]
		if len(cs) == 0 || (in.Side != "" && pt.Side != in.Side) {
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

// isSchema2 reports a plugin read from a schema 2 manifest.
func isSchema2(p core.Plugin) bool {
	return p.Manifest.Schema != nil && *p.Manifest.Schema == core.SchemaVersion
}

// previewPermissions prints one permission block under head.
func previewPermissions(w io.Writer, head string, pm *core.Permissions) {
	fmt.Fprintln(w, head)
	n := 0
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
}

// previewSides prints, for the side asked for (every declared side when none),
// its sdk levels and permissions, then what the other sides and the tree hold
// that this build does not load.
func previewSides(w io.Writer, p core.Plugin, in PreviewInfo) {
	shown := p.Sides
	if in.Side != "" {
		shown = nil
		if slices.Contains(p.Sides, in.Side) {
			shown = []core.Side{in.Side}
		}
	}
	for _, side := range shown {
		c := p.Manifest.Components[side]
		var levels []string
		for _, fam := range slices.Sorted(maps.Keys(c.SDK)) {
			levels = append(levels, fmt.Sprintf("%s %d", fam, c.SDK[fam]))
		}
		head := "permissions:"
		if in.Side == "" {
			fmt.Fprintf(w, "%s side:\n", side)
			head = "  permissions:"
		}
		if len(levels) > 0 {
			fmt.Fprintf(w, "sdk: %s\n", escAll(levels))
		}
		if why := p.Unsupported[side]; why != "" {
			fmt.Fprintf(w, "not loadable by this build: %s\n", esc(why))
		}
		previewPermissions(w, head, c.Permissions)
		if side == core.SideSurveyor && c.Permissions != nil && len(c.Permissions.Env) > 0 {
			fmt.Fprintf(w, "your Surveyor config must map under plugins.%s.env: %s\n", esc(p.Manifest.Name), escAll(c.Permissions.Env))
		}
	}
	if in.Side != "" {
		var others []string
		for _, side := range p.Sides {
			if side != in.Side {
				others = append(others, string(side))
				if why := p.Unsupported[side]; why != "" {
					others[len(others)-1] += " (" + why + ")"
				}
			}
		}
		if len(others) > 0 {
			fmt.Fprintf(w, "other sides, not loaded here: %s\n", escAll(others))
		}
	}
	if len(p.Unknown) > 0 {
		fmt.Fprintf(w, "not known to this build, stored but never loaded: %s\n", escAll(p.Unknown))
	}
}

// RangeMetOK is RangeMet with a malformed range counting as not met.
func RangeMetOK(rng, v string) bool {
	ok, err := core.RangeMet(rng, v)
	return err == nil && ok
}
