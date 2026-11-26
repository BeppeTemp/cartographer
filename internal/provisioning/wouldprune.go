package provisioning

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// RootPredictor predicts, for a dry run, the provisioning roots a real prune would
// remove. It reads names and the disk and never writes or copies anything: a pure
// walk that climbs as pruneEmptyDirs does (ensureSafeDir on every component, a
// recorded root, a real directory by Lstat, never a link) and counts a directory as
// ending empty when every entry under it is a file the run deletes or a directory
// that itself ends empty. A link or an unreadable entry counts as "not empty".
//
// The set of deleted files accumulates across calls, in the order the real run
// goes, so a root two providers share (.config with opencode and crush) is
// predicted at the provider where the real run removes it, and a root is reported
// once. A recorded root the walk could not read is returned as unknown, never
// dropped silently.
type RootPredictor struct {
	baseDir  string
	gone     map[string]bool
	reported map[string]bool
	// unknown keeps the roots already reported as unreadable, across calls.
	unknown map[string]bool
	// memo caches endsEmpty per directory for the length of one Predict call (the
	// set of deleted files only grows between calls), so a climb from many files
	// reads each directory once.
	memo map[string]emptiness
	// above holds every directory that is an ancestor of a deleted file: only those
	// are on a climb, so only those are removed; an empty directory the run never
	// passes through survives and keeps its parent.
	above map[string]bool
}

type emptiness struct{ empty, readable bool }

// readDirFn is os.ReadDir, a seam for the test that bounds the reads.
var readDirFn = os.ReadDir

// NewRootPredictor starts a prediction for one base dir.
func NewRootPredictor(baseDir string) *RootPredictor {
	return &RootPredictor{baseDir: baseDir, gone: map[string]bool{}, reported: map[string]bool{}, unknown: map[string]bool{}, above: map[string]bool{}}
}

// PredictOptions says what a dry-run prune of one provider also deletes.
type PredictOptions struct {
	// Predictor carries the accumulated state across providers; nil starts a fresh one.
	Predictor *RootPredictor
	// Provider names the provider whose files are pruned: the registration files of
	// its hooks (settings.json, hooks.json) are counted as deleted.
	Provider string
	// Whole counts the shared files the removal deletes once nothing else is in them
	// (instructions, MCP config, hook registrations) as gone, on top of the managed
	// files: set by disconnect, unbind and plugin removal, which strip them; the sync
	// dry run (write-findings) counts managed files only.
	Whole bool
	// ConfigFiles counts the provider's MCP config file as deleted when the registry
	// lets an emptied one go (a disconnect removes the entries first).
	ConfigFiles bool
}

// Predict adds files (slash paths relative to the base dir) to the set the run
// deletes and returns the roots newly predicted removed, deepest first, and the
// recorded roots it could not read.
func (p *RootPredictor) Predict(files []string) (roots, unknown []string) {
	for _, f := range files {
		f = filepath.ToSlash(filepath.Clean(f))
		p.gone[f] = true
		for d := path.Dir(f); d != "." && d != "/"; d = path.Dir(d) {
			p.above[d] = true
		}
	}
	p.memo = map[string]emptiness{}
	seenUnknown := p.unknown
	// markUnknown flags every recorded root on dir's path: the walk could not read
	// below it, so it cannot say whether the root ends empty.
	markUnknown := func(dir string) {
		for d := dir; d != "." && d != "/"; d = filepath.ToSlash(filepath.Dir(filepath.FromSlash(d))) {
			if configurator.IsRootDir(d) && configurator.WasDirCreated(p.baseDir, d) && !seenUnknown[d] {
				seenUnknown[d] = true
				unknown = append(unknown, d)
			}
		}
	}
	for _, f := range files {
		dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(filepath.ToSlash(filepath.Clean(f)))))
		for dir != "." && dir != "/" {
			if isPluginBoundary(dir) {
				break
			}
			isRoot := configurator.IsRootDir(dir)
			if isRoot && !configurator.WasDirCreated(p.baseDir, dir) {
				break
			}
			if ensureSafeDir(p.baseDir, dir) != nil {
				break
			}
			fi, err := os.Lstat(filepath.Join(p.baseDir, filepath.FromSlash(dir)))
			if err != nil {
				if !os.IsNotExist(err) {
					markUnknown(dir)
					break
				}
				if isRoot {
					break
				}
				dir = filepath.ToSlash(filepath.Dir(filepath.FromSlash(dir)))
				continue // already gone: the real climb goes on
			}
			if !fi.IsDir() {
				break
			}
			empty, readable := p.endsEmpty(dir)
			if !readable {
				markUnknown(dir)
				break
			}
			if !empty {
				break
			}
			if isRoot && !p.reported[dir] {
				p.reported[dir] = true
				roots = append(roots, dir)
			}
			dir = filepath.ToSlash(filepath.Dir(filepath.FromSlash(dir)))
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		di, dj := strings.Count(roots[i], "/"), strings.Count(roots[j], "/")
		if di != dj {
			return di > dj
		}
		return roots[i] < roots[j]
	})
	sort.Strings(unknown)
	return roots, unknown
}

