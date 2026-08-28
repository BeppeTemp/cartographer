package provisioning

// On-disk verification of managed artifacts (D139). Drift detection used to
// compare the manifest against the lockfile only: once the lockfile said an
// artifact was applied, its files were never looked at again, so a skill
// edited by hand stayed edited forever and a deleted file was never restored
// — while `cartographer status` reported in-sync. This is the mechanical half
// of what D138's provenance stamp promises editorially.
//
// Existence and content are verified separately, because they need different
// evidence: a recorded hash is required to tell whether bytes changed, but not
// to tell whether a path is still there. Collapsing the two behind one gate is
// what let entries predating D138 keep reporting in-sync after their files had
// been deleted.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// Drift reasons reported by VerifyManaged.
const (
	// DriftMissing: the managed path is gone from disk.
	DriftMissing = "missing"
	// DriftModified: the content on disk no longer hashes to what was written.
	DriftModified = "modified"
	// DriftUnregistered: a managed key or marker block inside a file shared
	// with the user (mcp, instructions) is no longer there.
	DriftUnregistered = "unregistered"
	// DriftUnknown: the artifact is on disk but no materialized hash was
	// recorded — a lockfile written before D138 — so its content cannot be
	// compared. Reported but never healed: treating it as drift would rewrite
	// every artifact on every client at once on the first upgrade. Scoped to
	// content: whether the path exists is checked without any hash, so a
	// pre-D138 entry that vanished is DriftMissing, not unknown.
	DriftUnknown = "unknown"
	// DriftError: the artifact could not be verified (permissions, a path
	// that became a directory). Reported, never fatal: one unreadable
	// artifact must not abort the verification of the others.
	DriftError = "error"
)

// DriftFinding is one artifact whose on-disk state no longer matches the
// lockfile.
type DriftFinding struct {
	Kind   string
	Name   string
	Path   string // the managed path the finding is about, relative to base dir
	Reason string
	Detail string // free text for DriftError, empty otherwise
}

// Healable reports whether this finding is one Apply restores. DriftUnknown is
// not, by design (see the constant).
func (f DriftFinding) Healable() bool {
	return f.Reason == DriftMissing || f.Reason == DriftModified || f.Reason == DriftUnregistered
}

// VerifyManaged checks every artifact in lock against the filesystem under
// baseDir and returns one finding per diverging artifact (never one per file:
// a skill's several managed paths share one hash and one finding).
//
// Per-kind scope, decided by what is actually verifiable:
//   - skill, hook — a dedicated directory Cartographer owns: the whole
//     directory is re-hashed, so an extra file left inside counts as modified;
//   - agent — a single file: its bytes are hashed;
//   - mcp, instructions — a key or marker block inside a file shared with the
//     user: presence only. Content comparison would be meaningless there and
//     the surrounding file is never rewritten on a content difference.
//
// Nothing outside lock.Managed is ever read.
// RepairManagedHashes re-records the content hash of every managed entry that has
// none, computing it from the bytes already on disk (D157).
//
// The gap it closes is narrow: entries written before materialized hashes existed
// are DriftUnknown, deliberately not healable, and ComputeDiff sees no change
// either — so `sync` leaves them alone and the only remedy on offer was
// `reconnect`, which prunes and rewrites **every** managed artifact on every
// client. In the field that was ~150 file operations to backfill six hashes, with
// a partial failure leaving both clients without skills.
//
// Backfilling is **not healing**: it records what is on disk as the baseline, and
// does not claim the file matches the server. An adopted entry is marked with
// AdoptedAt so a later genuine drift is still detectable from the next
// server-side change onward, and so an operator can tell a verified entry from an
// adopted one.
//
// Entries whose file is missing are left alone: that is DriftMissing, a real
// finding for `sync` to fix, not something to paper over.
func RepairManagedHashes(lock Lock, provider configurator.Provider, baseDir string, dryRun bool) (repaired []ManagedFile, skipped []DriftFinding, err error) {
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range lock.Managed {
		mf := lock.Managed[i]
		// MaterializedHash is the field verifyArtifact needs for the content
		// comparison; ContentHash is what ComputeDiff compares against the
		// manifest and is not this function's business.
		if mf.MaterializedHash != "" {
			continue
		}
		rel, full, ok := managedDest(mf, provider, baseDir)
		if !ok {
			// The provider does not support this kind: nothing to hash, and not
			// an error.
			continue
		}
		if isSymlink(full) {
			skipped = append(skipped, DriftFinding{Kind: mf.Kind, Name: mf.Name, Path: rel, Reason: "destination is a symlink"})
			continue
		}
		files, readErr := readManagedFiles(mf, full)
		switch {
		case errors.Is(readErr, fs.ErrNotExist):
			skipped = append(skipped, DriftFinding{Kind: mf.Kind, Name: mf.Name, Path: rel, Reason: DriftMissing})
			continue
		case readErr != nil:
			skipped = append(skipped, DriftFinding{Kind: mf.Kind, Name: mf.Name, Path: rel, Reason: readErr.Error()})
			continue
		}
		// Hashed exactly the way Apply records materializedHash, over the same
		// ordered set of files: a value computed any other way would never match
		// a future Apply, turning an unverifiable entry into a permanently
		// drifted one — worse than the gap.
		lock.Managed[i].MaterializedHash = hashArtifactFiles(files)
		lock.Managed[i].AdoptedAt = now
		repaired = append(repaired, lock.Managed[i])
	}
	if dryRun {
		return repaired, skipped, nil
	}
	return repaired, skipped, nil
}

