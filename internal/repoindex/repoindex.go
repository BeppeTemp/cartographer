// Package repoindex resolves `{{repo:<key>}}` placeholders (D75) to a local
// clone path: it scans a set of search roots for git repositories, reads
// each one's `origin` remote from `.git/config` (no `git` exec — the file is
// parsed directly, see readOriginURL), and normalizes the remote URL to a
// canonical "host/owner/name" key that is stable across every machine on the
// team. Results are cached at CachePath so repeated resolutions don't re-walk
// the filesystem; a cache miss triggers a fresh Scan.
package repoindex

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// userHomeDir is indirected so tests can stub it out, mirroring
// internal/agents and internal/service.
var userHomeDir = os.UserHomeDir

// DefaultDepth and MaxDepth bound how many directory levels Scan descends from
// each root (D162). The old fixed cap of 4 made a workspace organised as
// <root>/<program>/<area>/<repo> invisible — ~160 repositories unreachable in one
// deployment, so every {{repo:<name>}} citing one was unusable — and the failure
// message never mentioned a depth, so the limit had to be guessed.
//
// The default stays 4: raising it for everyone would slow every resolution to
// accommodate one layout. MaxDepth exists because Scan walks the filesystem on
// every unresolved placeholder, and an unbounded depth on a large home directory
// is a multi-second stall in the middle of a sync.
const (
	DefaultDepth = 4
	MaxDepth     = 8
)

// heavyDirs are non-hidden directories Scan never descends into: they are
// large, never contain a repo of interest themselves, and walking them would
// make Scan slow for no benefit.
var heavyDirs = map[string]bool{
	"node_modules": true,
	"vendor":       true,
}

// RemoteKey is a canonical, machine-independent remote identifier of the
// form "host/owner/name" (or "host/group/subgroup/name" for nested
// providers like GitLab) — see NormalizeRemote.
type RemoteKey string

// ShortName returns the trailing path segment of the key, e.g. "name" for
// "github.com/owner/name" — the form a user types as `{{repo:name}}`.
func (k RemoteKey) ShortName() string {
	parts := strings.Split(string(k), "/")
	return parts[len(parts)-1]
}

// Index is the result of a Scan: every discovered repo's canonical remote
// key mapped to the local clone path(s) that carry it, in the order their
// root was scanned (first entry wins on ambiguity, see lookupIndex). It is
// the on-disk shape of CachePath's JSON cache.
type Index struct {
	Roots []string               `json:"roots"`
	Repos map[RemoteKey][]string `json:"repos"`
}

// CachePath returns ~/.config/cartographer/repos.json, the on-disk cache
// written by Scan and read by Resolve.
func CachePath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("repoindex: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "cartographer", "repos.json"), nil
}

// LoadCache reads the cache written by a previous Scan. Returns
// (nil, os.ErrNotExist) if no cache file exists yet.
func LoadCache() (*Index, error) {
	path, err := CachePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("repoindex: read %s: %w", path, err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("repoindex: parse %s: %w", path, err)
	}
	if idx.Repos == nil {
		idx.Repos = map[RemoteKey][]string{}
	}
	return &idx, nil
}

// SaveCache writes idx to CachePath, creating the parent directory if
// necessary.
func SaveCache(idx *Index) error {
	path, err := CachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("repoindex: mkdir %s: %w", filepath.Dir(path), err)
	}
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("repoindex: marshal cache: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("repoindex: write %s: %w", path, err)
	}
	return nil
}

// Scan walks roots (each expanded for a leading "~", see ExpandHome) up to
// depthCap levels deep, skipping hidden directories and heavyDirs. Every
// directory containing a .git entry is treated as a repo root: its origin
// remote (if any) is read and normalized, and the directory recorded under
// that canonical key. Scan does not descend into a repo's own working tree
// once found. A directory with no readable/parseable origin remote is
// silently skipped — not every clone has one, and that is not a scan error.
//
// A configured root that does not exist, or that cannot be read, is reported as
// a warning naming the root and the OS error, and the remaining roots are still
// scanned. A warning and not an error on purpose: a machine-local config may
// legitimately list a root that only exists on another machine, and failing the
// whole sync for it would be worse than the silence this replaces — while the
// silence itself was the actual defect, because the only message the user ever
// saw was Resolve's, which talks about directory depth.
func Scan(roots []string, maxDepth int) (*Index, []string, error) {
	idx := &Index{Roots: roots, Repos: map[RemoteKey][]string{}}
	var warnings []string
	for _, root := range roots {
		expanded := ExpandHome(root)
		if err := checkSearchRoot(expanded); err != nil {
			// %s, not %q: Go's quoted form escapes every separator, so a Windows
			// root is printed back at the operator as C:\\Users\\… — a path they
			// never wrote and cannot paste.
			warnings = append(warnings, fmt.Sprintf("repoindex: search root %s is not usable: %v", root, err))
			continue
		}
		walkDir(expanded, 0, EffectiveDepth(maxDepth), idx)
	}
	return idx, warnings, nil
}

