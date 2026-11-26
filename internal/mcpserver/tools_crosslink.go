package mcpserver

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// toolCrossLinkRewrite retargets, in this KB, every cross-KB link to given
// pages of other KBs. It is the write path of the cross-KB rewrite and its
// retry path: ordinary tool of this KB (its own lock, write gate, commit and
// audit, as if the caller had called it here), never a helper that writes
// another KB's files. Idempotent: a second identical call finds nothing and
// commits nothing.
func toolCrossLinkRewrite(k *kb.KB) Tool {
	return Tool{
		Name: "cross_link_rewrite",
		Description: "Retargets, in this KB, every cross-KB wiki-link [[kb:<from_kb>/<from_id>]] to a page that changed address: each retarget {from_kb, from_id, to_kb, to_id} (1 to 50) rewrites the links to [[kb:<to_kb>/<to_id>]], keeping #section and |label; a target that is this KB itself becomes the internal form [[<to_id>]]. " +
			"Links by an old mount name of from_kb are found too, unless a relation of this KB pins that name to another remote (then they are left alone). Index files, code spans and links with a server qualifier are not touched. One commit; zero links is a success and commits nothing, so repeating a call is safe. " +
			"A move, archive or transfer in another KB does this for you in the KBs you can write; call this tool to repair one that failed. Needs a whole-KB write grant. Returns {rewritten:{pages, links}, findings}.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["retargets"],
			"properties": {
				"retargets": {
					"type": "array",
					"minItems": 1,
					"maxItems": 50,
					"description": "Pages that changed address: {from_kb, from_id} is where the link points now, {to_kb, to_id} where the page lives",
					"items": {
						"type": "object",
						"required": ["from_kb", "from_id", "to_kb", "to_id"],
						"properties": {
							"from_kb": {"type": "string"},
							"from_id": {"type": "string"},
							"to_kb": {"type": "string"},
							"to_id": {"type": "string"}
						}
					}
				}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var params struct {
				Retargets []struct {
					FromKB string `json:"from_kb"`
					FromID string `json:"from_id"`
					ToKB   string `json:"to_kb"`
					ToID   string `json:"to_id"`
				} `json:"retargets"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if n := len(params.Retargets); n == 0 || n > transferMaxMoves {
				return errorResult(fmt.Sprintf("'retargets' takes 1 to %d entries", transferMaxMoves)), nil
			}
			m := k.Mounts()
			if m == nil || k.MountName() == "" {
				return errorResult("cross_link_rewrite needs a server mounting several KBs"), nil
			}
			moves := make([]kb.CrossRetarget, 0, len(params.Retargets))
			idsByKB := map[string]map[string]bool{}
			for i, r := range params.Retargets {
				if !kb.ValidMountName(r.FromKB) || !kb.ValidMountName(r.ToKB) {
					return errorResult(fmt.Sprintf("retargets[%d]: from_kb and to_kb must be KB names", i)), nil
				}
				if okf.ValidateID(r.FromID) != nil || okf.ValidateID(r.ToID) != nil {
					return errorResult(fmt.Sprintf("retargets[%d]: from_id and to_id must be concept ids", i)), nil
				}
				moves = append(moves, kb.CrossRetarget{FromKB: r.FromKB, FromID: r.FromID, ToKB: r.ToKB, ToID: r.ToID})
				cur := m.Canonical(r.FromKB)
				if idsByKB[cur] == nil {
					idsByKB[cur] = map[string]bool{}
				}
				idsByKB[cur][r.FromID] = true
			}
			// The names a link may carry to reach each KB: the current one and the
			// aliases this KB follows (a relation's remote guard can rule one out).
			followed := map[string][]string{}
			for _, mv := range moves {
				cur := m.Canonical(mv.FromKB)
				if _, done := followed[cur]; done {
					continue
				}
				ns, err := k.FollowedNames(cur)
				if err != nil {
					return errorResult("cross_link_rewrite: " + relPathErr(err, k.Root)), nil
				}
				followed[cur] = ns
			}
			names := func(name string) []string { return followed[m.Canonical(name)] }
			own := m.NamesFor(k.MountName())

			pages := map[okf.ConceptID]bool{}
			for cur, ids := range idsByKB {
				found, err := k.CrossLinksTo(names(cur), ids)
				if err != nil {
					return errorResult("cross_link_rewrite: " + relPathErr(err, k.Root)), nil
				}
				for _, id := range found {
					pages[id] = true
				}
			}
			sorted := make([]okf.ConceptID, 0, len(pages))
			for id := range pages {
				sorted = append(sorted, id)
			}
			sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

			// Everything is read and parsed before anything is written: a page
			// that cannot be rewritten fails the call with nothing on disk.
			type edit struct {
				id   okf.ConceptID
				fm   *okf.Frontmatter
				body string
				hash string
			}
			var edits []edit
			links := 0
			for _, id := range sorted {
				data, err := k.ReadConcept(id)
				if err != nil {
					return errorResult(fmt.Sprintf("cross_link_rewrite: read %q: %v", id, relPathErr(err, k.Root))), nil
				}
				newBody, n := kb.RewriteCrossLinks(data.Body, own, moves, names)
				if n == 0 {
					continue
				}
				fm, err := okf.ParseFrontmatter(data.FrontmatterRaw)
				if err != nil {
					return errorResult(fmt.Sprintf("cross_link_rewrite: parse frontmatter %q: %v", id, err)), nil
				}
				edits = append(edits, edit{id, fm, newBody, data.ContentHash})
				links += n
			}
			written := make([]string, 0, len(edits))
			for _, e := range edits {
				if _, err := k.WriteConcept(e.id, e.fm, e.body, e.hash); err != nil {
					return errorResult(fmt.Sprintf("cross_link_rewrite: write %q: %v", e.id, relPathErr(err, k.Root))), nil
				}
				written = append(written, string(e.id))
			}
			result := map[string]interface{}{"rewritten": map[string]int{"pages": len(edits), "links": links}}
			if len(written) > 0 {
				f, repaired, _ := repairWritten(k, written, nil, newCallerView(ctx, k))
				if f != nil {
					result["findings"] = f
				}
				if len(repaired) > 0 {
					result["repaired"] = repaired
				}
			}
			return jsonResult(result), nil
		},
	}
}
