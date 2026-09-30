package kb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/execbit"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// AssetMaxFileSize is the largest file still worth versioning in the KB's git
// history (D270). asset_write refuses a larger file and asset_read will not
// return one; a larger file that reached the KB through git is still listed
// (Oversized) and can still be deleted, so it never blocks the owner.
const AssetMaxFileSize = 10 * 1024 * 1024 // 10 MiB

// AssetEntry describes one non-Markdown regular file owned by an expanded concept.
type AssetEntry struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable"`
	Oversized  bool   `json:"oversized,omitempty"`
}

func assetHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// assetFileHash streams the file, so hashing an oversized asset to list or
// delete it does not load it whole.
func assetFileHash(abs string) (string, error) {
	f, err := os.Open(abs)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func errAssetTooLarge(rel string, size int64) error {
	return fmt.Errorf("%w: asset %s is %d bytes, exceeds the %d MiB cap — too large to version in git; keep it outside the KB and cite it by link", okf.ErrInvalidPath, rel, size, AssetMaxFileSize>>20)
}

// resolveAsset verifies that id is an existing expanded data concept and that
// assetPath is a safe, non-Markdown path below its directory.
func (kb *KB) resolveAsset(id okf.ConceptID, assetPath string, writeMode bool) (string, string, error) {
	if len(strings.Split(string(id), "/")) != 2 || isServicesID(id) {
		return "", "", fmt.Errorf("%w: assets require an expanded data concept (map/concept)", okf.ErrInvalidPath)
	}
	conceptRel, expanded, err := kb.resolveConceptRelPath(id, writeMode)
	if err != nil {
		return "", "", err
	}
	if !expanded || conceptRel != path.Join(string(id), "index.md") {
		conceptAbs, resolveErr := kb.ResolvePath(conceptRel, false)
		if resolveErr == nil {
			if _, statErr := os.Stat(conceptAbs); os.IsNotExist(statErr) {
				return "", "", fmt.Errorf("%w: concept %s", okf.ErrNotFound, id)
			}
		}
		return "", "", fmt.Errorf("%w: concept %s must be expanded first with concept_expand", okf.ErrInvalidPath, id)
	}
	conceptDir := filepath.Dir(conceptRel)
	conceptAbs, err := kb.ResolvePath(conceptDir, false)
	if err != nil {
		return "", "", err
	}
	if info, err := os.Lstat(conceptAbs); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		if os.IsNotExist(err) {
			return "", "", fmt.Errorf("%w: concept %s", okf.ErrNotFound, id)
		}
		return "", "", fmt.Errorf("%w: expanded concept directory %s is not a directory", okf.ErrInvalidPath, id)
	}

	clean, parts, err := validateAssetRelativePath(assetPath, true)
	if err != nil {
		return "", "", err
	}

	// ResolvePath keeps the lexical guard anchored at data/. Lstat every
	// existing segment so a repository-controlled symlink cannot bypass it.
	rel := path.Join(conceptDir, clean)
	abs, err := kb.ResolvePath(rel, writeMode)
	if err != nil {
		return "", "", err
	}
	current := conceptAbs
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if os.IsNotExist(statErr) {
			break
		}
		if statErr != nil {
			return "", "", fmt.Errorf("asset path %s: %w", assetPath, statErr)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", "", fmt.Errorf("%w: asset path contains symlink %s", okf.ErrInvalidPath, assetPath)
		}
	}
	return rel, abs, nil
}

// validateAssetRelativePath applies the lexical ownership rules to both a
// direct API path and every path discovered by ListAssets. Markdown files are
// skipped by listings rather than treated as assets, so callers can opt out of
// that final extension check while retaining all traversal/hidden guards.
func validateAssetRelativePath(assetPath string, rejectMarkdown bool) (string, []string, error) {
	if assetPath == "" || filepath.IsAbs(assetPath) {
		return "", nil, fmt.Errorf("%w: asset path must be relative", okf.ErrInvalidPath)
	}
	clean := filepath.Clean(assetPath)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(os.PathSeparator)) {
		return "", nil, fmt.Errorf("%w: asset path escapes concept directory: %s", okf.ErrInvalidPath, assetPath)
	}
	parts := strings.Split(filepath.ToSlash(clean), "/")
	for _, part := range parts {
		if part == "" || strings.HasPrefix(part, ".") {
			return "", nil, fmt.Errorf("%w: hidden asset path segment %q", okf.ErrInvalidPath, part)
		}
	}
	if rejectMarkdown && strings.EqualFold(filepath.Ext(clean), ".md") {
		return "", nil, fmt.Errorf("%w: Markdown assets are not allowed; use concept_write", okf.ErrInvalidPath)
	}
	return clean, parts, nil
}