// checkSearchRoot reports why a configured search root cannot be walked: it does
// not exist, is not a directory, or its entries cannot be listed. Only the root
// is checked this way — walkDir keeps discarding the error of a directory it
// meets *inside* the walk, where an unreadable subdirectory is ordinary and
// naming every one of them would drown the one message that matters.
func checkSearchRoot(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("not a directory")
	}
	if _, err := os.ReadDir(dir); err != nil {
		return err
	}
	return nil
}

// EffectiveDepth normalises a configured depth: zero or negative means the
// default, and a value above MaxDepth is clamped rather than rejected — a config
// value that stops a sync is worse than one adjusted loudly, and the caller
// reports the clamp.
func EffectiveDepth(configured int) int {
	switch {
	case configured <= 0:
		return DefaultDepth
	case configured > MaxDepth:
		return MaxDepth
	default:
		return configured
	}
}

// isLiveClone reports whether path currently holds a git clone: a directory
// containing a .git entry, checked with the stat that follows symlinks (so a
// symlinked clone keeps working). It defines "is a clone" in one place, used
// both by walkDir when discovering repos during a Scan and by lookupIndex
// when validating a cached path before serving it (D181) — a cache hit must
// not paper over a clone that moved or was removed.
func isLiveClone(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || !fi.IsDir() {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, ".git")); err != nil {
		return false
	}
	return true
}

func walkDir(dir string, depth, maxDepth int, idx *Index) {
	if depth > maxDepth {
		return
	}
	if isLiveClone(dir) {
		if key, ok := readOriginRemote(dir); ok {
			idx.Repos[key] = append(idx.Repos[key], dir)
		}
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") || heavyDirs[name] {
			continue
		}
		walkDir(filepath.Join(dir, name), depth+1, maxDepth, idx)
	}
}

// gitConfigSectionRe matches a `.git/config` INI section header line.
var gitConfigSectionRe = regexp.MustCompile(`^\[([^\s\]]+)(?:\s+"([^"]*)")?\]$`)

// readOriginRemote reads repoDir/.git/config and returns the normalized key
// of its `[remote "origin"]` url, if present and parseable.
func readOriginRemote(repoDir string) (RemoteKey, bool) {
	data, err := os.ReadFile(filepath.Join(repoDir, ".git", "config"))
	if err != nil {
		return "", false
	}
	inOrigin := false
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if m := gitConfigSectionRe.FindStringSubmatch(trimmed); m != nil {
			inOrigin = strings.EqualFold(m[1], "remote") && m[2] == "origin"
			continue
		}
		if !inOrigin {
			continue
		}
		if key, val, ok := strings.Cut(trimmed, "="); ok && strings.TrimSpace(key) == "url" {
			remoteKey, err := NormalizeRemote(strings.TrimSpace(val))
			if err != nil {
				return "", false
			}
			return remoteKey, true
		}
	}
	return "", false
}

// scpLikeRe matches the scp-like ssh remote shorthand, e.g.
// "git@github.com:owner/name.git" or "host:owner/name" — no explicit scheme,
// a host, a literal ':', then a path.
var scpLikeRe = regexp.MustCompile(`^(?:[^@/\s]+@)?([^:/\s]+):(.+)$`)

