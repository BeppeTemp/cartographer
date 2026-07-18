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

// depthCap bounds how many directory levels Scan descends from each root.
const depthCap = 4

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

// Scan walks roots (each expanded for a leading "~", see expandHome) up to
// depthCap levels deep, skipping hidden directories and heavyDirs. Every
// directory containing a .git entry is treated as a repo root: its origin
// remote (if any) is read and normalized, and the directory recorded under
// that canonical key. Scan does not descend into a repo's own working tree
// once found. A directory with no readable/parseable origin remote is
// silently skipped — not every clone has one, and that is not a scan error.
func Scan(roots []string) (*Index, error) {
	idx := &Index{Roots: roots, Repos: map[RemoteKey][]string{}}
	for _, root := range roots {
		walkDir(expandHome(root), 0, idx)
	}
	return idx, nil
}

func walkDir(dir string, depth int, idx *Index) {
	if depth > depthCap {
		return
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
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
		walkDir(filepath.Join(dir, name), depth+1, idx)
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
// every indexed key's ShortName(). Multiple distinct repos matching a short
// name is an ambiguity error (spec: ask for the full form). Multiple local
// clones of the same repo resolve to the first root-order match, with a
// warning.
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
	if len(candidates) == 0 {
		return "", nil, errNotIndexed
	}
	if len(candidates) > 1 {
		sort.Slice(candidates, func(i, j int) bool { return candidates[i] < candidates[j] })
		names := make([]string, len(candidates))
		for i, c := range candidates {
			names[i] = string(c)
		}
		return "", nil, fmt.Errorf("repoindex: %q is ambiguous between %s — use the full host/owner/name form", key, strings.Join(names, ", "))
	}

	paths := idx.Repos[candidates[0]]
	if len(paths) == 0 {
		return "", nil, errNotIndexed
	}
	var warnings []string
	if len(paths) > 1 {
		warnings = append(warnings, fmt.Sprintf("repoindex: multiple local clones of %s, using %s", candidates[0], paths[0]))
	}
	return paths[0], warnings, nil
}

// Resolve resolves key (a full "host/owner/name" or short "name" repo
// reference) to a local path: manualPaths (the clientconfig `paths:`
// override map) wins first, then the on-disk cache, then a fresh Scan of
// roots on a cache miss (refreshing the cache for next time). Returns an
// error — including any ambiguity error from lookupIndex — if key cannot be
// resolved.
func Resolve(key string, manualPaths map[string]string, roots []string) (string, []string, error) {
	if p, ok := manualPaths[key]; ok {
		return expandHome(p), nil, nil
	}

	if idx, err := LoadCache(); err == nil {
		path, warnings, lookupErr := lookupIndex(idx, key)
		if lookupErr == nil {
			return path, warnings, nil
		}
		if !errors.Is(lookupErr, errNotIndexed) {
			return "", nil, lookupErr
		}
	}

	idx, err := Scan(roots)
	if err != nil {
		return "", nil, err
	}
	_ = SaveCache(idx) // best-effort: resolution proceeds even if the cache can't be persisted

	path, warnings, err := lookupIndex(idx, key)
	if err != nil {
		if errors.Is(err, errNotIndexed) {
			return "", nil, fmt.Errorf("repoindex: repo %q not found under search roots %v", key, roots)
		}
		return "", nil, err
	}
	return path, warnings, nil
}

// expandHome expands a leading "~" (alone or as "~/...") to the user's home
// directory. Paths without a leading "~" are returned unchanged.
func expandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p
	}
	home, err := userHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}