// readManagedFiles reads the on-disk bytes of one managed artifact in the shape
// hashArtifactFiles expects: one entry for a single-file kind, every file under
// the directory for a skill or hook.
func readManagedFiles(mf ManagedFile, full string) ([]ArtifactFile, error) {
	info, err := os.Lstat(full)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		data, readErr := os.ReadFile(full)
		if readErr != nil {
			return nil, readErr
		}
		return []ArtifactFile{{Path: filepath.Base(full), Content: data, Executable: info.Mode()&0o111 != 0}}, nil
	}
	var files []ArtifactFile
	walkErr := filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(full, p)
		if relErr != nil {
			return relErr
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		fi, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		files = append(files, ArtifactFile{Path: filepath.ToSlash(rel), Content: data, Executable: fi.Mode()&0o111 != 0})
		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}
	return files, nil
}

func VerifyManaged(lock Lock, provider configurator.Provider, baseDir string) []DriftFinding {
	var findings []DriftFinding
	seen := make(map[string]bool, len(lock.Managed))

	for _, mf := range lock.Managed {
		key := mf.Kind + "\x00" + mf.Name
		if seen[key] {
			continue
		}
		seen[key] = true
		if f, ok := verifyArtifact(mf, provider, baseDir); ok {
			findings = append(findings, f)
		}
	}
	return findings
}

func verifyArtifact(mf ManagedFile, provider configurator.Provider, baseDir string) (DriftFinding, bool) {
	finding := DriftFinding{Kind: mf.Kind, Name: mf.Name, Path: mf.Path}

	switch mf.Kind {
	case "mcp", "instructions":
		present, err := managedEntryPresent(mf, provider, baseDir)
		switch {
		case err != nil:
			finding.Reason, finding.Detail = DriftError, err.Error()
		case !present:
			finding.Reason = DriftUnregistered
		default:
			return DriftFinding{}, false
		}
		return finding, true
	}

	destRel, full, ok := managedDest(mf, provider, baseDir)
	if !ok {
		return DriftFinding{}, false
	}
	finding.Path = destRel

	// Existence before content. Whether the path is on disk needs no recorded
	// hash, so it is checked ahead of the MaterializedHash gate below: an
	// artifact that is gone is missing whatever the lockfile knows about its
	// bytes. Stat also comes first because the walk helper wraps a missing
	// directory in a formatted error, and "gone" must not be reported as
	// "unreadable".
	if _, statErr := os.Stat(full); statErr != nil {
		if os.IsNotExist(statErr) {
			finding.Reason = DriftMissing
		} else {
			finding.Reason, finding.Detail = DriftError, statErr.Error()
		}
		return finding, true
	}

	// Only the content comparison needs a hash: a pre-D138 entry whose files
	// are present stays unknown and is never rewritten.
	if mf.MaterializedHash == "" {
		finding.Reason = DriftUnknown
		return finding, true
	}

	var onDisk string
	var err error
	switch mf.Kind {
	case "agent":
		var data []byte
		data, err = os.ReadFile(full)
		if err == nil {
			onDisk = hashArtifactFiles([]ArtifactFile{{Path: mf.Name + ".md", Content: data}})
		}
	default:
		// skill/hook: a directory of its own.
		onDisk, err = contentHashDirOS(full, mf.Kind)
	}

	switch {
	case os.IsNotExist(err):
		finding.Reason = DriftMissing
		return finding, true
	case err != nil:
		finding.Reason, finding.Detail = DriftError, err.Error()
		return finding, true
	case onDisk != mf.MaterializedHash:
		finding.Reason = DriftModified
		return finding, true
	}
	return DriftFinding{}, false
}

// managedDest resolves where an artifact of this kind lives on disk, returning
// the path relative to baseDir and the absolute one. ok is false when the
// artifact has no destination for this provider: an unsupported kind is not
// drift, it simply does not concern it.
func managedDest(mf ManagedFile, provider configurator.Provider, baseDir string) (rel, full string, ok bool) {
	rel = mf.Path
	if mf.Kind != "agent" {
		// skill/hook: a directory of its own.
		if rel = destDir(mf.Kind, mf.Name, provider); rel == "" {
			return "", "", false
		}
	}
	return rel, filepath.Join(baseDir, rel), true
}

// managedEntryPresent reports whether the managed key (mcp) or marker block
// (instructions) is still in the shared file it was merged into.
func managedEntryPresent(mf ManagedFile, provider configurator.Provider, baseDir string) (bool, error) {
	full := filepath.Join(baseDir, mf.Path)
	data, err := os.ReadFile(full)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", mf.Path, err)
	}

	if mf.Kind == "instructions" {
		content := string(data)
		return strings.Contains(content, instructionsBlockBeginPrefix) && strings.Contains(content, instructionsBlockEnd), nil
	}

	// mcp: a TOML managed block, or a key in the provider's server map.
	if filepath.Ext(full) == ".toml" {
		begin, end := mcpServerMarkers(mf.Name)
		content := string(data)
		return strings.Contains(content, begin) && strings.Contains(content, end), nil
	}
	settings, err := loadJSONObject(full)
	if err != nil {
		return false, fmt.Errorf("parse %s: %w", mf.Path, err)
	}
	servers, ok := settings[mcpServerKey(provider)].(map[string]interface{})
	if !ok {
		return false, nil
	}
	_, present := servers[mf.Name]
	return present, nil
}
