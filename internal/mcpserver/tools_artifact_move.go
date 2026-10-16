package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
)

// artifactMoveMaxUnits bounds one call, like transferMaxMoves: both KBs' git
// locks are held for the whole of it.
const artifactMoveMaxUnits = 20

type artifactMoveParams struct {
	TargetKB string   `json:"target_kb"`
	Paths    []string `json:"paths"`
}

// artifactUnit is one validated thing that moves: a skill or hook directory, or
// a single agent, mcp or template file. The path is the same in both KBs.
type artifactUnit struct {
	rel   string // slash path below the KB root: skills/<slug>, hooks/<name>, agents/<slug>.md…
	info  artifactPathInfo
	isDir bool
	files []transferFile // slash paths below the unit; a single file unit has one, rel ""
}

// artifactMovePlan is everything validation learned, so the two steps only write.
type artifactMovePlan struct {
	units     []artifactUnit
	placehold []string // placeholders the target's paths.yaml does not declare
	sops      []string
	refs      []string // "<path> → <unit>": text in the source that cites a unit by path
	findings  []findingOut
}

func toolArtifactMove(src *kb.KB, deps Deps) Tool {
	t := Tool{
		Name: "artifact_move",
		Description: "Moves artifacts to another KB on this server, in one commit per KB: a whole skill (skills/<slug>) or hook (hooks/<name>) directory, " +
			"or a single agents/<slug>.md, mcp/<slug>.json or templates/<slug>.md. The path stays the same in the target and must be free there. " +
			"Bytes copied verbatim. instructions.md, paths.yaml and glossary.yaml describe their own KB and do not move. " +
			"Needs allow_artifact_write on both KBs.",
		InputSchema: json.RawMessage(fmt.Sprintf(`{
			"type": "object",
			"required": ["target_kb", "paths"],
			"properties": {
				"target_kb": {"type": "string", "description": "Another KB this server mounts"},
				"paths": {
					"type": "array",
					"maxItems": %d,
					"items": {"type": "string", "description": "The artifact, not a file inside it: skills/<slug>, hooks/<name>, agents/<slug>.md, mcp/<slug>.json, templates/<slug>.md"}
				}
			}
		}`, artifactMoveMaxUnits)),
	}
	t.InputSchema = withReasonProperty(t.Name, t.InputSchema)
	t.GitWrapped = true
	t.Handler = func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
		var p artifactMoveParams
		if err := json.Unmarshal(args, &p); err != nil {
			return errorResult("invalid params: " + err.Error()), nil
		}
		if deps.KBByName == nil {
			return errorResult(errTransferNeedsServer), nil
		}
		if p.TargetKB == "" {
			return errorResult("'target_kb' is required"), nil
		}
		if len(p.Paths) == 0 || len(p.Paths) > artifactMoveMaxUnits {
			return errorResult(fmt.Sprintf("'paths' needs 1 to %d entries", artifactMoveMaxUnits)), nil
		}
		dst, ok := deps.KBByName(p.TargetKB)
		if !ok {
			return errorResult(fmt.Sprintf("unknown target_kb %q", p.TargetKB)), nil
		}
		srcName := kbName(src)
		if dst == src || dst.Root == src.Root || p.TargetKB == srcName {
			return errorResult("target_kb is this KB: artifacts are already here"), nil
		}
		if set := routedSetFrom(ctx); set != nil && !containsString(set, p.TargetKB) {
			return errorResult(fmt.Sprintf("kb %q is not bound to this connection (allowed: %s)", p.TargetKB, strings.Join(set, ", "))), nil
		}
		if err := authorizeArtifactMoveTarget(ctx, p.TargetKB); err != nil {
			return errorResult(err.Error()), nil
		}

		// TRAP (lock order): same rule as concept_transfer, one global order by KB
		// root path, whichever side is the source.
		first, second := src, dst
		if second.Root < first.Root {
			first, second = second, first
		}
		var res ToolResult
		_ = first.WithGitLock(func() error {
			return second.WithGitLock(func() error {
				res = runArtifactMove(ctx, args, src, srcName, dst, p)
				return nil
			})
		})
		return res, nil
	}
	return t
}