// NormalizeRemote parses a git remote URL in any of its common forms (ssh
// scp-like, ssh://, https://, http://, git://) and returns the canonical
// "host/owner/name" key: lowercased host, trailing ".git" stripped, no
// leading/trailing slashes.
func NormalizeRemote(raw string) (RemoteKey, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("repoindex: empty remote url")
	}

	var host, path string
	switch {
	case strings.Contains(s, "://"):
		u, err := url.Parse(s)
		if err != nil {
			return "", fmt.Errorf("repoindex: parse remote %q: %w", raw, err)
		}
		host, path = u.Hostname(), u.Path
	case scpLikeRe.MatchString(s):
		m := scpLikeRe.FindStringSubmatch(s)
		host, path = m[1], m[2]
	default:
		return "", fmt.Errorf("repoindex: unrecognized remote url %q", raw)
	}

	host = strings.ToLower(host)
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	if host == "" || path == "" {
		return "", fmt.Errorf("repoindex: cannot normalize remote %q", raw)
	}
	return RemoteKey(host + "/" + path), nil
}

// errNotIndexed is an internal sentinel: key has no match in idx, distinct
// from an ambiguity error (which Resolve must propagate immediately rather
// than paper over with a rescan).
var errNotIndexed = errors.New("repoindex: not indexed")

// lookupIndex resolves key against idx alone: key containing "/" is treated
// as a full canonical RemoteKey, otherwise as a short name matched against
// every indexed key's ShortName(). A candidate whose every local path has
// moved or vanished is dropped before ambiguity or emptiness is evaluated
// (D181): a cached entry pointing at a dead clone behaves as a miss, not as
// an answer, so Resolve's caller falls through to a rescan. Among the
// candidates with at least one live clone, multiple distinct repos matching
// a short name is an ambiguity error (spec: ask for the full form), and
// multiple live local clones of the same repo resolve to the first
// root-order match, with a warning.
func lookupIndex(idx *Index, key string) (string, []string, error) {
	var candidates []RemoteKey
	if strings.Contains(key, "/") {
		if _, ok := idx.Repos[RemoteKey(key)]; ok {
			candidates = []RemoteKey{RemoteKey(key)}
		}
	} else {
		for k := range idx.Repos {
			if k.ShortName() == key {
				candidates = append(candidates, k)
			}
		}
	}

	livePaths := map[RemoteKey][]string{}
	var survivors []RemoteKey
	for _, c := range candidates {
		var paths []string
		for _, p := range idx.Repos[c] {
			if isLiveClone(p) {
				paths = append(paths, p)
			}
		}
		if len(paths) > 0 {
			livePaths[c] = paths
			survivors = append(survivors, c)
		}
	}

	if len(survivors) == 0 {
		return "", nil, errNotIndexed
	}
	if len(survivors) > 1 {
		sort.Slice(survivors, func(i, j int) bool { return survivors[i] < survivors[j] })
		names := make([]string, len(survivors))
		for i, c := range survivors {
			names[i] = string(c)
		}
		return "", nil, fmt.Errorf("repoindex: %q is ambiguous between %s — use the full host/owner/name form", key, strings.Join(names, ", "))
	}

	paths := livePaths[survivors[0]]
	var warnings []string
	if len(paths) > 1 {
		warnings = append(warnings, fmt.Sprintf("repoindex: multiple local clones of %s, using %s", survivors[0], paths[0]))
	}
	return paths[0], warnings, nil
}

// Resolve resolves key (a full "host/owner/name" or short "name" repo
// reference) to a local path: manualPaths (the clientconfig `paths:`
// override map) wins first, then the on-disk cache, then a fresh Scan of
// roots on a cache miss (refreshing the cache for next time). Returns an
// error — including any ambiguity error from lookupIndex — if key cannot be
// resolved.
//
// It is a single lookup through a fresh Resolver; a caller resolving many
// keys in one pass holds one Resolver instead, so the walk runs once (D262).
func Resolve(key string, manualPaths map[string]string, roots []string, maxDepth int) (string, []string, error) {
	return NewResolver(manualPaths, roots, maxDepth).Resolve(key)
}

// scanFunc is Scan, as a variable so a test can count the walks a Resolver
// performs.
var scanFunc = Scan

// Resolver resolves repo keys against one configuration and performs at most
// one Scan over its lifetime (D262). The first key that misses both the
// manual paths and the on-disk cache scans the roots and refreshes the cache;
// every later miss is answered from that fresh index and fails without
// walking again. Before this, one sync with twenty unresolved keys walked the
// search roots twenty times — and a key the fresh index does not hold is not
// going to appear by walking the same tree a second later.
//
// A Resolver is not safe for concurrent use.
type Resolver struct {
	manualPaths map[string]string
	roots       []string
	maxDepth    int

	cacheLoaded bool
	cache       *Index // nil when there is no usable cache for these roots

	scanned      bool
	index        *Index
	rootWarnings []string
	scanErr      error
}

