package kb

import (
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