// checkAssetFile requires a regular file; enforceCap also refuses one above
// AssetMaxFileSize (reads), which a delete must not do.
func checkAssetFile(abs, rel string, enforceCap bool) (os.FileInfo, error) {
	info, err := os.Lstat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", okf.ErrNotFound, rel)
		}
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: asset %s is not a regular file", okf.ErrInvalidPath, rel)
	}
	if enforceCap && info.Size() > AssetMaxFileSize {
		return nil, errAssetTooLarge(rel, info.Size())
	}
	return info, nil
}

// ReadAsset returns the raw bytes and metadata of an owned asset.
func (kb *KB) ReadAsset(id okf.ConceptID, assetPath string) ([]byte, AssetEntry, error) {
	rel, abs, err := kb.resolveAsset(id, assetPath, false)
	if err != nil {
		return nil, AssetEntry{}, err
	}
	info, err := checkAssetFile(abs, rel, true)
	if err != nil {
		return nil, AssetEntry{}, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, AssetEntry{}, fmt.Errorf("ReadAsset %s: %w", assetPath, err)
	}
	return data, AssetEntry{Path: filepath.ToSlash(assetPath), Size: info.Size(), SHA256: assetHash(data), Executable: execbit.IsExecutable(info.Mode())}, nil
}

// WriteAsset creates or overwrites an owned asset using the raw-byte sha256
// as its optimistic-concurrency token.
func (kb *KB) WriteAsset(id okf.ConceptID, assetPath string, data []byte, ifMatch string, executable *bool) (AssetEntry, error) {
	if len(data) > AssetMaxFileSize {
		return AssetEntry{}, errAssetTooLarge(assetPath, int64(len(data)))
	}
	rel, abs, err := kb.resolveAsset(id, assetPath, true)
	if err != nil {
		return AssetEntry{}, err
	}
	info, statErr := os.Lstat(abs)
	exists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return AssetEntry{}, fmt.Errorf("WriteAsset %s: %w", assetPath, statErr)
	}
	if exists && !info.Mode().IsRegular() {
		return AssetEntry{}, fmt.Errorf("%w: asset %s is not a regular file", okf.ErrInvalidPath, assetPath)
	}
	if exists {
		current, readErr := assetFileHash(abs)
		if readErr != nil {
			return AssetEntry{}, fmt.Errorf("WriteAsset %s: %w", assetPath, readErr)
		}
		if ifMatch == "" {
			return AssetEntry{}, fmt.Errorf("already_exists: %s already exists (sha256 %s) — pass if_match to overwrite", assetPath, current)
		}
		if ifMatch != current {
			return AssetEntry{}, fmt.Errorf("%w: %s content-hash mismatch", okf.ErrStaleWrite, assetPath)
		}
	} else if ifMatch != "" {
		return AssetEntry{}, fmt.Errorf("%w: if_match must be omitted when creating %s", okf.ErrStaleWrite, assetPath)
	}
	if err := kb.WriteFileAtomic(rel, data); err != nil {
		return AssetEntry{}, err
	}
	mode := os.FileMode(0o644)
	if exists {
		mode = info.Mode()
	}
	if executable != nil {
		if *executable {
			mode = 0o755
		} else {
			mode = 0o644
		}
	}
	if err := os.Chmod(abs, mode); err != nil {
		return AssetEntry{}, fmt.Errorf("WriteAsset %s: chmod: %w", assetPath, err)
	}
	return AssetEntry{Path: filepath.ToSlash(assetPath), Size: int64(len(data)), SHA256: assetHash(data), Executable: execbit.IsExecutable(mode)}, nil
}

