package mcpserver

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

const (
	// maxChangesLinksEdges caps the edges changes_since(links) reports (D250).
	maxChangesLinksEdges = 500
	// maxChangesLinksFiles bounds the git show calls: past it the structural
	// delta is omitted with a note rather than computed unbounded.
	maxChangesLinksFiles = 1000
)

type changesLinkEdge struct {
	Source        string `json:"source"`
	Target        string `json:"target"`
	TargetMissing bool   `json:"target_missing,omitempty"`
}

type changesLinks struct {
	Added          []changesLinkEdge `json:"added"`
	Removed        []changesLinkEdge `json:"removed"`
	BecameOrphan   []string          `json:"became_orphan"`
	NoLongerOrphan []string          `json:"no_longer_orphan"`
	Truncated      bool              `json:"truncated,omitempty"`
}

// linkSourceFromGitPath maps a repository path to the concept it holds and
// its KB-relative physical path. Unlike kb.GitPathToConceptID it keeps an
// expanded concept's index.md ("data/<map>/<concept>/index.md"), because that
// file is where the expanded concept's links live.
func linkSourceFromGitPath(gitPath string) (id, rel string, ok bool) {
	if id, ok := kb.GitPathToConceptID(gitPath); ok {
		return id, strings.TrimPrefix(gitPath, "data/"), true
	}
	rel = strings.TrimPrefix(gitPath, "data/")
	if rel == gitPath || path.Base(rel) != "index.md" {
		return "", "", false
	}
	dir := path.Dir(rel)
	if strings.Count(dir, "/") != 1 {
		return "", "", false
	}
	return dir, rel, true
}

// changesSinceLinks is the structural delta of commits (newest first, as
// gitx.LogNameStatus returns them), D250. An edge belongs to its source file,
// so the edges that changed are, over the concept files changed in the range,
// the difference between their links at the base commit (the parent of the
// oldest commit) and their links now (the cached graph, D241). Only edges
// with both endpoints visible to the caller count, so hidden concepts never
// show up — not as an edge, and not as the linker that keeps a page from
// being an orphan. note is set, and the delta nil, when the range touches too
// many files.
func changesSinceLinks(ctx requestContext, k *kb.KB, commits []gitx.CommitChanges) (*changesLinks, string, error) {
	out := &changesLinks{Added: []changesLinkEdge{}, Removed: []changesLinkEdge{}, BecameOrphan: []string{}, NoLongerOrphan: []string{}}
	if len(commits) == 0 {
		return out, "", nil
	}

	// basePath[current path] is the path the same file had at the base, so a
	// source renamed within the range is compared with its own old content.
	basePath := map[string]string{}
	for i := len(commits) - 1; i >= 0; i-- {
		for _, f := range commits[i].Files {
			if f.Status == "R" {
				origin, seen := basePath[f.OldPath]
				if !seen {
					origin = f.OldPath
				}
				delete(basePath, f.OldPath)
				basePath[f.Path] = origin
				continue
			}
			if _, seen := basePath[f.Path]; !seen {
				basePath[f.Path] = f.Path
			}
		}
	}
	for cur, old := range basePath {
		if _, _, ok := linkSourceFromGitPath(cur); !ok {
			if _, _, ok := linkSourceFromGitPath(old); !ok {
				delete(basePath, cur)
			}
		}
	}
	if len(basePath) > maxChangesLinksFiles {
		return nil, fmt.Sprintf("links omitted: %d concept files changed in the range (limit %d)", len(basePath), maxChangesLinksFiles), nil
	}

	// A root commit has no parent: every link in the range is then "added".
	base, err := gitx.HeadSHAAt(k.Root, commits[len(commits)-1].SHA+"^")
	if err != nil {
		base = ""
	}
	graph, err := k.Links()
	if err != nil {
		return nil, "", err
	}
	visible := func(id okf.ConceptID) bool { return Visible(ctx, k, string(id)) }

	var added, removed []changesLinkEdge
	delta := map[okf.ConceptID]int{} // inbound(now) − inbound(then), visible edges only
	for cur, old := range basePath {
		curID, _, curOK := linkSourceFromGitPath(cur)
		oldID, oldRel, oldOK := linkSourceFromGitPath(old)
		if !curOK && !oldOK {
			continue
		}
		source := okf.ConceptID(curID)
		if !curOK {
			source = okf.ConceptID(oldID)
		}
		if !visible(source) {
			continue
		}
		then := map[okf.ConceptID]struct{}{}
		if base != "" && oldOK {
			if content, err := gitx.ShowFile(k.Root, base, old); err == nil {
				_, body, _ := okf.SplitFrontmatter(content)
				// Trap: resolve against the file's path AT THE BASE, not its
				// current one — a relative link in a renamed file pointed
				// wherever it pointed from where the file used to sit. No
				// asset resolver: assets at the base are not probed (D250).
				for _, t := range kb.ExtractLinks(body, oldRel) {
					then[t] = struct{}{}
				}
			}
		}
		now := map[okf.ConceptID]struct{}{}
		if _, exists := graph.Exists[okf.ConceptID(curID)]; curOK && exists {
			now = graph.Out[okf.ConceptID(curID)]
		}
		for t := range now {
			if _, had := then[t]; !had && t != source && visible(t) {
				added = append(added, changesLinkEdge{Source: string(source), Target: string(t)})
				delta[t]++
			}
		}
		for t := range then {
			if _, has := now[t]; !has && t != source && visible(t) {
				_, exists := graph.Exists[t]
				removed = append(removed, changesLinkEdge{Source: string(source), Target: string(t), TargetMissing: !exists})
				delta[t]--
			}
		}
	}

	for t, d := range delta {
		if _, exists := graph.Exists[t]; !exists || d == 0 {
			continue
		}
		inNow := 0
		for src := range graph.In[t] {
			if src != t && visible(src) {
				inNow++
			}
		}
		inThen := inNow - d
		switch {
		case inThen > 0 && inNow == 0:
			out.BecameOrphan = append(out.BecameOrphan, string(t))
		case inThen == 0 && inNow > 0:
			out.NoLongerOrphan = append(out.NoLongerOrphan, string(t))
		}
	}
	sort.Strings(out.BecameOrphan)
	sort.Strings(out.NoLongerOrphan)

	less := func(e []changesLinkEdge) func(i, j int) bool {
		return func(i, j int) bool {
			if e[i].Source != e[j].Source {
				return e[i].Source < e[j].Source
			}
			return e[i].Target < e[j].Target
		}
	}
	sort.Slice(added, less(added))
	sort.Slice(removed, less(removed))
	if len(added)+len(removed) > maxChangesLinksEdges {
		out.Truncated = true
		if len(added) > maxChangesLinksEdges {
			added = added[:maxChangesLinksEdges]
		}
		removed = removed[:maxChangesLinksEdges-len(added)]
	}
	out.Added = append(out.Added, added...)
	out.Removed = append(out.Removed, removed...)
	return out, "", nil
}