// authorizeArtifactMoveTarget is the target half of the policy: the move adds to
// the target what artifact_write would, so it needs the whole-KB write grant
// there, as authorizeTool demands on the source. The per-KB right to write
// artifacts (allow_artifact_write, or allow_template_write for templates/ only)
// is checked by the plan, against the settings read under the locks.
func authorizeArtifactMoveTarget(ctx context.Context, dstName string) error {
	p := auth.PrincipalFromContext(ctx)
	if p.ID != "" && p.Policy.Admin {
		return nil
	}
	if !p.Policy.HasKBAccess(dstName, false) {
		return deny(denyNoKBAccess)
	}
	if !p.Policy.HasKBAccess(dstName, true) {
		return deny(denyReadOnlyToken)
	}
	if !p.Policy.AllowsWholeKB(dstName, true) {
		return deny(denyNeedsWholeKB)
	}
	return nil
}

// runArtifactMove is the body of the tool under both git locks: the artifact
// half of the two-KB transaction (twokbmove.go).
func runArtifactMove(ctx context.Context, args json.RawMessage, src *kb.KB, srcName string, dst *kb.KB, p artifactMoveParams) ToolResult {
	dstName := p.TargetKB
	var plan *artifactMovePlan
	m := &twoKBMove{
		Tool: "artifact_move", Things: "artifacts",
		Src: src, Dst: dst, SrcName: srcName, DstName: dstName, Args: args,
		AuthorizeTarget: func(ctx context.Context) error { return authorizeArtifactMoveTarget(ctx, dstName) },
		Plan: func() *ToolResult {
			var errRes *ToolResult
			plan, errRes = planArtifactMove(src, dst, p.Paths)
			return errRes
		},
		ApplyTarget: func() error {
			if err := applyArtifactMoveTarget(src, dst, plan); err != nil {
				return err
			}
			if err := dst.AppendLog(fmt.Sprintf("artifact_move (%d artifact(s) from %s):\n%s", len(plan.units), srcName, strings.Join(plan.logLines(srcName, dstName, false), "\n")), time.Now()); err != nil {
				return err
			}
			plan.findings = plan.lintMoved(dst)
			return nil
		},
		ApplySource: func() error {
			if err := applyArtifactMoveSource(src, plan); err != nil {
				return err
			}
			return src.AppendLog(fmt.Sprintf("artifact_move (%d artifact(s) to %s):\n%s", len(plan.units), dstName, strings.Join(plan.logLines(srcName, dstName, true), "\n")), time.Now())
		},
		TargetSubject: func() string { return plan.subject("from", srcName) },
		SourceSubject: func() string { return plan.subject("to", dstName) },
		Report: func(srcSHA, dstSHA string) map[string]any {
			return artifactMoveReport(plan, srcName, dstName, srcSHA, dstSHA)
		},
	}
	return m.run(ctx)
}

func (pl *artifactMovePlan) subject(dir, other string) string {
	paths := make([]string, 0, len(pl.units))
	for _, u := range pl.units {
		paths = append(paths, u.rel)
	}
	return fmt.Sprintf("artifact_move: %s %s %s", strings.Join(paths, ", "), dir, other)
}

func (pl *artifactMovePlan) logLines(srcName, dstName string, source bool) []string {
	lines := make([]string, 0, len(pl.units))
	for _, u := range pl.units {
		if source {
			lines = append(lines, fmt.Sprintf("- %s → %s:%s", u.rel, dstName, u.rel))
		} else {
			lines = append(lines, fmt.Sprintf("- %s:%s → %s", srcName, u.rel, u.rel))
		}
	}
	return lines
}

func artifactMoveReport(pl *artifactMovePlan, srcName, dstName, srcSHA, dstSHA string) map[string]any {
	moved := make([]map[string]any, 0, len(pl.units))
	for _, u := range pl.units {
		moved = append(moved, map[string]any{"path": u.rel, "kind": u.info.Kind, "files": len(u.files)})
	}
	result := map[string]any{
		"source": map[string]string{"kb": srcName, "commit": srcSHA},
		"target": map[string]string{"kb": dstName, "commit": dstSHA},
		"moved":  moved,
	}
	if len(pl.placehold) > 0 {
		result["undeclared_placeholders"] = pl.placehold
	}
	if len(pl.sops) > 0 {
		result["sops_warnings"] = pl.sops
	}
	if len(pl.refs) > 0 {
		result["references_in_source"] = pl.refs
	}
	if len(pl.findings) > 0 {
		// findings_by_kb and not "findings", as in concept_transfer: the client
		// write-findings hook (D353) reads that key as one list.
		result["findings_by_kb"] = map[string][]findingOut{dstName: pl.findings}
	}
	return result
}

