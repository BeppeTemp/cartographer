package mcpserver

import (
	"regexp"
	"testing"
)

var leakHash = regexp.MustCompile(`[0-9a-f]{40,64}`)

// AC-cross-kb-links-20
func TestOtherReadersIgnoreCrossLinks(t *testing.T) {
	t.Parallel()
	calls := map[string]string{
		"search":        `{"query":"alpha"}`,
		"graph_context": `{"ids":["p"]}`,
		"graph_path":    `{"from":"p","to":"q"}`,
		"concept_list":  `{}`,
		"kb_review":     `{}`,
		"index_get":     `{}`,
		"work_list":     `{}`,
		"concept_read":  `{"id":"q"}`,
	}
	run := func(withLink bool) map[string]string {
		s, r := edgesRig(t)
		// A holds the same bytes in both worlds; what differs is kb-b: whether the
		// linked page exists and whether a page there links back to kb-a.
		if withLink {
			putThroughServer(t, r.b, "ops/back", "[[kb:kb-a/q]]\n")
		} else {
			putThroughServer(t, r.b, "ops/back", "no link\n")
			putThroughServer(t, r.b, "ops/x", "")
		}
		out := map[string]string{}
		for name, args := range calls {
			for who, ctx := range crossCallers() {
				text, _ := callJSON(t, s, ctx, name, args)
				out[name+"/"+who] = leakHash.ReplaceAllString(normaliseGolden(text, r.a.DataRoot()), "<hash>")
			}
		}
		return out
	}
	with, without := run(true), run(false)
	for k, v := range without {
		if with[k] != v {
			t.Errorf("%s differs with a cross link:\n%s\n%s", k, with[k], v)
		}
	}
}