// endsEmpty reports whether dir (slash path under the base dir) holds only files
// the run deletes and directories that end empty; readable is false when a
// directory could not be read (the answer is then unknown). It stops at the first
// entry that survives (empty false, readable true): nothing after it can change the
// answer, so an unreadable sibling later cannot turn it unknown, and a tree under a
// surviving file is never walked. Never follows a link.
func (p *RootPredictor) endsEmpty(dir string) (empty, readable bool) {
	if m, ok := p.memo[dir]; ok {
		return m.empty, m.readable
	}
	empty, readable = p.walkEmpty(dir)
	p.memo[dir] = emptiness{empty, readable}
	return empty, readable
}

func (p *RootPredictor) walkEmpty(dir string) (empty, readable bool) {
	entries, err := readDirFn(filepath.Join(p.baseDir, filepath.FromSlash(dir)))
	if err != nil {
		return false, false
	}
	for _, e := range entries {
		rel := dir + "/" + e.Name()
		if p.gone[rel] {
			continue
		}
		if e.Type()&os.ModeSymlink == 0 && e.IsDir() && p.above[rel] {
			sub, ok := p.endsEmpty(rel)
			if !ok {
				return false, false
			}
			if sub {
				continue
			}
		}
		return false, true
	}
	return true, true
}

// prunedFilesOf lists the paths a prune of managed deletes, by name: every managed
// file, and, when whole is set (the prunes that strip shared files: disconnect,
// unbind, plugin removal), the shared files they edit that the removal deletes once
// nothing else is in them: an instructions file, an MCP config, the registration
// file of a hook (the ledger's Claude settings, Codex's hooks.json, ...). A shared
// file the user also keeps entries in stays on disk in the real run, so a dry run
// that counts it can list a root the real run keeps; the output says "predicted".
func prunedFilesOf(baseDir, provider string, managed []ManagedFile, whole, configFiles bool) []string {
	var out []string
	add := func(rel string) { out = append(out, filepath.ToSlash(filepath.Clean(rel))) }
	hookProviders := map[string]bool{}
	for _, mf := range managed {
		if !whole && (mf.Kind == "instructions" || mf.Kind == "mcp") {
			continue
		}
		add(mf.Path)
		if mf.Kind == "hook" {
			hookProviders[hookProviderFromPath(mf.Path)] = true
		}
	}
	if !whole {
		return out
	}
	for hp := range hookProviders {
		switch hp {
		case "claude":
			for _, f := range configurator.CreatedFiles(baseDir) {
				if strings.HasPrefix(f, ".claude/") {
					add(f)
				}
			}
		case "codex":
			add(".codex/hooks.json")
		case "kiro":
			add(strings.Join(kiroHooksRelPath, "/"))
		case "antigravity":
			add(antigravityGlobalHooksRel)
			add(antigravityProjectHooksRel)
		}
	}
	if configFiles {
		if d, ok := configurator.Lookup(configurator.Provider(provider)); ok && d.MCPConfigPath != "" {
			// Only a file that is there: a run that deletes nothing leaves the directory.
			if MCPConfigFileExists(baseDir, configurator.Provider(provider)) && configurator.MayDeleteEmpty(d, baseDir, d.MCPConfigPath) {
				add(d.MCPConfigPath)
			}
		}
	}
	return out
}

// wouldPruneRoots is the prediction for a pass that deletes only managed files
// (the dry run of Apply): shared files are never counted, so it can list fewer
// roots than the real run, never more.
func wouldPruneRoots(baseDir string, pruned []ManagedFile) (roots, unknown []string) {
	return NewRootPredictor(baseDir).Predict(prunedFilesOf(baseDir, "", pruned, false, false))
}