// parseArtifactUnit turns one requested path into the unit it names. The unit
// is the artifact (skills/<slug>, hooks/<name>) and not a file in it: moving
// half a skill would leave neither KB with a working one.
func parseArtifactUnit(raw string) (artifactUnit, error) {
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return artifactUnit{}, fmt.Errorf("path %q has a control character", raw)
		}
	}
	if raw == "" {
		return artifactUnit{}, fmt.Errorf("'paths' has an empty entry")
	}
	if filepath.IsAbs(raw) {
		return artifactUnit{}, fmt.Errorf("path not allowed: absolute path %q", raw)
	}
	clean := filepath.ToSlash(filepath.Clean(raw))
	segs := strings.Split(clean, "/")
	switch segs[0] {
	case "skills", "hooks":
		if len(segs) > 2 {
			return artifactUnit{}, fmt.Errorf("%q is inside an artifact: name the artifact itself, %s/%s", raw, segs[0], segs[1])
		}
		if len(segs) == 2 && segs[0] == "skills" && !artifactSlugPattern.MatchString(segs[1]) {
			return artifactUnit{}, fmt.Errorf("path %q is not a skill: the name must be a lowercase-hyphenated slug", raw)
		}
		if len(segs) == 2 {
			kind := "skill"
			if segs[0] == "hooks" {
				kind = "hook"
			}
			return artifactUnit{rel: clean, info: artifactPathInfo{Kind: kind, Name: segs[1]}}, nil
		}
	default:
		info, err := classifyArtifactPath(raw)
		if err != nil {
			return artifactUnit{}, err
		}
		switch info.Kind {
		case "agent", "mcp", "template":
			return artifactUnit{rel: clean, info: info}, nil
		case "instructions", "paths", "glossary":
			return artifactUnit{}, fmt.Errorf("%s describes the KB it is in and is not moved between KBs", clean)
		}
	}
	return artifactUnit{}, fmt.Errorf("path %q is not a movable artifact (skills/<slug>, hooks/<name>, agents/<slug>.md, mcp/<slug>.json, templates/<slug>.md)", raw)
}

// artifactWriteRefusal says why a KB cannot take artifact writes of this unit:
// the same right artifact_write needs, so a move cannot reach what a write
// could not.
func artifactWriteRefusal(k *kb.KB, name string, u artifactUnit) string {
	pol := k.Policy()
	if !pol.AllowArtifactWrite && !pol.AllowTemplateWrite {
		return fmt.Sprintf("artifact_move: %s does not accept artifact writes (set kbs[].allow_artifact_write for it)", name)
	}
	if msg := templateOnlyRefusal(k, "artifact_move", u.info); msg != "" {
		return strings.Replace(msg, "on this KB", "on "+name, 1)
	}
	return ""
}