// ListAssets returns all regular, non-Markdown files below an expanded concept.
// Hidden files and directories (.DS_Store, .gitkeep) are not assets and are
// skipped; a file above AssetMaxFileSize is listed with Oversized set. Files
// arrive through git as well as asset_write, so neither may fail the listing:
// lint, concept_delete and concept_collapse all depend on it (D270).
func (kb *KB) ListAssets(id okf.ConceptID) ([]AssetEntry, error) {
	_, conceptAbs, err := kb.resolveAsset(id, "asset", false)
	if err != nil {
		return nil, err
	}
	conceptAbs = filepath.Dir(conceptAbs)
	var entries []AssetEntry
	err = filepath.WalkDir(conceptAbs, func(abs string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if abs == conceptAbs {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: asset path contains symlink %s", okf.ErrInvalidPath, abs)
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%w: asset %s is not a regular file", okf.ErrInvalidPath, abs)
		}
		rel, err := filepath.Rel(conceptAbs, abs)
		if err != nil {
			return err
		}
		rel, _, err = validateAssetRelativePath(rel, false)
		if err != nil {
			return err
		}
		if strings.EqualFold(filepath.Ext(rel), ".md") || okf.IsReserved(filepath.Base(rel)) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		sum, err := assetFileHash(abs)
		if err != nil {
			return err
		}
		entries = append(entries, AssetEntry{Path: filepath.ToSlash(rel), Size: info.Size(), SHA256: sum, Executable: execbit.IsExecutable(info.Mode()), Oversized: info.Size() > AssetMaxFileSize})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ListAssets %s: %w", id, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// DeleteAsset deletes an asset after a raw-byte sha256 concurrency check and
// removes only now-empty directories below the expanded concept directory.
func (kb *KB) DeleteAsset(id okf.ConceptID, assetPath, ifMatch string) error {
	rel, abs, err := kb.resolveAsset(id, assetPath, true)
	if err != nil {
		return err
	}
	if ifMatch == "" {
		return fmt.Errorf("%w: if_match is required for asset_delete", okf.ErrStaleWrite)
	}
	if _, err := checkAssetFile(abs, rel, false); err != nil {
		return err
	}
	sum, err := assetFileHash(abs)
	if err != nil {
		return fmt.Errorf("DeleteAsset %s: %w", assetPath, err)
	}
	if ifMatch != sum {
		return fmt.Errorf("%w: %s content-hash mismatch", okf.ErrStaleWrite, assetPath)
	}
	if err := os.Remove(abs); err != nil {
		return fmt.Errorf("DeleteAsset %s: %w", assetPath, err)
	}
	conceptDir, err := kb.ResolvePath(string(id), false)
	if err != nil {
		return err
	}
	for dir := filepath.Dir(abs); dir != conceptDir; dir = filepath.Dir(dir) {
		if err := os.Remove(dir); err != nil {
			if os.IsNotExist(err) {
				break
			}
			entries, readErr := os.ReadDir(dir)
			if readErr == nil && len(entries) > 0 {
				break
			}
			return fmt.Errorf("DeleteAsset %s: prune: %w", assetPath, err)
		}
	}
	return nil
}

// DeleteConceptWithAssets preserves the existing non-recursive concept-delete
// contract while making asset loss explicit. Satellite Markdown concepts are
// never removed; force only acknowledges deletion of non-Markdown assets.
func (kb *KB) DeleteConceptWithAssets(id okf.ConceptID, force bool) ([]AssetEntry, error) {
	_, expanded, err := kb.resolveConceptRelPath(id, true)
	if err != nil {
		return nil, err
	}
	if !expanded {
		return nil, kb.DeleteConcept(id)
	}
	assets, err := kb.ListAssets(id)
	if err != nil {
		return nil, err
	}
	if len(assets) > 0 && !force {
		paths := make([]string, 0, len(assets))
		for i, asset := range assets {
			if i == 10 {
				paths = append(paths, "...")
				break
			}
			paths = append(paths, asset.Path)
		}
		return assets, fmt.Errorf("%w: expanded concept %s owns %d asset(s): %s — pass force=true to delete them", okf.ErrInvalidPath, id, len(assets), strings.Join(paths, ", "))
	}
	for _, asset := range assets {
		if err := kb.DeleteAsset(id, asset.Path, asset.SHA256); err != nil {
			return nil, err
		}
	}
	return assets, kb.DeleteConcept(id)
}

// Searchable asset text (D277). An expanded concept's text assets are indexed
// into the owner's search document, so a value that lives only in a CSV, a
// config or a script is findable. Binary formats are out of scope: extracting
// them needs a dependency, and the ingestion procedure has the agent write
// their content into the concept instead.

// AssetIndexMaxBytes is how much of one asset is indexed; the rest is ignored.
const AssetIndexMaxBytes = 256 * 1024

// indexableAssetExts are the extensions whose content is searchable text.
var indexableAssetExts = map[string]bool{
	".txt": true, ".csv": true, ".tsv": true, ".json": true, ".yaml": true,
	".yml": true, ".toml": true, ".ini": true, ".conf": true, ".cfg": true,
	".sh": true, ".ps1": true, ".py": true, ".go": true, ".sql": true,
	".xml": true, ".log": true,
}

// assetStat is an indexable asset as a stat sees it: no content read.
type assetStat struct {
	path    string // relative to the owner directory, slash-separated
	size    int64
	modTime int64 // nanoseconds
}

// ownerAssetStats stats the indexable assets below ownerAbs (an expanded
// concept directory): known extension, not Oversized, hidden entries and
// symlinks skipped. It reads no content and hashes nothing, so it is cheap
// enough to run on every reconcile; it lists each directory once and stats
// only candidate files (a WalkDir with Info() per file measured several times
// slower on macOS). racy reports a modification time within racyWindow of
// now, the graph cache's rule (D241): such a file may be rewritten inside one
// timestamp tick without changing its signature.
func ownerAssetStats(ownerAbs string) (stats []assetStat, racy bool, err error) {
	stats, racy, _, err = scanOwner(ownerAbs, time.Now())
	return stats, racy, err
}

// scanOwner is ownerAssetStats that also reports whether the directory holds
// an index.md at its top level, which is what makes a concept directory an
// expanded concept (AssetSignatures learns it from the same listing).
func scanOwner(ownerAbs string, now time.Time) (stats []assetStat, racy, hasIndex bool, err error) {
	var scan func(dirAbs, relPrefix string, top bool) error
	scan = func(dirAbs, relPrefix string, top bool) error {
		entries, err := os.ReadDir(dirAbs)
		if err != nil {
			return err
		}
		for _, e := range entries {
			name := e.Name()
			if top && name == "index.md" {
				hasIndex = true
			}
			if strings.HasPrefix(name, ".") {
				continue
			}
			if e.IsDir() {
				if err := scan(filepath.Join(dirAbs, name), relPrefix+name+"/", false); err != nil {
					return err
				}
				continue
			}
			if !e.Type().IsRegular() || !indexableAssetExts[strings.ToLower(filepath.Ext(name))] {
				continue
			}
			info, err := os.Lstat(filepath.Join(dirAbs, name))
			if err != nil || info.Size() > AssetMaxFileSize {
				continue // vanished since the listing, or oversized (D270)
			}
			if info.ModTime().After(now.Add(-racyWindow)) {
				racy = true
			}
			stats = append(stats, assetStat{path: relPrefix + name, size: info.Size(), modTime: info.ModTime().UnixNano()})
		}
		return nil
	}
	err = scan(ownerAbs, "", true)
	sort.Slice(stats, func(i, j int) bool { return stats[i].path < stats[j].path })
	return stats, racy, hasIndex, err
}

// assetSignature renders stats as the string two reconciliations compare. A
// racy signature ends in "~", so it never equals the next one: the owner is
// re-read once more after the window has passed, as the graph cache does.
func assetSignature(stats []assetStat, racy bool) string {
	if len(stats) == 0 {
		return ""
	}
	var b strings.Builder
	for _, s := range stats {
		fmt.Fprintf(&b, "%s\t%d\t%d\n", s.path, s.size, s.modTime)
	}
	if racy {
		b.WriteString("~")
	}
	return b.String()
}

// AssetSignatures returns, for every expanded data concept that owns at least
// one indexable asset, the signature of those assets (path, size, mtime). It
// reads no content: it lists the two directory levels where concepts live and
// stats each candidate file. An id absent from the map has no indexable assets.
func (kb *KB) AssetSignatures() (map[okf.ConceptID]string, error) {
	dataRoot, err := kb.ResolvePath(".", false)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	sigs := map[okf.ConceptID]string{}
	maps, err := os.ReadDir(dataRoot)
	if err != nil {
		return nil, fmt.Errorf("AssetSignatures: %w", err)
	}
	for _, m := range maps {
		if !m.IsDir() || strings.HasPrefix(m.Name(), ".") {
			continue
		}
		mapAbs := filepath.Join(dataRoot, m.Name())
		concepts, err := os.ReadDir(mapAbs)
		if err != nil {
			continue // a broken map must not blind every other one
		}
		direct := make(map[string]bool, len(concepts))
		for _, c := range concepts {
			if !c.IsDir() {
				direct[c.Name()] = true
			}
		}
		for _, c := range concepts {
			id := okf.ConceptID(m.Name() + "/" + c.Name())
			// The direct form map/id.md wins over an expanded directory
			// (resolveConceptRelPath), which then owns no assets.
			if !c.IsDir() || strings.HasPrefix(c.Name(), ".") || direct[c.Name()+".md"] || isServicesID(id) {
				continue
			}
			stats, racy, hasIndex, err := scanOwner(filepath.Join(mapAbs, c.Name()), now)
			if err != nil || !hasIndex {
				continue
			}
			if sig := assetSignature(stats, racy); sig != "" {
				sigs[id] = sig
			}
		}
	}
	return sigs, nil
}

// IndexableAssetText returns the searchable text of id's assets and their
// signature (the one AssetSignatures reports). The text is the concatenation,
// in path order, of "<path>\n<text>\n\n", so the path is searchable too. An
// asset counts only if its extension is a text one, it is not Oversized, and
// the first AssetIndexMaxBytes are valid UTF-8 (a cut inside a rune is
// forgiven); anything else is skipped, never an error. An id that is not an
// expanded data concept yields an error, as ListAssets does.
func (kb *KB) IndexableAssetText(id okf.ConceptID) (text string, sig string, err error) {
	_, anyAbs, err := kb.resolveAsset(id, "asset", false)
	if err != nil {
		return "", "", err
	}
	ownerAbs := filepath.Dir(anyAbs)
	stats, racy, err := ownerAssetStats(ownerAbs)
	if err != nil {
		return "", "", fmt.Errorf("IndexableAssetText %s: %w", id, err)
	}
	var b strings.Builder
	for _, s := range stats {
		data, ok := readAssetText(filepath.Join(ownerAbs, filepath.FromSlash(s.path)))
		if !ok {
			continue
		}
		b.WriteString(s.path)
		b.WriteByte('\n')
		b.WriteString(data)
		b.WriteString("\n\n")
	}
	return b.String(), assetSignature(stats, racy), nil
}

// readAssetText reads up to AssetIndexMaxBytes of abs and reports whether it
// is indexable text: valid UTF-8 without NUL bytes.
func readAssetText(abs string) (string, bool) {
	f, err := os.Open(abs)
	if err != nil {
		return "", false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, AssetIndexMaxBytes+1))
	if err != nil {
		return "", false
	}
	if len(data) > AssetIndexMaxBytes {
		data = data[:AssetIndexMaxBytes]
		// The cut may fall inside a multi-byte rune: drop the partial tail.
		for i := 0; i < utf8.UTFMax-1 && len(data) > 0 && !utf8.Valid(data); i++ {
			data = data[:len(data)-1]
		}
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", false
	}
	return string(data), true
}
