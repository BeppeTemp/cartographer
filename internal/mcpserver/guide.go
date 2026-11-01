package mcpserver

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
)

// The operating guide (D1097): what `initialize` and `server/discover` tell a
// connection that has no managed instructions block, so a client with no
// Cartographer client side can still work. It is read from disk at each
// handshake (an artifact_write or a pull shows up at the next one), for the
// connection's own KBs and the caller's own visibility.

// GuideKB gathers one KB's guide input for the caller of ctx: its name, the
// archives the caller may see (the filter atlas_overview applies) and the
// curated instructions.md. ok is false when the caller has no access to the KB,
// so a guide never names a KB the caller cannot read. Nothing here fails a
// handshake: an unreadable or symlinked file yields no curated section.
// Reusable with one KB and a request context.
func GuideKB(ctx context.Context, k *kb.KB, name string) (g provisioning.GuideKB, ok bool) {
	// The set of a routed view already holds only readable KBs and the metadata
	// gate asks the same of a per-KB endpoint: this is the belt that keeps a
	// stale set from leaking a name.
	if !auth.PrincipalFromContext(ctx).Policy.HasKBAccess(name, false) {
		return g, false
	}
	g.Name = name
	all := guideArchiveNames(k.Root)
	for _, a := range all {
		kind := "map"
		if meta, err := k.ReadArchiveMeta(a); err == nil {
			if v, found := meta.Get("kind"); found {
				kind, _ = v.(string)
			}
		}
		if VisibleCollection(ctx, k, a, kind) {
			g.Archives = append(g.Archives, a)
		}
	}
	g.ArchivesHidden = len(all) > 0 && len(g.Archives) == 0
	// instructions.md is the KB's own directions to whoever works in it, so it
	// reaches any caller with access to the KB (artifact_read keeps its whole-KB
	// rule for the file itself). A symlink is refused: a file outside the KB
	// must not reach a handshake.
	if rejectArtifactSymlinks(k.Root, "instructions.md") == nil {
		g.Curated, g.SkipPreamble = provisioning.KBCuratedInstructions(k.Root)
	}
	return g, true
}

// procedureLines are the served procedures whose body the bundle holds, for the
// guide's list (#1316). The one copy of the line source: connectionGuide
// (initialize) and renderKBGuide (kb_guide) both call it. No bundle, no list.
func procedureLines(bundle fs.FS) []provisioning.ProcedureLine {
	if bundle == nil {
		return nil
	}
	var lines []provisioning.ProcedureLine
	for _, p := range skillbundle.Procedures() {
		if _, err := p.Body(bundle); err != nil {
			continue
		}
		lines = append(lines, provisioning.ProcedureLine{Short: p.Short, UseWhen: p.UseWhen})
	}
	return lines
}

// guideArchiveNames lists the KB's archives, none when data/ is a symlink: a
// listing of a directory outside the KB must not reach a handshake.
func guideArchiveNames(root string) []string {
	if fi, err := os.Lstat(filepath.Join(root, "data")); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	return provisioning.KBArchiveNames(root)
}

// connectionGuide renders the guide of the connection this Server answers, or
// "" when it has no KB (tests) or the caller reads none of them.
func (s *Server) connectionGuide(ctx context.Context) string {
	type entry struct {
		name string
		k    *kb.KB
	}
	var entries []entry
	routed := len(s.connKBs) > 0 && s.connKB != nil
	if routed {
		for _, n := range s.connKBs {
			if k := s.connKB(n); k != nil {
				entries = append(entries, entry{n, k})
			}
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	} else if s.kbRef != nil {
		name := s.PolicyKB()
		if name == "" {
			name = kbName(s.kbRef)
		}
		entries = append(entries, entry{name, s.kbRef})
	}
	var kbs []provisioning.GuideKB
	var names []string
	for _, e := range entries {
		if g, ok := GuideKB(ctx, e.k, e.name); ok {
			kbs = append(kbs, g)
			names = append(names, e.name)
		}
	}
	if len(kbs) == 0 {
		return ""
	}
	s.mu.Lock()
	readOnly := s.readOnlySession || s.readMinSession || s.tokenReadOnly
	s.mu.Unlock()
	if !readOnly && !writesAnyKB(ctx, names) {
		readOnly = true
	}
	return provisioning.RenderGuide(kbs, provisioning.GuideOptions{Routed: routed, ReadOnly: readOnly, Procedures: procedureLines(s.guideBundle)})
}

// handshakeInstructions is the `instructions` of one handshake: what a
// provisioned connection gets as before (nothing, or the bridge's read-only
// sentence), the guide for everyone else.
func (s *Server) handshakeInstructions(ctx context.Context) string {
	if isProvisioned(ctx) {
		return s.sessionInstructions()
	}
	return s.connectionGuide(ctx)
}
