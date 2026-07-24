package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// cmdImport is the D74 WP2 mechanical scaffold: it walks --source for .md
// files, maps their source directories onto KB Map/expanded-concept
// destinations, synthesizes/completes OKF frontmatter (always ensuring
// status: imported, D74 WP1's lint anchor) and writes the result into --kb
// via kb.WriteConcept. By default it leaves the changes for the operator to
// review; --commit opts into one explicit final commit for the whole plan.
func cmdImport(args []string) int {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	source := fs.String("source", "", "Source directory to import (.md files, walked recursively)")
	kbDir := fs.String("kb", "", "Destination KB directory (an existing local clone, see kb.Open)")
	defaultMap := fs.String("default-map", "", "Default destination map for source directories with no --map entry (D77: was --archive)")
	dryRun := fs.Bool("dry-run", false, "Print the mapping plan without writing")
	commit := fs.Bool("commit", false, "Commit all successfully written import paths once")
	message := fs.String("message", "", "Commit message (implies --commit)")
	dirAsConcept := fs.Bool("dir-as-concept", false, "Import a directory with index.md or README.md as one expanded concept")
	var mapFlags stringSliceFlag
	fs.Var(&mapFlags, "map", "Per-directory mapping <srcdir>=<map> (repeatable)")
	fs.Parse(args)

	if *source == "" || *kbDir == "" {
		fmt.Fprintln(os.Stderr, "Error: --source and --kb are required")
		return 2
	}

	mapping, err := parseImportMap(mapFlags)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	plan, err := buildImportPlanWithOptions(*source, mapping, *defaultMap, *dirAsConcept)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	if *dryRun {
		printImportPlan(plan)
		return 0
	}

	k, err := kb.Open(*kbDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}

	scaffoldPaths, err := createImportMaps(k, plan)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	imported, errored, writtenPaths := applyImportPlan(k, plan, *source)
	writtenPaths = append(scaffoldPaths, writtenPaths...)
	if *commit || *message != "" {
		commitMessage := *message
		if commitMessage == "" {
			commitMessage = fmt.Sprintf("import: %s -> %s", *source, *kbDir)
		}
		if len(writtenPaths) > 0 {
			if err := k.CommitPaths(writtenPaths, commitMessage); err != nil {
				fmt.Fprintln(os.Stderr, "Error: commit:", err)
				return 1
			}
			if errored > 0 {
				fmt.Printf("committed %d import path(s) despite %d error(s)\n", len(writtenPaths), errored)
			} else {
				fmt.Printf("committed %d import path(s)\n", len(writtenPaths))
			}
		}
	}
	fmt.Printf("imported: %d, skipped: %d, errors: %d\n", imported, len(plan.skipped), errored)
	if errored > 0 {
		return 1
	}
	return 0
}

// stringSliceFlag implements flag.Value, accumulating one entry per
// occurrence of a repeatable flag (used by --map).
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// importMapEntry is one --map <srcdir>=<map> entry, normalized: from is
// a clean forward-slash path relative to --source ("." for the source root
// itself), to is the destination map[/expanded concept] with no leading/
// trailing slash.
type importMapEntry struct{ from, to string }

// parseImportMap parses and normalizes every --map flag occurrence.
func parseImportMap(raw []string) ([]importMapEntry, error) {
	var out []importMapEntry
	for _, r := range raw {
		idx := strings.Index(r, "=")
		if idx < 0 {
			return nil, fmt.Errorf("invalid --map %q: expected <srcdir>=<map>", r)
		}
		fromRaw := strings.TrimSpace(r[:idx])
		toRaw := strings.TrimSpace(r[idx+1:])
		if fromRaw == "" || toRaw == "" {
			return nil, fmt.Errorf("invalid --map %q: expected <srcdir>=<map>", r)
		}
		from := path.Clean(filepath.ToSlash(fromRaw))
		to := strings.Trim(filepath.ToSlash(toRaw), "/")
		out = append(out, importMapEntry{from: from, to: to})
	}
	return out, nil
}

// importFile is one planned import: the source path (relative to --source,
// forward-slash, with .md) and its destination concept ID.
type importFile struct {
	srcRel   string
	destID   okf.ConceptID
	destPath string // data-root-relative path actually written, including .md
	promoted bool
}