// planArtifactMove validates every unit before any write. Nothing here mutates
// either KB.
func planArtifactMove(src, dst *kb.KB, paths []string) (*artifactMovePlan, *ToolResult) {
	refuse := func(format string, a ...any) (*artifactMovePlan, *ToolResult) {
		r := errorResult("artifact_move: " + fmt.Sprintf(format, a...))
		return nil, &r
	}
	srcName, dstName := kbName(src), kbName(dst)
	plan := &artifactMovePlan{}
	seen := map[string]bool{}
	for _, raw := range paths {
		u, err := parseArtifactUnit(raw)
		if err != nil {
			return refuse("%v", err)
		}
		if seen[u.rel] {
			return refuse("%s is listed twice", u.rel)
		}
		seen[u.rel] = true
		for _, side := range []struct {
			k    *kb.KB
			name string
		}{{src, srcName}, {dst, dstName}} {
			if msg := artifactWriteRefusal(side.k, side.name, u); msg != "" {
				r := errorResult(msg)
				return nil, &r
			}
		}

		for _, side := range []struct {
			k    *kb.KB
			name string
		}{{src, srcName}, {dst, dstName}} {
			if err := rejectArtifactSymlinks(side.k.Root, u.rel); err != nil {
				return refuse("%s in %s: %v", u.rel, side.name, err)
			}
		}
		srcAbs, err := src.ResolveRootPath(u.rel)
		if err != nil {
			return refuse("%s: %v", u.rel, err)
		}
		dstAbs, err := dst.ResolveRootPath(u.rel)
		if err != nil {
			return refuse("%s: %v", u.rel, err)
		}
		info, err := os.Lstat(srcAbs)
		if os.IsNotExist(err) {
			return refuse("%s does not exist in %s", u.rel, srcName)
		}
		if err != nil {
			return refuse("%s: %v", u.rel, err)
		}
		if _, err := os.Lstat(dstAbs); err == nil {
			return refuse("%s already exists in %s: a move never overwrites; rename or delete it there first", u.rel, dstName)
		} else if !os.IsNotExist(err) {
			return refuse("%s in %s: %v", u.rel, dstName, err)
		}

		u.isDir = info.IsDir()
		switch {
		case u.isDir && (u.info.Kind == "skill" || u.info.Kind == "hook"):
			u.files, err = collectTransferFiles(srcAbs)
			if err != nil {
				return refuse("%s: %v", u.rel, err)
			}
			if len(u.files) == 0 {
				return refuse("%s has no files: git cannot carry an empty directory", u.rel)
			}
		case !u.isDir && info.Mode().IsRegular() && u.info.Kind != "skill":
			u.files = []transferFile{{rel: "", size: info.Size(), exec: info.Mode()&0o111 != 0}}
		default:
			return refuse("%s is not the kind of file or directory a %s is", u.rel, u.info.Kind)
		}

		// A file git ignores is not in the source's history: the target commit
		// would not carry it, and removing the unit would destroy the only copy.
		ignored, err := gitx.IgnoredFiles(src.Root, srcAbs)
		if err != nil {
			return refuse("%s: cannot tell which files git ignores: %v", u.rel, err)
		}
		if len(ignored) > 0 {
			return refuse("%s holds files git ignores (%s): they would not be committed and would be lost; remove or un-ignore them first", u.rel, strings.Join(ignored, ", "))
		}

		for _, f := range u.files {
			rel := u.rel
			if f.rel != "" {
				rel += "/" + f.rel
			}
			if f.size > artifactMaxFileSize {
				return refuse("%s is %d bytes, over the %d bytes cap of an artifact file", rel, f.size, artifactMaxFileSize)
			}
			fi := artifactPathInfo{Kind: u.info.Kind, Name: u.info.Name}
			if !isStructuredArtifact(fi, rel) {
				continue
			}
			data, err := os.ReadFile(filepath.Join(srcAbs, filepath.FromSlash(f.rel)))
			if err != nil {
				return refuse("%s: %v", rel, err)
			}
			if err := validateArtifactContent(fi, rel, data); err != nil {
				return refuse("%s does not validate and would not in %s either: %v", rel, dstName, err)
			}
		}
		plan.units = append(plan.units, u)
	}
	sort.Slice(plan.units, func(i, j int) bool { return plan.units[i].rel < plan.units[j].rel })
	if err := plan.scan(src, dst); err != nil {
		return refuse("%v", err)
	}
	return plan, nil
}

// scan fills the reports that never refuse: placeholders the target does not
// declare, sops material, and text in the source that cites a unit by path.
func (pl *artifactMovePlan) scan(src, dst *kb.KB) error {
	reg, err := dst.ReadPathRegistry()
	if err != nil {
		return fmt.Errorf("read the target's paths.yaml: %w", err)
	}
	dstName := kbName(dst)
	undeclared := map[string]bool{}
	for _, u := range pl.units {
		for _, f := range u.files {
			label := u.rel
			if f.rel != "" {
				label += "/" + f.rel
			}
			abs := unitFilePath(src, u, f)
			if strings.Contains(strings.ToLower(filepath.Base(abs)), ".sops.") {
				pl.sops = append(pl.sops, label+": sops-encrypted, copied verbatim and not re-encrypted: decryptable in "+dstName+" only if its age key is a recipient")
			}
			data, err := readTransferHead(abs, transferScanMaxBytes)
			if err != nil || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
				continue
			}
			collectUndeclaredPlaceholders(reg, string(data), undeclared)
		}
	}
	for id := range undeclared {
		pl.placehold = append(pl.placehold, id)
	}
	sort.Strings(pl.placehold)
	pl.refs = pl.referencesInSource(src)
	return nil
}

// artifactRefScanExts are the text files that can cite an artifact by path.
var artifactRefScanExts = map[string]bool{".md": true, ".json": true, ".yaml": true, ".yml": true, ".txt": true}