// NewResolver returns a Resolver over manualPaths (the `paths:` map), the
// search roots and the configured depth.
func NewResolver(manualPaths map[string]string, roots []string, maxDepth int) *Resolver {
	return &Resolver{manualPaths: manualPaths, roots: roots, maxDepth: maxDepth}
}

// Resolve resolves one key; see the package-level Resolve for the order.
// Warnings about unusable search roots are returned once, by the call that
// scanned, rather than repeated on every later miss.
func (r *Resolver) Resolve(key string) (string, []string, error) {
	if p, ok := r.manualPaths[key]; ok {
		return ExpandHome(p), nil, nil
	}

	if !r.scanned {
		if !r.cacheLoaded {
			r.cacheLoaded = true
			if idx, err := LoadCache(); err == nil && rootsMatch(idx.Roots, r.roots) {
				r.cache = idx
			}
		}
		if r.cache != nil {
			path, warnings, lookupErr := lookupIndex(r.cache, key)
			if lookupErr == nil {
				return path, warnings, nil
			}
			if !errors.Is(lookupErr, errNotIndexed) {
				return "", nil, lookupErr
			}
		}
	}

	var warnings []string
	if !r.scanned {
		r.scanned = true
		r.index, r.rootWarnings, r.scanErr = scanFunc(r.roots, r.maxDepth)
		if r.scanErr == nil {
			_ = SaveCache(r.index) // best-effort: resolution proceeds even if the cache can't be persisted
		}
		// A bad root is surfaced even when the resolution failed: it is
		// usually the reason it failed, and the error below can only talk
		// about depth.
		warnings = append(warnings, r.rootWarnings...)
	}
	if r.scanErr != nil {
		return "", warnings, r.scanErr
	}

	path, lookupWarnings, err := lookupIndex(r.index, key)
	warnings = append(warnings, lookupWarnings...)
	if err != nil {
		if errors.Is(err, errNotIndexed) {
			return "", warnings, fmt.Errorf("repoindex: repo %q not found within %d directory levels of search roots %v — raise search_depth (max %d) or add a closer root in .cartographer.yaml", key, EffectiveDepth(r.maxDepth), r.roots, MaxDepth)
		}
		return "", warnings, err
	}
	return path, warnings, nil
}

// rootsMatch reports whether the cached search roots are still the ones
// Resolve was called with, once each element is normalized with ExpandHome
// so that "~/Documents" and its expanded form compare equal (D181). The
// comparison is ordered: root order is what decides the winner among
// multiple live clones in lookupIndex, so a reorder is a semantic change and
// must invalidate the cache exactly like an addition or removal. A cache
// written before Roots was compared this way carries an empty slice, which
// differs from any nonempty configured roots — costing exactly one rescan on
// first use after upgrade.
func rootsMatch(cached, configured []string) bool {
	if len(cached) != len(configured) {
		return false
	}
	for i := range cached {
		if ExpandHome(cached[i]) != ExpandHome(configured[i]) {
			return false
		}
	}
	return true
}

// ExpandHome expands a leading "~" — alone, or followed by either separator —
// to the user's home directory. A path without that prefix is returned
// unchanged, "~name" included: expanding another user's home is not something a
// .cartographer.yaml entry means.
//
// `~\x` is accepted on every platform, not only Windows, so the two spellings
// agree wherever the config is read; a literal directory named `~\x` on unix is
// pathological. What follows the separator is left in the spelling it was
// written in — a search_roots or paths entry names this machine's filesystem, so
// there is nothing to translate, and a wrong one is now reported by name (Scan).
//
// It is exported because internal/provisioning needs exactly this function and
// already imports this package (D75 WP3): one implementation, not two that drift.
func ExpandHome(p string) string {
	if p == "~" {
		home, err := userHomeDir()
		if err != nil {
			return p
		}
		return home
	}
	if len(p) < 2 || p[0] != '~' || (p[1] != '/' && p[1] != '\\') {
		return p
	}
	home, err := userHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[2:])
}
