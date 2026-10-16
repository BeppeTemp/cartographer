package mcpserver

import (
	"strings"
	"testing"
)

const (
	archivedPage   = "---\ntype: Note\nstatus: archived\n---\n"
	deprecatedPage = "---\ntype: Note\nstatus: deprecated\n---\n"
)

func relatedEntry(t *testing.T, out map[string]interface{}, id string) map[string]interface{} {
	t.Helper()
	for _, raw := range out["related"].([]interface{}) {
		if e := raw.(map[string]interface{}); e["id"] == id {
			return e
		}
	}
	t.Fatalf("%s is not in the block: %v", id, out["related"])
	return nil
}

// A retired page stays in the graph as a step, so the walk from a hit can pass
// through one. The entry it leads to is still offered; the retired page is
// never named, as an entry (D322) or as the concept it was reached through.
func TestSearchRelated_ViaNeverNamesARetiredPage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files map[string]string
		// want is the via of ops/target; "" means via is left out.
		want string
	}{
		{
			name: "only through an archived page: back to the hit",
			files: map[string]string{
				"ops/seed.md":   "Zeppelin hangar. [closed](closed.md).\n",
				"ops/closed.md": archivedPage + "[target](target.md).\n",
				"ops/target.md": "End of the road.\n",
			},
			want: "ops/seed",
		},
		{
			name: "through a deprecated page after a live one: the live step",
			files: map[string]string{
				"ops/seed.md":   "Zeppelin hangar. [mid](mid.md).\n",
				"ops/mid.md":    "[old](old.md).\n",
				"ops/old.md":    deprecatedPage + "[target](target.md).\n",
				"ops/target.md": "End of the road.\n",
			},
			want: "ops/mid",
		},
		{
			name: "an equally short live path wins over the retired one",
			files: map[string]string{
				"ops/seed.md":   "Zeppelin hangar. [closed](closed.md) [live](live.md).\n",
				"ops/closed.md": archivedPage + "[target](target.md).\n",
				"ops/live.md":   "[target](target.md).\n",
				"ops/target.md": "End of the road.\n",
			},
			want: "ops/live",
		},
		{
			name: "no live step on the way: no via",
			files: map[string]string{
				"ops/seed.md":   deprecatedPage + "Zeppelin hangar. [closed](closed.md).\n",
				"ops/closed.md": archivedPage + "[target](target.md).\n",
				"ops/target.md": "End of the road.\n",
			},
			want: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := graphToolKB(t, tc.files)
			out, text := relatedSearch(t, s, `{"query":"zeppelin"}`)
			e := relatedEntry(t, out, "ops/target")
			if got, _ := e["via"].(string); got != tc.want {
				t.Errorf("via = %q, want %q: %s", got, tc.want, text)
			}
			if ids := relatedIDs(out); contains(ids, "ops/closed") || contains(ids, "ops/old") {
				t.Errorf("a retired page is offered: %v", ids)
			}
			for _, retired := range []string{"ops/closed", "ops/old"} {
				if strings.Contains(text, `"via": "`+retired+`"`) {
					t.Errorf("an entry was reached through the retired %s: %s", retired, text)
				}
			}
			if tc.want == "" {
				if _, ok := e["via"]; ok {
					t.Errorf("via should be omitted: %v", e)
				}
			}
		})
	}
}