// importPlan is the full result of walking --source and resolving the
// destination-map mapping, before any write happens — the same plan is
// printed by --dry-run and executed by applyImportPlan.
type importPlan struct {
	files   []importFile
	skipped []string // non-.md files found under --source, for the summary count
	maps    []string // destination map names, one per map touched by the plan
}

// buildImportPlan walks source for .md files (skipping hidden directories),
// resolves each file's destination map via mapping (falling back to
// defaultMap), and derives a destination concept ID per file,
// de-duplicating slug collisions within the same destination directory.
// Returns an explicit error if any source directory containing .md files has
// neither a --map entry nor a --default-map to fall back to.
func buildImportPlan(source string, mapping []importMapEntry, defaultMap string) (*importPlan, error) {
	return buildImportPlanWithOptions(source, mapping, defaultMap, false)
}

// buildImportPlanWithOptions also promotes source directories containing an
// index.md (or, when absent, README.md) into expanded concepts when requested.
func buildImportPlanWithOptions(source string, mapping []importMapEntry, defaultMap string, dirAsConcept bool) (*importPlan, error) {
	source = filepath.Clean(source)

	var mdFiles []string
	var skipped []string

	walkErr := filepath.WalkDir(source, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == source {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(source, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if strings.HasSuffix(strings.ToLower(name), ".md") {
			mdFiles = append(mdFiles, rel)
		} else {
			skipped = append(skipped, rel)
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("import: walk %s: %w", source, walkErr)
	}
	sort.Strings(mdFiles)

	mapIndex := map[string]string{}
	for _, m := range mapping {
		mapIndex[m.from] = m.to
	}

	destDirFor := map[string]string{}
	unmapped := map[string]bool{}
	for _, rel := range mdFiles {
		srcDir := path.Dir(rel)
		if _, done := destDirFor[srcDir]; done {
			continue
		}
		if dest, ok := mapIndex[srcDir]; ok {
			destDirFor[srcDir] = dest
		} else if defaultMap != "" {
			destDirFor[srcDir] = defaultMap
		} else {
			unmapped[srcDir] = true
		}
	}
	if len(unmapped) > 0 {
		var dirs []string
		for d := range unmapped {
			dirs = append(dirs, d)
		}
		sort.Strings(dirs)
		return nil, fmt.Errorf("unmapped source directory/ies (add --map <srcdir>=<map> or a --default-map): %s",
			strings.Join(dirs, ", "))
	}

	promotedIndex := map[string]string{}
	if dirAsConcept {
		for _, rel := range mdFiles {
			dir := path.Dir(rel)
			switch {
			case strings.EqualFold(path.Base(rel), "index.md"):
				promotedIndex[dir] = rel // index.md takes precedence over README.md
			case strings.EqualFold(path.Base(rel), "README.md"):
				if _, already := promotedIndex[dir]; !already {
					promotedIndex[dir] = rel
				}
			}
		}
	}

	plan := &importPlan{skipped: skipped}
	mapNames := map[string]bool{}
	used := map[string]int{}
	for _, rel := range mdFiles {
		destDir := destDirFor[path.Dir(rel)]
		mapNames[strings.Split(destDir, "/")[0]] = true
		var destID, destPath string
		promoted := false
		if indexRel, ok := promotedIndex[path.Dir(rel)]; ok {
			concept := path.Join(destDir, slugify(importSourceDirName(source, path.Dir(rel))))
			if rel == indexRel {
				destID, destPath, promoted = concept, path.Join(concept, "index.md"), true
			} else {
				destID = path.Join(concept, slugify(strings.TrimSuffix(path.Base(rel), ".md")))
				destPath = destID + ".md"
			}
		} else {
			destID = path.Join(destDir, slugify(strings.TrimSuffix(path.Base(rel), ".md")))
			destPath = destID + ".md"
		}
		if n, ok := used[destPath]; ok {
			n++
			used[destPath] = n
			destID = fmt.Sprintf("%s-%d", destID, n)
			if promoted {
				destPath = path.Join(destID, "index.md")
			} else {
				destPath = destID + ".md"
			}
		} else {
			used[destPath] = 1
		}
		plan.files = append(plan.files, importFile{srcRel: rel, destID: okf.ConceptID(destID), destPath: destPath, promoted: promoted})
	}
	for name := range mapNames {
		plan.maps = append(plan.maps, name)
	}
	sort.Strings(plan.maps)
	return plan, nil
}

func importSourceDirName(source, dir string) string {
	if dir == "." {
		return filepath.Base(source)
	}
	return path.Base(dir)
}

// printImportPlan renders the --dry-run output: one "source -> dest" line
// per planned file plus a summary count.
func printImportPlan(plan *importPlan) {
	for _, f := range plan.files {
		if f.promoted {
			fmt.Printf("[dry-run] promote %s -> %s\n", f.srcRel, f.destPath)
		} else {
			fmt.Printf("[dry-run] %s -> %s\n", f.srcRel, f.destPath)
		}
	}
	fmt.Printf("would import: %d, skipped: %d\n", len(plan.files), len(plan.skipped))
}

// applyImportPlan executes plan against k: for every planned file, it reads
// the source content, completes its frontmatter (prepareFrontmatter),
// rewrites relative markdown links to the new layout (rewriteMarkdownLinks —
// wiki-links are left untouched, D72/D74), and writes it via
// kb.WriteConcept. A per-file error is reported and counted, not fatal to the
// rest of the batch.
func applyImportPlan(k *kb.KB, plan *importPlan, source string) (imported, errored int, writtenPaths []string) {
	moveMap := make(map[string]string, len(plan.files))
	for _, f := range plan.files {
		moveMap[strings.TrimSuffix(f.srcRel, ".md")] = f.destPath
	}

	for _, f := range plan.files {
		abs := filepath.Join(source, filepath.FromSlash(f.srcRel))
		data, err := os.ReadFile(abs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: read %s: %v\n", f.srcRel, err)
			errored++
			continue
		}

		fmRaw, body, hasFM := okf.SplitFrontmatter(string(data))
		fallbackTitle := strings.TrimSuffix(path.Base(f.srcRel), ".md")
		frontmatter, err := prepareFrontmatter(fmRaw, hasFM, body, fallbackTitle)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %s: %v\n", f.srcRel, err)
			errored++
			continue
		}

		newBody := rewriteMarkdownLinks(body, f.srcRel, f.destPath, moveMap)

		var writeErr error
		if f.promoted {
			_, writeErr = k.WriteExpandedConcept(f.destID, frontmatter, newBody, "")
		} else {
			_, writeErr = k.WriteConcept(f.destID, frontmatter, newBody, "")
		}
		if writeErr != nil {
			fmt.Fprintf(os.Stderr, "Error: write %s: %v\n", f.destID, writeErr)
			errored++
			continue
		}
		fmt.Printf("imported %s -> %s.md\n", f.srcRel, f.destID)
		imported++
		writtenPaths = append(writtenPaths, filepath.ToSlash(filepath.Join("data", f.destPath)))
	}
	return imported, errored, writtenPaths
}

// createImportMaps creates the standard scaffold for every destination map
// absent from the KB before import. Existing maps, including legacy implicit
// directories, are left untouched.
func createImportMaps(k *kb.KB, plan *importPlan) ([]string, error) {
	var paths []string
	for _, name := range plan.maps {
		mapAbs := filepath.Join(k.DataRoot(), name)
		info, err := os.Stat(mapAbs)
		if err == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("destination map %q is not a directory", name)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("stat destination map %q: %w", name, err)
		}
		if err := k.CreateMap(name, name, "map", nil, ""); err != nil {
			return nil, err
		}
		for _, scaffold := range []string{"_map.md", "index.md", "log.md"} {
			paths = append(paths, filepath.ToSlash(filepath.Join("data", name, scaffold)))
		}
	}
	return paths, nil
}

// firstH1Re matches a level-1 markdown heading line.
var firstH1Re = regexp.MustCompile(`(?m)^#[ \t]+(.+?)[ \t]*$`)

// prepareFrontmatter parses fmRaw (if hasFM) and returns a frontmatter with
// title/type/status completed for any field that was missing — an existing
// value for any of these is never overwritten (D74: "preserve, adding only
// the missing fields"). title falls back to the first H1 in body, then to
// fallbackTitle (the source filename without extension). type has no
// equivalent in the source spec text but is required by kb.WriteConcept, so
// a generic "Note" default is synthesized when absent (documented deviation,
// see docs/decisions.md D74). status defaults to "imported" — the D74 WP1
// lint anchor.
func prepareFrontmatter(fmRaw string, hasFM bool, body string, fallbackTitle string) (*okf.Frontmatter, error) {
	raw := ""
	if hasFM {
		raw = fmRaw
	}
	frontmatter, err := okf.ParseFrontmatter(raw)
	if err != nil {
		return nil, fmt.Errorf("parse existing frontmatter: %w", err)
	}

	if _, ok := frontmatter.Get("title"); !ok {
		title := fallbackTitle
		if m := firstH1Re.FindStringSubmatch(body); m != nil {
			title = strings.TrimSpace(m[1])
		}
		frontmatter.Set("title", title)
	}
	if _, ok := frontmatter.Get("type"); !ok {
		frontmatter.Set("type", "Note")
	}
	if _, ok := frontmatter.Get("status"); !ok {
		frontmatter.Set("status", "imported")
	}
	return frontmatter, nil
}

// importMDLinkRe matches markdown links [text](href), mirroring kb's
// (unexported) mdLinkRe.
var importMDLinkRe = regexp.MustCompile(`\[([^\]]*)\]\(([^)]+)\)`)

// rewriteMarkdownLinks rewrites, in body, every relative markdown link whose
// resolved target (relative to srcRel's own directory) is a key in moveMap,
// to a new relative path from destID's own directory. Absolute URLs,
// fragment-only links and mailto: links are left untouched, as are links
// with no match in moveMap (best-effort, broken_link is the safety net).
// Wiki-links [[...]] are not touched at all here — D74/D72 keep them as-is.
func rewriteMarkdownLinks(body, srcRel, destPath string, moveMap map[string]string) string {
	baseDir := path.Dir(srcRel)
	newBaseDir := path.Dir(destPath)

	return importMDLinkRe.ReplaceAllStringFunc(body, func(match string) string {
		sub := importMDLinkRe.FindStringSubmatch(match)
		text, href := sub[1], sub[2]
		if strings.Contains(href, "://") || strings.HasPrefix(href, "#") || strings.HasPrefix(href, "mailto:") {
			return match
		}

		pathPart, frag := href, ""
		if idx := strings.Index(href, "#"); idx >= 0 {
			pathPart, frag = href[:idx], href[idx:]
		}
		if pathPart == "" {
			return match
		}

		pathPartMd := pathPart
		if !strings.HasSuffix(pathPartMd, ".md") {
			pathPartMd += ".md"
		}

		resolved := path.Clean(path.Join(baseDir, pathPartMd))
		if strings.HasPrefix(resolved, "..") {
			return match
		}

		targetKey := strings.TrimSuffix(resolved, ".md")
		newTarget, ok := moveMap[targetKey]
		if !ok {
			return match
		}

		newHref := relLinkPath(newBaseDir, newTarget) + frag
		return "[" + text + "](" + newHref + ")"
	})
}

// relLinkPath returns the relative forward-slash path from directory baseDir
// to file targetPath (both clean forward-slash paths from the same root,
// baseDir "." meaning that root) — a local copy of kb's unexported relLink,
// scoped here to avoid exporting KB-internal move machinery for a single
// caller.
func relLinkPath(baseDir, targetPath string) string {
	baseDir = path.Clean(baseDir)
	var baseParts []string
	if baseDir != "." {
		baseParts = strings.Split(baseDir, "/")
	}
	targetParts := strings.Split(path.Clean(targetPath), "/")

	i := 0
	for i < len(baseParts) && i < len(targetParts)-1 && baseParts[i] == targetParts[i] {
		i++
	}

	var relParts []string
	for j := i; j < len(baseParts); j++ {
		relParts = append(relParts, "..")
	}
	relParts = append(relParts, targetParts[i:]...)
	return strings.Join(relParts, "/")
}

// slugify converts an arbitrary source filename (without extension) into a
// kebab-case concept-ID segment (okf.PathToID requires lowercase
// letters/digits/hyphens/dots/underscores): lowercased, any run of other
// characters collapsed to a single hyphen, leading/trailing hyphens trimmed.
// Falls back to "concept" if nothing alphanumeric survives.
func slugify(name string) string {
	var b strings.Builder
	lastDash := true // avoid a leading hyphen
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	slug := strings.TrimRight(b.String(), "-")
	if slug == "" {
		return "concept"
	}
	return slug
}
