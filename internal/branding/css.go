package branding

import (
	"fmt"
	"strings"
)

// CSS renders the stylesheet of the palette: two rules whose selectors match
// the bundle's own (web/src/styles/tokens.css), the property order fixed. The
// only inputs are validated #rrggbb values, so no operator text reaches it.
func (p Palette) CSS() []byte {
	var b strings.Builder
	b.WriteString("/* generated from the approved branding plugin: do not edit */\n")
	rule := func(sel string, t Theme, light bool) {
		fmt.Fprintf(&b, "%s {\n", sel)
		for _, kv := range [][2]string{
			{"primary", t.Primary}, {"primary-dim", t.PrimaryDim}, {"accent", t.Accent},
			{"text-on-primary", t.TextOnPrimary}, {"text-on-accent", t.TextOnAccent},
			{"graph-edge-active", t.EdgeActive},
		} {
			fmt.Fprintf(&b, "  --%s: %s;\n", kv[0], kv[1])
		}
		if light {
			fmt.Fprintf(&b, "  --graph-edge-3d-active: %s;\n  --graph-ring: %s;\n  --graph-glow-a: %s;\n", t.EdgeActive3D, t.Ring, t.Glow)
		}
		b.WriteString("}\n")
	}
	rule(`:root:not([data-theme="light"])`, p.Dark, false)
	rule(`:root[data-theme="light"]`, p.Light, true)
	return []byte(b.String())
}