// artifactRefsMax bounds the report: past it the list says so instead of growing.
const artifactRefsMax = 100

// referencesInSource lists the text files that stay in the source and name a
// moved unit by its path. Nothing is rewritten: a path inside a sentence or a
// script is not a link, and which KB it should point at is the author's call.
func (pl *artifactMovePlan) referencesInSource(src *kb.KB) []string {
	pats := make([]*regexp.Regexp, len(pl.units))
	for i, u := range pl.units {
		// Not followed by a slug character, so skills/a does not match skills/a-b.
		pats[i] = regexp.MustCompile(`(^|[^A-Za-z0-9_./-])` + regexp.QuoteMeta(u.rel) + `($|[^A-Za-z0-9_-])`)
	}
	moved := func(rel string) bool {
		for _, u := range pl.units {
			if rel == u.rel || strings.HasPrefix(rel, u.rel+"/") {
				return true
			}
		}
		return false
	}
	found := map[string]bool{}
	_ = filepath.WalkDir(src.Root, func(abs string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(src.Root, abs)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || moved(rel) || rel == "log.md" || !artifactRefScanExts[strings.ToLower(filepath.Ext(rel))] {
			return nil
		}
		data, rerr := readTransferHead(abs, transferScanMaxBytes)
		if rerr != nil || !utf8.Valid(data) {
			return nil
		}
		for i, pat := range pats {
			if pat.Match(data) {
				found[rel+" → "+pl.units[i].rel] = true
			}
		}
		return nil
	})
	refs := make([]string, 0, len(found))
	for r := range found {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	if len(refs) > artifactRefsMax {
		more := len(refs) - artifactRefsMax
		refs = append(refs[:artifactRefsMax], fmt.Sprintf("… and %d more", more))
	}
	return refs
}

// lintMoved runs the KB's checks on the target as it will be committed and keeps
// what concerns the moved units: a skill that no longer names the target's
// perimeter, or a path that hard-codes a sibling KB's root, shows up only there.
// The findings inform: a lint that cannot run does not stop a move whose bytes
// are already validated.
func (pl *artifactMovePlan) lintMoved(dst *kb.KB) []findingOut {
	all, err := lint.Run(dst, "", false)
	if err != nil {
		return []findingOut{{Check: "artifact_move_lint", Severity: "warning", Message: "the target could not be linted: " + err.Error()}}
	}
	var kept []lint.Finding
	for _, f := range all {
		if !f.Artifact || !lint.ArtifactPanelCheck(f.Check) {
			continue
		}
		for _, u := range pl.units {
			if f.Path == u.rel || strings.HasPrefix(f.Path, u.rel+"/") {
				kept = append(kept, f)
				break
			}
		}
	}
	return findingsOut(kept)
}

// unitFilePath is the absolute path of one file of a unit in a KB; a single-file
// unit is the path itself.
func unitFilePath(k *kb.KB, u artifactUnit, f transferFile) string {
	p := filepath.Join(k.Root, filepath.FromSlash(u.rel))
	if f.rel != "" {
		p = filepath.Join(p, filepath.FromSlash(f.rel))
	}
	return p
}

// applyArtifactMoveTarget copies the units into dst. TRAP (copy, never
// rename), as in applyTransferTarget: two KBs may sit on different filesystems,
// and a rename would remove the source before the target is committed. Any
// error leaves a half-written tree for the caller to discard.
func applyArtifactMoveTarget(src, dst *kb.KB, pl *artifactMovePlan) error {
	for _, u := range pl.units {
		for _, f := range u.files {
			if err := copyTransferFile(unitFilePath(src, u, f), unitFilePath(dst, u, f), f.exec); err != nil {
				return fmt.Errorf("copy %s: %w", u.rel, err)
			}
		}
	}
	return nil
}

// applyArtifactMoveSource removes the units from src. A skill or hook directory
// goes whole; the shared skills/, hooks/, agents/, mcp/ and templates/ parents
// stay, as artifact_delete leaves them.
func applyArtifactMoveSource(src *kb.KB, pl *artifactMovePlan) error {
	for _, u := range pl.units {
		abs := filepath.Join(src.Root, filepath.FromSlash(u.rel))
		var err error
		if u.isDir {
			err = os.RemoveAll(abs)
		} else {
			err = os.Remove(abs)
		}
		if err != nil {
			return fmt.Errorf("remove %s: %w", u.rel, err)
		}
	}
	return nil
}
