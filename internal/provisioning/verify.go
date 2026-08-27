package provisioning

// On-disk verification of managed artifacts (D139). Drift detection used to
// compare the manifest against the lockfile only: once the lockfile said an
// artifact was applied, its files were never looked at again, so a skill
// edited by hand stayed edited forever and a deleted file was never restored
// — while `cartographer status` reported in-sync. This is the mechanical half
// of what D138's provenance stamp promises editorially.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	// DriftUnknown: no materialized hash was recorded — a lockfile written
	// before D138. Reported but never healed: treating it as drift would
	// rewrite every artifact on every client at once on the first upgrade.
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

	if mf.MaterializedHash == "" {
		finding.Reason = DriftUnknown
		return finding, true
	}

	var onDisk string
	var err error
	switch mf.Kind {
	case "agent":
		full := filepath.Join(baseDir, mf.Path)
		var data []byte
		data, err = os.ReadFile(full)
		if err == nil {
			onDisk = hashArtifactFiles([]ArtifactFile{{Path: mf.Name + ".md", Content: data}})
		}
	default:
		// skill/hook: a directory of its own.
		destRel := destDir(mf.Kind, mf.Name, provider)
		if destRel == "" {
			return DriftFinding{}, false
		}
		finding.Path = destRel
		full := filepath.Join(baseDir, destRel)
		// Stat first: the walk helper wraps a missing directory in a formatted
		// error, and "gone" must not be reported as "unreadable".
		if _, statErr := os.Stat(full); statErr != nil {
			if os.IsNotExist(statErr) {
				finding.Reason = DriftMissing
			} else {
				finding.Reason, finding.Detail = DriftError, statErr.Error()
			}
			return finding, true
		}
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
