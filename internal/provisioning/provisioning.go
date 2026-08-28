// Package provisioning manages the synchronization of artifacts (skill, agent,
// hook — D48; instructions — D56; mcp, third-party MCP servers from KBs — D69)
// between the Cartographer server and the LLM client (CLI or MCP tool).
//
// Three objects drive the mechanism:
//   - Manifest: server-side source of truth (bundle + KB).
//   - Lock: client-side applied state (written in base-dir).
//   - Diff: difference between Manifest and Lock, basis for Add/Update/Prune.
//
// The model is extensible to new Kinds by adding a new emitter in
// BuildManifest and a case in destDir — Manifest, Lock and diff logic don't change.
package provisioning

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/artifactsig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/skill"
)

// Artifact describes a single provisioning artifact.
// Kind makes it extensible to "hook", "agent" etc. without changing the flow.
type Artifact struct {
	Kind        string `json:"kind"` // "skill" (extensible)
	Name        string `json:"name"`
	Source      string `json:"source"` // "bundle" | "kb:<name>"
	Version     string `json:"version,omitempty"`
	ContentHash string `json:"content_hash"` // sha256 hex of the whole skill folder
	// Signed is true only after a cryptographic verification. It is never an
	// authorization or policy decision.
	Signed    bool                   `json:"signed"`
	BuiltIn   bool                   `json:"built_in,omitempty"`
	Signature *artifactsig.Signature `json:"signature,omitempty"`

	// Files, if non-empty, holds the artifact's content already in memory
	// (e.g. received via the sync_pull MCP tool on a remote HTTP client, with
	// no filesystem shared with the server). When present, Apply materializes
	// from here instead of walking BundleFS/KBRoots. Not part of the JSON
	// manifest exposed by sync_check/sync_apply/sync_pull (that uses dedicated
	// structures).
	Files []ArtifactFile `json:"-"`
}

// BuildOptions controls manifest construction. Signers is keyed by mounted KB
// name; a missing signer leaves that KB's artifacts unsigned.
type BuildOptions struct {
	Signers       map[string]ed25519.PrivateKey
	MCPAllowlists map[string][]MCPAllowlistEntry
	// ToolPrefixes is the effective MCP tool-name prefix (D102) of each
	// mounted KB, keyed by KB name. A missing key or an empty value means
	// unprefixed. It is what the generated instructions block names the tools
	// with (D144), so a prefixed KB does not imprint tools the agent cannot
	// call.
	ToolPrefixes map[string]string
	// MCPDiagnostic receives non-fatal denied/stale allow-list diagnostics.
	// It never contains descriptor headers or environment references.
	MCPDiagnostic func(string)
}

// ArtifactFile is a single file of an Artifact, with content in memory.
type ArtifactFile struct {
	Path       string // path relative to the artifact's (skill) folder
	Content    []byte
	Executable bool
}

// Manifest is the server-side source of truth.
// Revision changes if even a single artifact changes: O(1) drift detection.
// GeneratedAt must be set by the caller if needed; BuildManifest leaves it empty
// to guarantee determinism in tests.
type Manifest struct {
	Revision    string     `json:"revision"`
	GeneratedAt string     `json:"generated_at,omitempty"`
	Artifacts   []Artifact `json:"artifacts"`
}

// ManagedFile records a single file materialized by provisioning in the client's base-dir.
// Prune uses this list to remove only what Cartographer created.
type ManagedFile struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Path        string `json:"path"`         // relative to base-dir
	ContentHash string `json:"content_hash"` // hash of the manifest artifact this came from
	// MaterializedHash is the hash of what was actually written to disk:
	// after placeholder expansion, provenance stamping and any per-provider
	// translation (D138). ContentHash is what ComputeDiff compares against the
	// manifest, so the two must not be conflated — before D138 the expanded
	// hash was stored in ContentHash, and any artifact carrying a placeholder
	// compared unequal on every sync, reporting permanent drift.
	// Empty means "unknown": lockfiles written before D138, and entries with
	// no materialized file of their own (kind "mcp", dry runs).
	MaterializedHash string `json:"materialized_hash,omitempty"`
	// AdoptedAt marks an entry whose MaterializedHash was backfilled from the
	// bytes on disk by `doctor --repair-hashes` (D157) rather than recorded when
	// the file was written. It is *adopted*, not *verified*: nothing was compared
	// against the server, so drift is detectable only from the next server-side
	// change onward. RFC3339, empty for a normally-recorded entry.
	AdoptedAt string `json:"adopted_at,omitempty"`
}

// Lock is the client's lockfile: applied revision + managed files.
// Written to <base-dir>/.cartographer-sync.lock.json.
type Lock struct {
	AppliedRevision string        `json:"applied_revision"`
	Provider        string        `json:"provider"`
	Managed         []ManagedFile `json:"managed"`
	// BaseDir is the directory the Path of every ManagedFile above is
	// relative to, recorded only for a provider that materializes outside the
	// shared client base dir (D141, hermes under $HERMES_HOME). Empty — every
	// lockfile written before D141, and every other provider — means "the
	// lockfile's own directory", so nothing migrates. Read it through
	// LockBaseDir, never directly: pruning or verifying against the wrong base
	// dir deletes the wrong files or silently finds nothing.
	BaseDir string `json:"base_dir,omitempty"`
	// ServerVersion is the version of the Cartographer server this state was
	// materialized against (D142). Empty means "unknown" — every lockfile
	// written before D142, and any sync that could not reach /health — and
	// never triggers the "the server changed" report: the first sync after an
	// upgrade must not tell every user something it cannot actually know.
	ServerVersion string `json:"server_version,omitempty"`
}

// LockBaseDir returns the directory lock's managed paths are relative to:
// lock.BaseDir when the provider has its own root (D141), defaultDir — the
// directory holding the lockfile — otherwise.
func LockBaseDir(lock Lock, defaultDir string) string {
	if lock.BaseDir != "" {
		return lock.BaseDir
	}
	return defaultDir
}

// BaseDirFor returns the directory provider materializes its artifacts under,
// given the shared client base dir. It is the registry descriptor's
// ResolveBaseDir (D141): every provider but hermes returns defaultDir
// unchanged, and hermes fails naming $HERMES_HOME when it is unset.
func BaseDirFor(provider configurator.Provider, defaultDir string) (string, error) {
	d, ok := configurator.Lookup(provider)
	if !ok {
		return defaultDir, nil
	}
	return d.ResolveBaseDir(defaultDir)
}

// Diff is the result of comparing Manifest ↔ Lock.
type Diff struct {
	Added   []Artifact
	Updated []Artifact
	Removed []ManagedFile
	InSync  bool
}

// ApplyOptions collects the parameters for Apply.
type ApplyOptions struct {
	BundleFS fs.FS                 // FS with the bundled skills (paths: "bundled/<name>/…")
	KBRoots  map[string]string     // KB name → absolute path on disk
	Provider configurator.Provider // destination provider
	BaseDir  string                // base directory where artifacts are materialized
	DryRun   bool                  // if true, writes nothing
	// AutoTrust explicitly authorizes eligible unsigned KB artifacts. It does
	// not alter Artifact.Signed.
	AutoTrust bool
	// ApprovedMCP holds exact local approvals keyed as "kb:<source>\x00<name>".
	// It is authorization, not verification; Signed is never mutated by it.
	ApprovedMCP map[string]string
	Lock        Lock // current lockfile

	// SkipLockWrite, if true, computes AppliedResult.NewLock but does not persist
	// it to <BaseDir>/LockFileName. Used by the multi-provider clients
	// (cartographer connect/sync) that write a v2 LockFile (see
	// ReadLockFile/WriteLockFile) with the Locks of several providers in the
	// same file.
	SkipLockWrite bool

	// ExpandPlaceholders, SearchRoots and Paths drive the client-side expansion
	// of {{repo:<key>}}/{{path:<name>}} (D75 WP3) at materialization time. Set
	// ONLY by client callers (cmd/cartographer); internal/mcpserver never sets
	// them — the MCP server never expands anything (D75): only the client knows
	// its own local filesystem.
	ExpandPlaceholders bool
	SearchRoots        []string
	Paths              map[string]string

	// NoHeal reports on-disk divergence (AppliedResult.Divergent) instead of
	// restoring it (D139). The escape hatch for someone deliberately
	// iterating on a materialized copy; off by default, because the default
	// must match what the D138 provenance stamp promises the reader.
	NoHeal bool
}

// AppliedResult is the result of Apply.
type AppliedResult struct {
	NewLock       Lock
	Written       []ManagedFile
	Pruned        []ManagedFile
	NeedsApproval []Artifact
	// Unsupported: artifacts whose kind has no known destination for the
	// provider (destDir == ""). Distinct from NeedsApproval: no approval
	// unblocks them — the provider simply doesn't support them.
	Unsupported []Artifact
	// Refused: artifacts not materialized because their destination, or a
	// directory component of it, is a symlink (D148). Distinct from
	// Unsupported and NeedsApproval: the provider supports the kind and the
	// artifact is authorized, but writing would land outside the client — see
	// safepath.go. They are not recorded in the lockfile, so the next sync
	// retries and reports again, which is right for a condition only the
	// operator can clear.
	Refused []Artifact
	// Healed: artifacts restored because their files on disk had diverged
	// from what the lockfile recorded (D139). Reported separately from
	// Written: a restore means someone's local change was discarded, which
	// must be visible rather than folded into "3 updated".
	Healed []ManagedFile
	// Divergent: on-disk divergence detected but NOT restored, because
	// opts.NoHeal was set.
	Divergent []DriftFinding
	// Warnings: non-fatal messages about individual aspects of materialization
	// that stay partial without failing Apply — today the only case is an
	// OpenCode hook whose KB event has no equivalent in OpenCode's plugin
	// engine (D59, see registerOpenCodePlugin in hooksettings.go): the hook's
	// files are materialized anyway, but no plugin is generated.
	Warnings []string
}

// LockFileName is the lockfile's name, relative to base-dir.
const LockFileName = ".cartographer-sync.lock.json"

// --- Hash ---

// ContentHashDir computes a deterministic sha256 hex of the entire dir folder
// inside fsys. Walks files in alphabetical order of relative path, including
// relative path and each file's bytes in the hash.
//
// Use this for Artifact.ContentHash: a skill is a folder, not just SKILL.md.
func ContentHashDir(fsys fs.FS, dir string) (string, error) {
	return contentHashDir(fsys, dir, nil)
}

func contentHashDir(fsys fs.FS, dir string, executable func(string, bool) bool) (string, error) {
	var files []ArtifactFile
	var paths []string

	err := fs.WalkDir(fsys, dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("provisioning: walk %s: %w", dir, err)
	}

	sort.Strings(paths)

	for _, p := range paths {
		// Compute the path relative to the root dir.
		rel := p
		prefix := dir + "/"
		if strings.HasPrefix(p, prefix) {
			rel = p[len(prefix):]
		}
		// Case dir == ".": WalkDir returns paths without "./" prefix, rel = path.

		data, readErr := fs.ReadFile(fsys, p)
		if readErr != nil {
			return "", fmt.Errorf("provisioning: read %s: %w", p, readErr)
		}
		info, statErr := fs.Stat(fsys, p)
		if statErr != nil {
			return "", fmt.Errorf("provisioning: stat %s: %w", p, statErr)
		}
		exec := info.Mode()&0o111 != 0
		if executable != nil {
			exec = executable(rel, exec)
		}
		files = append(files, ArtifactFile{Path: rel, Content: data, Executable: exec})
	}
	return hashArtifactFiles(files), nil
}

func effectiveExecutable(kind, path string, executable bool) bool {
	if kind != "hook" {
		return executable
	}
	return filepath.Base(path) != "hook.json"
}

// ContentHashDirOS computes the hash of a folder on the real filesystem.
// Reuses ContentHashDir via os.DirFS to avoid code duplication.
func ContentHashDirOS(dirPath string) (string, error) {
	return ContentHashDir(os.DirFS(dirPath), ".")
}

// ContentHashFiles computes the canonical artifact hash for transport-received
// files. It is the counterpart of ContentHashDir and includes paths and modes.
func ContentHashFiles(files []ArtifactFile) string { return hashArtifactFiles(files) }

// VerifyArtifactSignature verifies a KB artifact against the pinned key matching
// its signature key ID. A missing signature is deliberately unsigned, not an error.
func VerifyArtifactSignature(a Artifact, keys map[string]ed25519.PublicKey) error {
	if a.Signature == nil {
		return nil
	}
	if !strings.HasPrefix(a.Source, "kb:") {
		return fmt.Errorf("provisioning: signature on non-KB artifact %s/%s", a.Kind, a.Name)
	}
	key, ok := keys[a.Signature.KeyID]
	if !ok {
		return fmt.Errorf("provisioning: unknown signing key for KB %q (key ID %s)", strings.TrimPrefix(a.Source, "kb:"), a.Signature.KeyID)
	}
	if err := artifactsig.Verify(key, artifactsig.Identity{Source: a.Source, Kind: a.Kind, Name: a.Name, Version: a.Version, ContentHash: a.ContentHash}, a.Signature); err != nil {
		return fmt.Errorf("provisioning: verify %s/%s: %w", a.Kind, a.Name, err)
	}
	return nil
}

// VerifiedManifest returns a copy whose Signed flags are cryptographic
// verification results only. Server-provided Signed values are never trusted.
func VerifiedManifest(m Manifest, pins map[string][]ed25519.PublicKey) (Manifest, error) {
	artifacts := append([]Artifact(nil), m.Artifacts...)
	for i := range artifacts {
		a := &artifacts[i]
		a.Signed = false
		a.BuiltIn = a.Source == "bundle"
		if a.Signature == nil {
			continue
		}
		kbName := strings.TrimPrefix(a.Source, "kb:")
		if !strings.HasPrefix(a.Source, "kb:") {
			return Manifest{}, fmt.Errorf("provisioning: signature source mismatch for %s/%s", a.Kind, a.Name)
		}
		keys := make(map[string]ed25519.PublicKey, len(pins[kbName]))
		for _, key := range pins[kbName] {
			id := artifactsig.KeyID(key)
			if _, exists := keys[id]; exists {
				return Manifest{}, fmt.Errorf("provisioning: duplicate signing key ID for KB %q", kbName)
			}
			keys[id] = key
		}
		if err := VerifyArtifactSignature(*a, keys); err != nil {
			return Manifest{}, err
		}
		a.Signed = true
	}
	return Manifest{Revision: m.Revision, GeneratedAt: m.GeneratedAt, Artifacts: artifacts}, nil
}

// ContentHashDirOSForKind is contentHashDirOS exported for callers that must
// reproduce a materialized artifact's hash — the client's own status check
// and its tests (D139).
func ContentHashDirOSForKind(dirPath, kind string) (string, error) {
	return contentHashDirOS(dirPath, kind)
}

func contentHashDirOS(dirPath, kind string) (string, error) {
	return contentHashDir(os.DirFS(dirPath), ".", func(path string, executable bool) bool {
		return effectiveExecutable(kind, path, executable)
	})
}

// --- Manifest ---

// BuildManifest collects the artifacts from the bundle and the given KBs,
// sorts the list and computes the deterministic aggregate revision.
//
// bundleFS: FS with the bundled skills (paths "bundled/<name>/…"). Can be nil.
// kbRoots: KB name → absolute path map. KBs with no skills/ folder are skipped.
// KB artifacts are signed when BuildOptions has a signer for their mounted KB.
// Bundled artifacts are trusted by construction and carry BuiltIn instead of a
// forged Ed25519 signature.
func BuildManifest(bundleFS fs.FS, kbRoots map[string]string, opts BuildOptions) (Manifest, error) {
	var artifacts []Artifact

	// 1. Skill dal bundle.
	if bundleFS != nil {
		entries, err := fs.ReadDir(bundleFS, "bundled")
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return Manifest{}, fmt.Errorf("provisioning: read bundle: %w", err)
		}

		// Load metadata (version) via the skill loader.
		bundledSkills, _ := skill.LoadAllFromFS(bundleFS, "bundled")
		versionByName := make(map[string]string, len(bundledSkills))
		for _, s := range bundledSkills {
			versionByName[s.Name] = s.Version
		}

		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			hash, err := ContentHashDir(bundleFS, "bundled/"+name)
			if err != nil {
				return Manifest{}, fmt.Errorf("provisioning: hash bundle/%s: %w", name, err)
			}
			artifacts = append(artifacts, Artifact{
				Kind:        "skill",
				Name:        name,
				Source:      "bundle",
				Version:     versionByName[name],
				ContentHash: hash,
				BuiltIn:     true,
			})
		}
	}

	// 2-4. Skill/agent/hook from the KBs. Sort names for determinism.
	kbNames := make([]string, 0, len(kbRoots))
	for name := range kbRoots {
		kbNames = append(kbNames, name)
	}
	sort.Strings(kbNames)

	for _, kbName := range kbNames {
		kbRoot := kbRoots[kbName]
		matchedMCPAllowlist := make(map[string]bool)

		// 2. Skill (skills/<name>/SKILL.md, several files per artifact). A KB with
		// no skills/ folder or an unreadable directory: kbSkills is empty,
		// silent skip (no fatal error).
		kbSkills, _ := skill.LoadAllSkills(kbRoot)
		for _, s := range kbSkills {
			skillDir := filepath.Join(kbRoot, s.DirPath)
			hash, err := contentHashDirOS(skillDir, "skill")
			if err != nil {
				return Manifest{}, fmt.Errorf("provisioning: hash kb:%s/%s: %w", kbName, s.Name, err)
			}
			artifacts = append(artifacts, Artifact{
				Kind:        "skill",
				Name:        s.Name,
				Source:      "kb:" + kbName,
				Version:     s.Version,
				ContentHash: hash,
			})
		}

		// 3. Agent (agents/<name>.md, single-file Claude subagent). No agents/
		// folder → zero agent artifacts, no error (backward compat with
		// pre-D48 KBs, see kb.Init).
		agentsDir := filepath.Join(kbRoot, "agents")
		agentEntries, err := os.ReadDir(agentsDir)
		if err == nil {
			for _, e := range agentEntries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
					continue
				}
				name := strings.TrimSuffix(e.Name(), ".md")
				hash, err := contentHashFile(filepath.Join(agentsDir, e.Name()))
				if err != nil {
					return Manifest{}, fmt.Errorf("provisioning: hash kb:%s/agents/%s: %w", kbName, name, err)
				}
				artifacts = append(artifacts, Artifact{
					Kind:        "agent",
					Name:        name,
					Source:      "kb:" + kbName,
					ContentHash: hash,
				})
			}
		}

		// 4. Hook (hooks/<name>/, script + hook.json — aggregate hash like
		// multi-file skills). No hooks/ folder → zero hook artifacts. A KB
		// that named a hook BootstrapHookName ("cartographer-bootstrap", D60) is
		// not filtered out here: BuildManifest stays a pure scan, with no notion
		// of reservation — the collision check (warning, artifact ignored) lives
		// entirely in Apply, the only place that materializes.
		hooksDir := filepath.Join(kbRoot, "hooks")
		hookEntries, err := os.ReadDir(hooksDir)
		if err == nil {
			for _, e := range hookEntries {
				if !e.IsDir() {
					continue
				}
				name := e.Name()
				hash, err := contentHashDirOS(filepath.Join(hooksDir, name), "hook")
				if err != nil {
					return Manifest{}, fmt.Errorf("provisioning: hash kb:%s/hooks/%s: %w", kbName, name, err)
				}
				artifacts = append(artifacts, Artifact{
					Kind:        "hook",
					Name:        name,
					Source:      "kb:" + kbName,
					ContentHash: hash,
				})
			}
		}

		// 5. Third-party MCP servers (mcp/<name>.json, D69): one JSON file per
		// server, single-file like agents. No mcp/ folder → zero artifacts
		// (backward compat). Parse+validation here (parseMCPServerSpec): a
		// malformed file, or one with a headers/env value that looks like a
		// literal secret, fails BuildManifest, not Apply (WP2). An unsigned MCP
		// descriptor needs its own hash-bound approval: generic AutoTrust never
		// authorizes it. A D114 signature remains independently verifiable.
		mcpDir := filepath.Join(kbRoot, "mcp")
		mcpEntries, err := os.ReadDir(mcpDir)
		if err == nil {
			for _, e := range mcpEntries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
					continue
				}
				name := strings.TrimSuffix(e.Name(), ".json")
				mcpPath := filepath.Join(mcpDir, e.Name())
				data, readErr := os.ReadFile(mcpPath)
				if readErr != nil {
					return Manifest{}, fmt.Errorf("provisioning: read kb:%s/mcp/%s: %w", kbName, name, readErr)
				}
				spec, specErr := parseMCPServerSpec(name, data)
				if specErr != nil {
					return Manifest{}, fmt.Errorf("provisioning: %w", specErr)
				}
				if !MCPAllowed(opts.MCPAllowlists[kbName], name, spec) {
					if opts.MCPDiagnostic != nil {
						opts.MCPDiagnostic(fmt.Sprintf("KB %q omits MCP artifact %q: add mcp_allowlist entry with transport http and target %q", kbName, name, normalizedMCPURL(spec.URL)))
					}
					continue
				}
				matchedMCPAllowlist[name] = true
				hash, hashErr := contentHashFile(mcpPath)
				if hashErr != nil {
					return Manifest{}, fmt.Errorf("provisioning: hash kb:%s/mcp/%s: %w", kbName, name, hashErr)
				}
				artifacts = append(artifacts, Artifact{
					Kind:        "mcp",
					Name:        name,
					Source:      "kb:" + kbName,
					ContentHash: hash,
					Signed:      false,
				})
			}
		}
		for _, entry := range opts.MCPAllowlists[kbName] {
			if !matchedMCPAllowlist[entry.Name] && opts.MCPDiagnostic != nil {
				opts.MCPDiagnostic(fmt.Sprintf("KB %q MCP allow-list entry %q has no matching descriptor", kbName, entry.Name))
			}
		}

		// 6. Instructions (kind "instructions", D56): an "imprinting" artifact
		// per KB, always present (unlike skill/agent/hook it doesn't depend on
		// an optional folder) — tells the LLM agent that this KB exists and how
		// to use it. Unlike other kinds, the content doesn't live on disk: it's
		// GENERATED by generateKBInstructions (a pure, deterministic function, no
		// timestamp) and placed directly in Artifact.Files, so ReadArtifactFiles
		// doesn't need to read anything from the KB for this kind (see its
		// "len(a.Files) > 0" check at the top of the function).
		instrContent := []byte(generateKBInstructions(kbName, kbRoot, opts.ToolPrefixes[kbName]))
		artifacts = append(artifacts, Artifact{
			Kind:        "instructions",
			Name:        kbName,
			Source:      "kb:" + kbName,
			ContentHash: hashArtifactFiles([]ArtifactFile{{Path: "instructions.md", Content: instrContent}}),
			Files:       []ArtifactFile{{Path: "instructions.md", Content: instrContent}},
		})
	}

	for i := range artifacts {
		a := &artifacts[i]
		if !strings.HasPrefix(a.Source, "kb:") {
			continue
		}
		kbName := strings.TrimPrefix(a.Source, "kb:")
		key := opts.Signers[kbName]
		if len(key) == 0 {
			continue
		}
		sig, err := artifactsig.Sign(key, artifactsig.Identity{Source: a.Source, Kind: a.Kind, Name: a.Name, Version: a.Version, ContentHash: a.ContentHash})
		if err != nil {
			return Manifest{}, fmt.Errorf("provisioning: sign kb:%s %s/%s: %w", kbName, a.Kind, a.Name, err)
		}
		a.Signature = sig
	}
	return MergeArtifacts(artifacts), nil
}

// contentHashBytes computes a deterministic sha256 hex of raw data, with no
// filename prefix — used for artifacts whose content is generated in memory
// instead of read from a file with its own name (kind "instructions", D56).
func contentHashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// kbArchive is a top-level archive of a KB (a subdirectory of
// <kbRoot>/data/ che contiene pagine concept .md), con il conteggio delle pagine.
type kbArchive struct {
	name  string
	pages int
}

// kbArchives lists the top-level archives of the KB rooted at kbRoot: the
// direct subdirectories of <kbRoot>/data/ (where the concept .md pages live,
// see kb.DataRoot), sorted by name, each with the count of .md pages inside it
// (recursive). The KB's infrastructure directories (skills/, agents/,
// hooks/, .cartographer/, .git/) live alongside data/, not inside it, so
// they are already excluded by construction; their names are still filtered
// here defensively in case they end up as subdirectories of data/. No error if
// data/ is missing or unreadable: returns nil (BuildManifest stays best-effort on
// this purely informational count).
func kbArchives(kbRoot string) []kbArchive {
	dataDir := filepath.Join(kbRoot, "data")
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil
	}

	infra := map[string]bool{
		"skills": true, "agents": true, "hooks": true, ".cartographer": true, ".git": true,
	}

	var archives []kbArchive
	for _, e := range entries {
		if !e.IsDir() || infra[e.Name()] {
			continue
		}
		archives = append(archives, kbArchive{
			name:  e.Name(),
			pages: countMarkdownFiles(filepath.Join(dataDir, e.Name())),
		})
	}
	sort.Slice(archives, func(i, j int) bool { return archives[i].name < archives[j].name })
	return archives
}

// countMarkdownFiles counts the files with a ".md" extension under dir, recursively.
// Best-effort: a walk error simply returns the partial count accumulated
// so far, it doesn't fail instructions generation over a purely cosmetic
// count.
func countMarkdownFiles(dir string) int {
	n := 0
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".md") {
			n++
		}
		return nil
	})
	return n
}

// generateKBInstructions generates the content (in English) of the "instructions"
// imprinting artifact for the KB kbName rooted at kbRoot (D56): tells the LLM
// agent that this KB exists, lists the names of its top-level archives
// (§kbArchives) and gives three lines of operational instructions
// (search/atlas_overview/concept_read/concept_write/log_append). Extended in D61 with
// two optional sections, in this order, each omitted if it has no content:
//   - the names of the KB's agents (agents/<name>.md, §kbAgentNames) — names
//     only: the full descriptions already reach the agent via the client's
//     agent registry (the agent is installed natively), duplicating them here
//     would cost the same context twice (D65);
//   - the optional curated file <kbRoot>/instructions.md (§kbCuratedInstructions)
//     — orchestration directives hand-written by the operator.
//
// The block is stable imprinting, not state: no page counts (D65) or
// timestamps, agents and archives sorted by name — so the ContentHash (computed
// on the result of this function, see BuildManifest) changes only when the
// set of archives/agents or instructions.md changes, not on every page
// added.
func generateKBInstructions(kbName, kbRoot, toolPrefix string) string {
	var sb strings.Builder
	tool := func(base string) string { return qualifyToolName(toolPrefix, base) }

	fmt.Fprintf(&sb, "The %q KB is served via MCP by the \"cartographer\" server.", kbName)

	archives := kbArchives(kbRoot)
	if len(archives) == 0 {
		sb.WriteString(" No archives detected yet.\n\n")
	} else {
		names := make([]string, len(archives))
		for i, a := range archives {
			names[i] = a.name + "/"
		}
		fmt.Fprintf(&sb, " Archives: %s.\n\n", strings.Join(names, ", "))
	}

	curated, skipPreamble := kbCuratedInstructionsWithPreamble(kbRoot)

	if !skipPreamble {
		sb.WriteString("Operational instructions:\n")
		fmt.Fprintf(&sb, "- consult it autonomously when you need historical or architectural context: `%s` (keyword) or `%s` to orient yourself, `%s` to read;\n",
			tool("search"), tool("atlas_overview"), tool("concept_read"))
		fmt.Fprintf(&sb, "- write or update a page with `%s` when you discover something relevant; close relevant sessions with `%s`;\n",
			tool("concept_write"), tool("log_append"))
		sb.WriteString("- every write is a git commit, revertible.\n")
	}

	// The subagent sentence is NOT emitted here (D154). This function has no
	// provider argument and BuildManifest only ever runs server-side, so the
	// list could only be what the KB *declares* — and a provider whose "agent"
	// cell is unsupported receives none of them, while being told to delegate to
	// them. applyInstructionsGroup appends the sentence per provider instead,
	// the same way the D75 WP4 paths table is appended client-side.

	if curated != "" {
		sb.WriteString("\n")
		sb.WriteString(curated)
		sb.WriteString("\n")
	}

	return sb.String()
}

// qualifyToolName returns the tool name an agent must call for a KB whose
// effective tool prefix is prefix: the base name unchanged when unprefixed,
// "<prefix>__<base>" otherwise. Mirrors Server.RegisterTool's renaming (D102)
// and the client-side qualifyTool (D120). prefix is already sanitised by
// config.ResolveToolPrefix when it reaches here.
func qualifyToolName(prefix, base string) string {
	if prefix == "" {
		return base
	}
	return prefix + "__" + base
}

// kbAgentNames lists the names of the KB's agents rooted at kbRoot
// (agents/<name>.md), sorted by name for determinism. No agents/ folder
// (or empty) → nil, no error (same best-effort spirit as
// kbArchives). Names only: the descriptions stay in the agent file, which
// provisioning translates and installs natively for each provider (D65).
func kbAgentNames(kbRoot string) []string {
	entries, err := os.ReadDir(filepath.Join(kbRoot, "agents"))
	if err != nil {
		return nil
	}

	var agents []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		agents = append(agents, strings.TrimSuffix(e.Name(), ".md"))
	}
	sort.Strings(agents)
	return agents
}

// kbCuratedInstructions reads the optional curated file <kbRoot>/instructions.md
// (D61): free-form markdown orchestration directives hand-written by the
// operator, included in the block AFTER the auto-generated part. Lives in the
// ROOT of the KB, alongside data/, skills/, agents/ — never inside data/, so it
// isn't scanned by kb.Walk/the indexes (it doesn't become a concept) and isn't
// materialized as a file of its own: only its content flows into the generated
// text. If it has frontmatter (tolerated but not required for a file meant as
// free-form markdown) it is discarded and only the body is used. Missing file →
// empty string (no section).
func kbCuratedInstructions(kbRoot string) string {
	body, _ := kbCuratedInstructionsWithPreamble(kbRoot)
	return body
}

// preambleNoneRe matches the opt-out directive on the FIRST line of
// instructions.md. First line only, and an exact spelling, so a KB can document
// the directive in its own prose without triggering it — the same trap as the
// placeholder syntax (D163).
var preambleNoneRe = regexp.MustCompile(`(?i)^<!--\s*cartographer:\s*preamble:\s*none\s*-->\s*$`)

// kbCuratedInstructionsWithPreamble returns the KB's curated instructions and
// whether it opted out of the generated "Operational instructions" bullets
// (D154).
//
// The generated wrapper is hardcoded English, so a KB written in the team's
// working language yielded a steering file that switched language twice — 1765
// bytes of English around 6559 of another language, and the *first* thing the
// model reads, which is the worst position for an inconsistency because it sets
// the expected output language. It is also partly redundant with any
// instructions.md that already explains how to consult the KB.
//
// Deliberately NOT a localisation mechanism: a language key would make
// Cartographer carry translations of its own prose forever. The KB owns the
// prose instead. The one-line routing sentence (KB name, server, archives) stays
// generated in every case — it is state, not prose, and no KB can write it for
// itself — so the residual is one English line rather than none.
func kbCuratedInstructionsWithPreamble(kbRoot string) (body string, skipPreamble bool) {
	data, err := os.ReadFile(filepath.Join(kbRoot, "instructions.md"))
	if err != nil {
		return "", false
	}
	_, content, hasFM := okf.SplitFrontmatter(string(data))
	if !hasFM {
		content = string(data)
	}
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if preambleNoneRe.MatchString(strings.TrimSpace(l)) {
			skipPreamble = true
			lines = append(lines[:i], lines[i+1:]...)
			content = strings.Join(lines, "\n")
		}
		break
	}
	return strings.TrimSpace(content), skipPreamble
}

// contentHashFile computes the versioned artifact hash of a single,
// non-executable file (agents and MCP descriptors).
func contentHashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("provisioning: read %s: %w", path, err)
	}
	return hashArtifactFiles([]ArtifactFile{{Path: filepath.Base(path), Content: data}}), nil
}

// MergeArtifacts deduplicates a list of artifacts by kind+name, sorts them
// deterministically and computes the aggregate revision. Factored out of BuildManifest
// so the multi-KB client (internal/client, cmd/cartographer) can also merge the
// artifacts received from several sync_pull calls (one per KB) with the same
// precedence rule used server-side for a single KB.
//
// Deduplicated by kind+name: a skill's destination depends only on name
// (skillDestDir doesn't use source), so two artifacts with the same kind+name
// would point at the same folder. Typical case: skill_install copies a bundled
// skill into the KB → it would appear both as "bundle" and as "kb:<name>".
// Precedence: the KB beats the bundle (domain version, potentially updated).
// Among several KBs (or several sync_pull calls) the choice is deterministic by
// alphabetical source.
func MergeArtifacts(artifacts []Artifact) Manifest {
	byKey := make(map[string]Artifact, len(artifacts))
	for _, a := range artifacts {
		k := a.Kind + "\x00" + a.Name
		if existing, ok := byKey[k]; ok {
			byKey[k] = preferArtifact(existing, a)
		} else {
			byKey[k] = a
		}
	}
	merged := make([]Artifact, 0, len(byKey))
	for _, a := range byKey {
		merged = append(merged, a)
	}

	// Ordina per determinismo: kind, source, name.
	sort.Slice(merged, func(i, j int) bool {
		ai, aj := merged[i], merged[j]
		if ai.Kind != aj.Kind {
			return ai.Kind < aj.Kind
		}
		if ai.Source != aj.Source {
			return ai.Source < aj.Source
		}
		return ai.Name < aj.Name
	})

	// Aggregate revision: sha256 of the sorted concatenation of "kind|name|source|content_hash\n".
	return Manifest{
		Revision:  computeRevision(merged),
		Artifacts: merged,
	}
}

// preferArtifact chooses, between two artifacts with the same kind+name, the one
// that wins in materialization. The KB ("kb:*") beats the bundle; for the same
// source type, the choice is deterministic by alphabetical source.
func preferArtifact(a, b Artifact) Artifact {
	aKB := strings.HasPrefix(a.Source, "kb:")
	bKB := strings.HasPrefix(b.Source, "kb:")
	if aKB != bKB {
		if aKB {
			return a
		}
		return b
	}
	if a.Source <= b.Source {
		return a
	}
	return b
}

// computeRevision computes the Manifest's aggregate revision.
// Artifacts must already be sorted to guarantee determinism.
func computeRevision(artifacts []Artifact) string {
	h := sha256.New()
	for _, a := range artifacts {
		fmt.Fprintf(h, "%s|%s|%s|%s\n", a.Kind, a.Name, a.Source, a.ContentHash)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// --- Lockfile ---

// ReadLock reads the JSON lockfile from the given path.
// If the file doesn't exist, returns Lock{} without error (first run).
func ReadLock(path string) (Lock, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Lock{}, nil
	}
	if err != nil {
		return Lock{}, fmt.Errorf("provisioning: read lock %s: %w", path, err)
	}
	var lock Lock
	if err := json.Unmarshal(data, &lock); err != nil {
		return Lock{}, fmt.Errorf("provisioning: parse lock %s: %w", path, err)
	}
	return lock, nil
}

// WriteLock serializes and writes the lockfile to the given path.
// Creates the parent directories if needed.
func WriteLock(path string, lock Lock) error {
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return fmt.Errorf("provisioning: serialize lock: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("provisioning: mkdir lock dir: %w", err)
	}
	if err := writeFileNoFollow(path, data, 0o644); err != nil {
		return fmt.Errorf("provisioning: write lock %s: %w", path, err)
	}
	return nil
}

// --- Lockfile v2 (multi-provider) ---

// LockFile is the v2 format of the lockfile written by the multi-provider CLI
// client (cartographer connect/sync): one Lock per connected provider, indexed by
// provider name, in the same .cartographer-sync.lock.json file (LockFileName).
//
// Backward compatibility: v1 lockfiles (written by the sync_apply MCP tool or the
// old cartographer-configure, a single provider with a top-level "provider"
// field) are automatically migrated on read by ReadLockFile.
type LockFile struct {
	Providers map[string]Lock `json:"providers"`
}

// ReadLockFile reads the multi-provider lockfile at the given path. If the file
// doesn't exist, returns an empty LockFile (first run) without error. If the file is
// in the legacy v1 format (non-empty top-level "provider" field), migrates it in
// memory to v2.
func ReadLockFile(path string) (LockFile, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return LockFile{Providers: map[string]Lock{}}, nil
	}
	if err != nil {
		return LockFile{}, fmt.Errorf("provisioning: read lockfile %s: %w", path, err)
	}

	var probe struct {
		Provider string `json:"provider"`
	}
	_ = json.Unmarshal(data, &probe)
	if probe.Provider != "" {
		var v1 Lock
		if err := json.Unmarshal(data, &v1); err != nil {
			return LockFile{}, fmt.Errorf("provisioning: parse legacy lockfile %s: %w", path, err)
		}
		return LockFile{Providers: map[string]Lock{v1.Provider: v1}}, nil
	}

	var lf LockFile
	if err := json.Unmarshal(data, &lf); err != nil {
		return LockFile{}, fmt.Errorf("provisioning: parse lockfile %s: %w", path, err)
	}
	if lf.Providers == nil {
		lf.Providers = map[string]Lock{}
	}
	return lf, nil
}

// WriteLockFile serializes and writes the v2 lockfile to the given path.
func WriteLockFile(path string, lf LockFile) error {
	data, err := json.MarshalIndent(lf, "", "  ")
	if err != nil {
		return fmt.Errorf("provisioning: serialize lockfile: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("provisioning: mkdir lockfile dir: %w", err)
	}
	if err := writeFileNoFollow(path, data, 0o644); err != nil {
		return fmt.Errorf("provisioning: write lockfile %s: %w", path, err)
	}
	return nil
}

// ForProvider returns the given provider's Lock (zero value if absent).
func (lf LockFile) ForProvider(provider string) Lock {
	if lf.Providers == nil {
		return Lock{}
	}
	return lf.Providers[provider]
}

// SetProvider updates (or adds) the given provider's Lock.
func (lf *LockFile) SetProvider(provider string, lock Lock) {
	if lf.Providers == nil {
		lf.Providers = map[string]Lock{}
	}
	lock.Provider = provider
	lf.Providers[provider] = lock
}

// --- Diff ---

// ComputeDiff compares the Manifest with the Lock and returns the Diff.
//
//   - Added: artifacts in the manifest not present in the lock (by kind+name).
//   - Updated: present in both but with a different content_hash.
//   - Removed: ManagedFile in the lock no longer present in the manifest.
//   - InSync: identical revisions AND no material difference. Comparing only
//     the revision would lie: Apply marks the revision as applied even when
//     unsigned artifacts remain in NeedsApproval, and those would show up as
//     Added with InSync=true ("in-sync" with missing stuff).
func ComputeDiff(m Manifest, lock Lock) Diff {
	// Map managed files by kind+name (matching key).
	managedByKey := make(map[string]ManagedFile, len(lock.Managed))
	for _, mf := range lock.Managed {
		k := mf.Kind + "\x00" + mf.Name
		// If there are several files for the same artifact, take the first for the hash comparison.
		if _, exists := managedByKey[k]; !exists {
			managedByKey[k] = mf
		}
	}

	var d Diff

	// Map manifest artifacts to detect stale managed entries.
	manifestKeys := make(map[string]bool, len(m.Artifacts))
	for _, a := range m.Artifacts {
		k := a.Kind + "\x00" + a.Name
		manifestKeys[k] = true

		mf, exists := managedByKey[k]
		if !exists {
			d.Added = append(d.Added, a)
		} else if mf.ContentHash != a.ContentHash {
			d.Updated = append(d.Updated, a)
		}
	}

	// Removed: managed files no longer in the manifest. The bootstrap hook (D60,
	// BootstrapHookName) is excluded: it's a client-side artifact, never present in
	// the server manifest by construction — treating it as "removed" would make
	// it disappear on every sync instead of staying stable between one
	// EnsureBootstrapHook and the next (see also the twin carry-forward in Apply).
	for _, mf := range lock.Managed {
		if mf.Kind == "hook" && mf.Name == BootstrapHookName {
			continue
		}
		k := mf.Kind + "\x00" + mf.Name
		if !manifestKeys[k] {
			d.Removed = append(d.Removed, mf)
		}
	}

	d.InSync = m.Revision == lock.AppliedRevision &&
		len(d.Added) == 0 && len(d.Updated) == 0 && len(d.Removed) == 0

	return d
}

// FilterForProvider returns a copy of m with only the artifacts that have a
// known destination for the provider (destDir != ""). Used by status/TUI
// for honest per-provider counts and diffs: a kind the provider can't
// materialize isn't "missing", it simply doesn't apply (e.g. hook
// exists only for Claude Code — D48; agent for Claude Code and OpenCode — D55).
func FilterForProvider(m Manifest, provider configurator.Provider) Manifest {
	out := Manifest{Revision: m.Revision}
	for _, a := range m.Artifacts {
		if destDir(a.Kind, a.Name, provider) != "" {
			out.Artifacts = append(out.Artifacts, a)
		}
	}
	return out
}

// KindCount is the installed-vs-total count of provisioning artifacts of a given
// kind, for one provider. Installed counts ManagedFile entries whose ContentHash
// still matches the manifest (i.e. not stale/pending an update).
type KindCount struct {
	Installed, Total int
}

// KindCounts aggregates per-kind counts of m's artifacts (Total) against lock's
// managed files (Installed), keyed by Kind ("skill", "agent", "hook", ...). Used by
// `cartographer status` and the TUI dashboard to render a compact per-kind summary
// (e.g. "skill 4/5 · agent 2/2 · hook 1/1") alongside the overall drift status.
func KindCounts(m Manifest, lock Lock) map[string]KindCount {
	managedByKey := make(map[string]ManagedFile, len(lock.Managed))
	for _, mf := range lock.Managed {
		k := mf.Kind + "\x00" + mf.Name
		if _, exists := managedByKey[k]; !exists {
			managedByKey[k] = mf
		}
	}

	counts := make(map[string]KindCount)
	for _, a := range m.Artifacts {
		c := counts[a.Kind]
		c.Total++
		if mf, ok := managedByKey[a.Kind+"\x00"+a.Name]; ok && mf.ContentHash == a.ContentHash {
			c.Installed++
		}
		counts[a.Kind] = c
	}
	return counts
}

// --- Apply ---

// Apply materializes verified, built-in, or explicitly authorized artifacts into BaseDir, prunes stale managed
// entries and writes the updated lockfile.
//
// Safety rules:
//   - Signed:true or BuiltIn:true → copy/update into BaseDir for the given provider.
//   - AutoTrust:true explicitly authorizes eligible unsigned KB artifacts.
//   - all other artifacts → NeedsApproval.
//
// Providers supported for direct materialization:
//   - claude: skill in .claude/skills/<name>/, agent in .claude/agents/<name>.md,
//     hook in .claude/hooks/<name>/ — and registered/updated in
//     .claude/settings.json (D57, see registerHookSettings)
//   - opencode: skill in .opencode/skills/<name>/, agent in .opencode/agent/<name>.md
//     (D55 — translated content, see translateAgentForProvider, not verbatim);
//     hook in .opencode/hooks/<name>/ — and, if the event has an equivalent in
//     OpenCode's plugin engine, a generated JS plugin in
//     .config/opencode/plugins/cartographer-<name>.js (D59, see
//     registerOpenCodePlugin/generateOpenCodePlugin in hooksettings.go).
//   - codex: skill in .codex/skills/<name>/; agent in .codex/agents/<name>.toml
//     (D58 — content translated into Codex's subagent schema, see
//     translateAgentForProvider); hook in .codex/hooks/<name>/ — and registered
//     in the Cartographer-managed block of .codex/config.toml (D58, see
//     registerHookConfigTOML in hooksettings.go).
//   - kiro: skill materialized as today; agent/hook not supported (no known
//     destination for this provider) → Unsupported.
//     To extend: add a case in destDir for the new kind×provider.
//
// Managed-only prune: removes from BaseDir ONLY the paths in opts.Lock.Managed no
// longer in the manifest. Never touches files not managed by Cartographer.
//
// Idempotent: two Apply calls on the same revision = no-op.
// DryRun: computes everything but writes nothing (neither files nor lockfile).
func Apply(m Manifest, opts ApplyOptions) (AppliedResult, error) {
	if err := PreflightStdioMCP(m, opts); err != nil {
		return AppliedResult{}, err
	}
	diff := ComputeDiff(m, opts.Lock)

	var result AppliedResult
	tracker := newExpansionTracker()

	// Artifacts to update (add + update).
	toWrite := append(diff.Added, diff.Updated...)
	toWriteKeys := make(map[string]bool, len(toWrite))
	for _, a := range toWrite {
		toWriteKeys[a.Kind+"\x00"+a.Name] = true
	}
	// On-disk verification (D139): the manifest↔lockfile comparison above says
	// nothing about what is actually on disk. An artifact whose files were
	// edited, deleted, or whose managed key/block was removed from a shared
	// file is rewritten from the server's version — the server is the source
	// of truth and sync restores, no merge and no backup copy. This also
	// covers executable-mode drift, which is part of the materialized hash:
	// a chmod alone is a modified artifact.
	healed := make(map[string]bool)
	for _, f := range VerifyManaged(opts.Lock, opts.Provider, opts.BaseDir) {
		if !f.Healable() {
			continue
		}
		if opts.NoHeal {
			result.Divergent = append(result.Divergent, f)
			continue
		}
		key := f.Kind + "\x00" + f.Name
		if toWriteKeys[key] {
			continue
		}
		// Authorization is unchanged: an unauthorized artifact is never
		// healed, it goes to NeedsApproval exactly as it would otherwise.
		for _, a := range m.Artifacts {
			if a.Kind == f.Kind && a.Name == f.Name && artifactAuthorized(a, opts) {
				toWrite = append(toWrite, a)
				toWriteKeys[key] = true
				healed[key] = true
				break
			}
		}
	}
	unauthorizedMCPKeys := make(map[string]bool)
	// A revoked MCP approval must be observed even when the manifest hash is
	// unchanged. Add it to the authorization pass so its previously managed
	// provider entry is pruned instead of surviving on stale local state.
	for _, a := range m.Artifacts {
		key := a.Kind + "\x00" + a.Name
		if a.Kind == "mcp" && !toWriteKeys[key] && !artifactAuthorized(a, opts) {
			toWrite = append(toWrite, a)
			toWriteKeys[key] = true
			unauthorizedMCPKeys[key] = true
		}
	}
	for _, a := range toWrite {
		if a.Kind == "mcp" && !artifactAuthorized(a, opts) {
			unauthorizedMCPKeys[a.Kind+"\x00"+a.Name] = true
		}
	}

	// Build the new managed set, starting from the unchanged ones.
	manifestKeys := make(map[string]bool, len(m.Artifacts))
	for _, a := range m.Artifacts {
		manifestKeys[a.Kind+"\x00"+a.Name] = true
	}
	newManaged := make([]ManagedFile, 0)
	for _, mf := range opts.Lock.Managed {
		// The "instructions" kind (D56) is handled entirely by
		// applyInstructionsGroup below (group, not per-artifact): don't report it
		// here, it would otherwise be duplicated.
		if mf.Kind == "instructions" {
			continue
		}
		// Bootstrap hook (D60): never driven by the server manifest, always
		// preserved through Apply — see EnsureBootstrapHook, which
		// regenerates/registers it separately and expects to find it here again
		// next round (same principle as the twin carry-forward in ComputeDiff).
		if mf.Kind == "hook" && mf.Name == BootstrapHookName {
			newManaged = append(newManaged, mf)
			continue
		}
		k := mf.Kind + "\x00" + mf.Name
		// Keep the managed entries that are unchanged and not removed.
		if manifestKeys[k] && !toWriteKeys[k] {
			newManaged = append(newManaged, mf)
		}
	}

	// Materialize the authorized artifacts.
	for _, a := range toWrite {
		if a.Kind == "instructions" {
			// Handled as a group by applyInstructionsGroup below, not here.
			continue
		}
		if a.Kind == "hook" && a.Name == BootstrapHookName {
			// Name reserved for the client-side bootstrap (D60, EnsureBootstrapHook):
			// a KB defining a hook with this very name would otherwise be
			// written over the files EnsureBootstrapHook manages on its own —
			// ignored with a warning, never materialized.
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"hook %q: name reserved by Cartographer (bootstrap), KB artifact ignored", a.Name))
			continue
		}
		if !artifactAuthorized(a, opts) {
			// Unsigned artifact: notify + signature gate. Don't write.
			result.NeedsApproval = append(result.NeedsApproval, a)
			continue
		}

		// The name determines the destination directory: it must be a single
		// local segment (no separators, "..", absolute paths) — artifacts
		// can arrive from a remote server via sync_pull.
		if a.Name != filepath.Base(a.Name) || !filepath.IsLocal(a.Name) {
			return AppliedResult{}, fmt.Errorf("provisioning: invalid artifact name %q", a.Name)
		}

		// Provider/kind with no known destination (destRel == ""): the provider
		// doesn't support this kind — no approval unblocks it, so it goes to
		// Unsupported (not NeedsApproval). Support is determined by destDir:
		// add a case there for a new kind×provider.
		destRel := destDir(a.Kind, a.Name, opts.Provider)
		if destRel == "" {
			result.Unsupported = append(result.Unsupported, a)
			continue
		}

		var relPaths []string
		// materializedHash is the hash of the bytes written to disk (D138);
		// a.ContentHash stays the source hash ComputeDiff compares.
		var materializedHash string

		if a.Kind == "mcp" {
			// Third-party MCP server (D69, WP3): no file materialized in its own
			// folder — a direct merge into the provider's native config
			// (registerMCPServer, mcpsettings.go). The source content
			// (mcp/<name>.json) has already been validated in BuildManifest
			// (parseMCPServerSpec); the parse here should never fail, but it
			// remains a defensive fatal error (not a warning) if it does.
			if !opts.DryRun {
				content, contentErr := singleArtifactContent(a, opts)
				if contentErr != nil {
					return AppliedResult{}, fmt.Errorf("provisioning: read mcp %s: %w", a.Name, contentErr)
				}
				kbSpec, specErr := parseMCPServerSpec(a.Name, content)
				if specErr != nil {
					return AppliedResult{}, fmt.Errorf("provisioning: mcp %s: %w", a.Name, specErr)
				}
				spec := configurator.ServerSpec{Type: kbSpec.Type, URL: kbSpec.URL, Command: kbSpec.Command, Args: kbSpec.Args, Headers: kbSpec.Headers, Env: kbSpec.Env}
				if d, ok := configurator.Lookup(opts.Provider); ok && !d.SupportsMCPHeaders && len(kbSpec.Headers) > 0 {
					// A provider whose emitter cannot represent auth headers
					// (kiro, unchanged since D69) — warn instead of silently
					// failing the authentication of the server distributed by
					// the KB.
					result.Warnings = append(result.Warnings, fmt.Sprintf(
						"mcp %q: %s does not support headers for MCP servers, ignored", a.Name, d.Provider))
				}
				rp, warnings, regErr := registerMCPServer(opts.BaseDir, a.Name, spec, opts.Provider)
				if regErr != nil {
					return AppliedResult{}, fmt.Errorf("provisioning: register mcp %s: %w", a.Name, regErr)
				}
				result.Warnings = append(result.Warnings, warnings...)
				relPaths = []string{rp}
			} else {
				relPaths = []string{destRel}
			}
		} else if a.Kind == "agent" {
			// agent: a single file, destRel is the file's full path (not a
			// directory) — e.g. .claude/agents/<name>.md.
			fullDestPath := filepath.Join(opts.BaseDir, destRel)
			if !opts.DryRun {
				if err := mkdirAllNoFollow(opts.BaseDir, filepath.Dir(fullDestPath), 0o755); err != nil {
					if refuse(&result, a, err) {
						continue
					}
					return AppliedResult{}, fmt.Errorf("provisioning: mkdir %s: %w", filepath.Dir(fullDestPath), err)
				}
				content, err := singleArtifactContent(a, opts)
				if err != nil {
					return AppliedResult{}, fmt.Errorf("provisioning: copy agent %s: %w", a.Name, err)
				}
				// Placeholder expansion (D75 WP3) BEFORE the per-provider
				// translation: the hash stays provider-agnostic, like every other
				// kind (docs/decisions/sync-provisioning.md D50) — it only differs based on the
				// KB content and local resolution, never on the destination
				// provider.
				content = expandPlaceholders(content, opts, tracker)
				content = stampArtifact(content, a)
				content, err = translateAgentForProvider(opts.Provider, a.Name, content)
				if err != nil {
					return AppliedResult{}, fmt.Errorf("provisioning: translate agent %s for %s: %w", a.Name, opts.Provider, err)
				}
				// After the translation: this is the byte sequence on disk.
				materializedHash = hashArtifactFiles([]ArtifactFile{{Path: a.Name + ".md", Content: content}})
				if err := writeFileNoFollow(fullDestPath, content, 0o644); err != nil {
					if refuse(&result, a, err) {
						continue
					}
					return AppliedResult{}, fmt.Errorf("provisioning: write agent %s: %w", a.Name, err)
				}
			}
			relPaths = []string{destRel}
		} else {
			// skill/hook: destRel is a directory, one or more files inside it.
			fullDestDir := filepath.Join(opts.BaseDir, destRel)
			if !opts.DryRun {
				if err := mkdirAllNoFollow(opts.BaseDir, fullDestDir, 0o755); err != nil {
					if refuse(&result, a, err) {
						continue
					}
					return AppliedResult{}, fmt.Errorf("provisioning: mkdir %s: %w", fullDestDir, err)
				}
				var err error
				var stampWarning string
				relPaths, materializedHash, stampWarning, err = copyArtifactFiles(a, opts, fullDestDir, tracker)
				if stampWarning != "" {
					result.Warnings = append(result.Warnings, stampWarning)
				}
				if err != nil {
					if refuse(&result, a, err) {
						continue
					}
					return AppliedResult{}, fmt.Errorf("provisioning: copy %s %s: %w", a.Kind, a.Name, err)
				}
				if a.Kind == "hook" {
					// Register the hook in the provider's native mechanism
					// (D137's hookMechanisms table: settings.json D57,
					// config.toml D58, generated plugin D59), starting from
					// the hook.json just materialized. Best-effort on a
					// malformed hook.json (see registerHookSettings/
					// registerHookConfigTOML): doesn't fail materialization.
					if mechanism, ok := hookMechanisms[opts.Provider]; ok {
						pluginRel, warning, regErr := mechanism.register(opts.BaseDir, a.Name, fullDestDir)
						if regErr != nil {
							return AppliedResult{}, regErr
						}
						if warning != "" {
							result.Warnings = append(result.Warnings, warning)
						}
						if pluginRel != "" {
							relPaths = append(relPaths, pluginRel)
						}
					}
				}
			} else {
				// DryRun: simulate the main file's path without writing.
				relPaths = []string{filepath.Join(destRel, principalFile(a.Kind))}
			}
		}

		for _, rp := range relPaths {
			mf := ManagedFile{
				Kind:             a.Kind,
				Name:             a.Name,
				Path:             rp,
				ContentHash:      a.ContentHash,
				MaterializedHash: materializedHash,
			}
			newManaged = append(newManaged, mf)
			result.Written = append(result.Written, mf)
			if healed[a.Kind+"\x00"+a.Name] {
				result.Healed = append(result.Healed, mf)
			}
		}
	}

	// Kind "instructions" (D56): handled as a GROUP, not per-artifact. Must be
	// applied before the generic prune below, which must ignore its
	// ManagedFile entries (see the genericRemoved filter) — applyInstructionsGroup
	// already takes care of it internally by rewriting the block wholesale.
	forceInstructions := false
	for key := range healed {
		if strings.HasPrefix(key, "instructions\x00") {
			forceInstructions = true
		}
	}
	if err := applyInstructionsGroup(m, diff, opts, tracker, &result, &newManaged, forceInstructions); err != nil {
		return AppliedResult{}, err
	}

	result.Warnings = append(result.Warnings, tracker.warnings...)
	result.Warnings = append(result.Warnings, unsupportedKindWarnings(m, opts.Provider)...)

	// Prune: remove stale managed entries from BaseDir. The "instructions" kind
	// is excluded: removing it with the generic logic (one file per
	// ManagedFile) would delete the user's entire instructions file instead of
	// just the managed block — applyInstructionsGroup above already handles it.
	var genericRemoved []ManagedFile
	for _, mf := range diff.Removed {
		if mf.Kind != "instructions" {
			genericRemoved = append(genericRemoved, mf)
		}
	}
	for _, mf := range opts.Lock.Managed {
		if mf.Kind == "mcp" && unauthorizedMCPKeys[mf.Kind+"\x00"+mf.Name] {
			genericRemoved = append(genericRemoved, mf)
		}
	}
	pruned, err := PruneManaged(genericRemoved, opts.BaseDir, opts.DryRun)
	if err != nil {
		return AppliedResult{}, err
	}
	result.Pruned = append(result.Pruned, pruned...)

	// Build the new Lock.
	result.NewLock = Lock{
		AppliedRevision: m.Revision,
		Provider:        string(opts.Provider),
		Managed:         newManaged,
	}

	// Write the v1 lockfile (only if not DryRun and not SkipLockWrite — the
	// multi-provider clients handle their own persistence to the v2 LockFile, see
	// ApplyOptions.SkipLockWrite).
	if !opts.DryRun && !opts.SkipLockWrite {
		lockPath := filepath.Join(opts.BaseDir, LockFileName)
		if err := WriteLock(lockPath, result.NewLock); err != nil {
			return AppliedResult{}, err
		}
	}

	return result, nil
}

// --- Instructions: managed block (D56) ---

// instructionsBlockBegin/instructionsBlockEnd delimit the block Cartographer
// manages inside the user's global instructions files (§applyInstructionsGroup).
// Anything outside these markers, in the file, is never touched.
// instructionsBlockBeginPrefix is what RECOGNIZES an existing begin marker:
// the tail after the em dash is display text, not marker identity — older
// versions wrote it in Italian, and matching the full current constant would
// miss their blocks and duplicate the block on the next sync.
const (
	instructionsBlockBeginPrefix = "<!-- cartographer:instructions:begin"
	instructionsBlockBegin       = instructionsBlockBeginPrefix + " — block managed by Cartographer, do not edit by hand -->"
	instructionsBlockEnd         = "<!-- cartographer:instructions:end -->"
)

// applyInstructionsGroup materializes the "instructions" kind (D56) as a
// GROUP — unlike every other kind, which is materialized per-artifact.
// Each provider has a single shared file (destDir("instructions", _, provider));
// the block delimited by the instructionsBlockBegin/End markers in that file
// contains the concatenation, sorted by Name, of the snippets of ALL *signed*
// instructions currently in the manifest m — not just those added/updated in this
// call. That's why the rewrite trigger isn't "this specific artifact
// changed" but "something in the instructions kind changed" (an Added/Updated in
// diff, or an instructions ManagedFile in diff.Removed): when it fires, the block
// is rebuilt from scratch from the current set, so a removed KB disappears from
// its section without leaving residue.
//
// Unsigned artifacts still follow the existing gate (NeedsApproval),
// like every other kind. A ManagedFile is still registered in *newManaged for
// every signed instructions artifact — even when the trigger doesn't fire, to
// keep newManaged consistent with the unchanged state — with Path equal to the
// provider's shared file, so KindCounts reports "instructions n/n" instead of
// counting a single physical file.
func applyInstructionsGroup(m Manifest, diff Diff, opts ApplyOptions, tracker *expansionTracker, result *AppliedResult, newManaged *[]ManagedFile, force bool) error {
	destRel := destDir("instructions", "", opts.Provider)
	if destRel == "" {
		// Provider with no known destination for instructions (none today: all
		// four providers support it — see destDir — but the case stays handled
		// for consistency with the other kinds' unsupported/needs_approval schema,
		// in case a future provider didn't implement it).
		for _, a := range m.Artifacts {
			if a.Kind == "instructions" {
				result.Unsupported = append(result.Unsupported, a)
			}
		}
		return nil
	}

	// Trigger: did something in the instructions kind change in this Apply —
	// or did the managed block disappear from the provider's file (D139)?
	// The block is rewritten when the instructions artifact itself changed, and
	// also when the KB's agent set changed: since D154 the block's rendered
	// content includes a per-provider sentence naming the subagents THIS client
	// can install, so it depends on the agent artifacts even though their content
	// is not part of the instructions artifact's hash. Without this the sentence
	// would go stale the moment an agent was added or removed.
	triggersRewrite := func(kind string) bool { return kind == "instructions" || kind == "agent" }

	triggered := force
	for _, a := range diff.Added {
		if triggersRewrite(a.Kind) {
			triggered = true
			break
		}
	}
	if !triggered {
		for _, a := range diff.Updated {
			if triggersRewrite(a.Kind) {
				triggered = true
				break
			}
		}
	}
	if !triggered {
		for _, mf := range diff.Removed {
			if triggersRewrite(mf.Kind) {
				triggered = true
				break
			}
		}
	}
	// Removals are still reported as "prune" (conceptual: the section
	// disappears from the rebuilt block), regardless of the shared physical path.
	for _, mf := range diff.Removed {
		if mf.Kind == "instructions" {
			result.Pruned = append(result.Pruned, mf)
		}
	}

	// Full current set (not just toWrite): the block is always rebuilt
	// from the whole manifest, sorted by Name for deterministic output.
	var current []Artifact
	for _, a := range m.Artifacts {
		if a.Kind == "instructions" {
			current = append(current, a)
		}
	}
	sort.Slice(current, func(i, j int) bool { return current[i].Name < current[j].Name })

	// Expanded content and hash computed here, once per authorized artifact,
	// regardless of triggered: newManaged (below) must stay consistent with the
	// expanded content even in rounds where the block isn't rewritten
	// (D75 WP3 — the instructions content already lives in memory via a.Files,
	// BuildManifest, so the cost is zero even when nothing else needs it).
	var signed []Artifact
	expandedContent := make(map[string][]byte, len(current))
	contentHashes := make(map[string]string, len(current))
	for _, a := range current {
		if !artifactAuthorized(a, opts) {
			result.NeedsApproval = append(result.NeedsApproval, a)
			continue
		}
		signed = append(signed, a)

		content, err := singleArtifactContent(a, opts)
		if err != nil {
			return fmt.Errorf("provisioning: instructions content %s: %w", a.Name, err)
		}
		content = expandPlaceholders(content, opts, tracker)
		expandedContent[a.Name] = content

		contentHash := a.ContentHash
		if opts.ExpandPlaceholders {
			contentHash = hashArtifactFiles([]ArtifactFile{{Path: "instructions.md", Content: content}})
		}
		contentHashes[a.Name] = contentHash
		*newManaged = append(*newManaged, ManagedFile{
			Kind: a.Kind, Name: a.Name, Path: destRel, ContentHash: contentHash,
		})
	}

	if !triggered {
		return nil
	}

	fullPath := filepath.Join(opts.BaseDir, destRel)

	if len(signed) == 0 {
		if !opts.DryRun {
			if err := removeInstructionsBlock(fullPath); err != nil {
				return fmt.Errorf("provisioning: remove instructions block %s: %w", destRel, err)
			}
			pruneEmptyDirs(opts.BaseDir, destRel)
		}
		return nil
	}

	snippets := make([]string, 0, len(signed))
	for _, a := range signed {
		snippets = append(snippets, strings.TrimRight(string(expandedContent[a.Name]), "\n"))
	}
	body := strings.Join(snippets, "\n\n")

	// "Local paths" table (D75 WP4): every repo:/path: placeholder resolved
	// during THIS Apply (not just in the instructions content — also
	// agent/skill/hook, see the shared tracker) becomes a key->local path
	// row, so the agent doesn't have to re-run `cartographer resolve` for
	// ones already known. Client-side only (opts.ExpandPlaceholders): the server
	// never expands anything, so it has nothing to tabulate.
	if opts.ExpandPlaceholders {
		if table := buildPathsTable(tracker.resolved); table != "" {
			body += "\n\n" + table
		}
	}

	// The subagent sentence, per provider and per KB (D154): it must describe
	// what THIS client received, not what the KB declares. Kiro and Hermes have
	// no native subagent directory, so their "agent" cell is unsupportedDest and
	// they receive none — while the old, server-generated sentence told them to
	// delegate to subagents that were not there.
	if sentence := installedSubagentSentence(m, opts); sentence != "" {
		body += "\n\n" + sentence
	}

	if !opts.DryRun {
		if err := writeInstructionsBlock(fullPath, body); err != nil {
			return fmt.Errorf("provisioning: write instructions block %s: %w", destRel, err)
		}
	}

	for _, a := range signed {
		result.Written = append(result.Written, ManagedFile{
			Kind: a.Kind, Name: a.Name, Path: destRel, ContentHash: contentHashes[a.Name],
		})
	}
	return nil
}

// unsupportedKindWarnings reports, per KB and per artifact kind, what this
// provider cannot receive at all (D154). Computed from the manifest rather than
// from the diff, so it is emitted on **every** run: the per-artifact
// "unsupported" line only appears on a run where the artifact enters the diff,
// after which the condition is invisible — while the KB keeps declaring
// artifacts that are silently not installed.
func unsupportedKindWarnings(m Manifest, provider configurator.Provider) []string {
	type key struct{ kb, kind string }
	counts := map[key]int{}
	for _, a := range m.Artifacts {
		if !strings.HasPrefix(a.Source, "kb:") || a.Kind == "instructions" {
			continue
		}
		if destDir(a.Kind, a.Name, provider) != "" {
			continue
		}
		counts[key{strings.TrimPrefix(a.Source, "kb:"), a.Kind}]++
	}
	if len(counts) == 0 {
		return nil
	}
	keys := make([]key, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].kb != keys[j].kb {
			return keys[i].kb < keys[j].kb
		}
		return keys[i].kind < keys[j].kind
	})
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, fmt.Sprintf("KB %q declares %d %s artifact(s) that %s cannot receive (no destination for this kind) — they are not installed on this client",
			k.kb, counts[k], k.kind, provider))
	}
	return out
}

// installedSubagentSentence names the KB agents this provider can actually
// receive, grouped per KB, or returns "" when none qualifies (D154). Emitted
// client-side, like the paths table: it is a statement about this client, not
// about the KB, so it deliberately does not enter the artifact hash.
//
// An artifact awaiting approval is excluded: it was not installed, and the whole
// point is that the sentence describes what happened.
func installedSubagentSentence(m Manifest, opts ApplyOptions) string {
	byKB := map[string][]string{}
	for _, a := range m.Artifacts {
		if a.Kind != "agent" || !strings.HasPrefix(a.Source, "kb:") {
			continue
		}
		if destDir("agent", a.Name, opts.Provider) == "" {
			continue
		}
		if !artifactAuthorized(a, opts) {
			continue
		}
		kbName := strings.TrimPrefix(a.Source, "kb:")
		byKB[kbName] = append(byKB[kbName], a.Name)
	}
	if len(byKB) == 0 {
		return ""
	}
	kbNames := make([]string, 0, len(byKB))
	for k := range byKB {
		kbNames = append(kbNames, k)
	}
	sort.Strings(kbNames)
	var lines []string
	for _, kbName := range kbNames {
		names := byKB[kbName]
		sort.Strings(names)
		lines = append(lines, fmt.Sprintf("Subagents installed by the %q KB: %s — their descriptions are in the client's agent registry: delegate to them the tasks they cover.",
			kbName, strings.Join(names, ", ")))
	}
	return strings.Join(lines, "\n")
}

func artifactAuthorized(a Artifact, opts ApplyOptions) bool {
	if a.Signed || a.BuiltIn {
		return true
	}
	if a.Kind == "mcp" {
		return opts.ApprovedMCP[a.Source+"\x00"+a.Name] == a.ContentHash
	}
	// MCP descriptors retain their pre-existing stricter explicit approval
	// boundary; generic trust only preserves compatibility for ordinary KB
	// provisioning artifacts.
	return opts.AutoTrust && a.Kind != "mcp" && strings.HasPrefix(a.Source, "kb:")
}

// PreflightStdioMCP verifies locally runnable commands without executing them.
// It intentionally keeps a bare command unmodified so provider configuration
// retains the user's normal PATH semantics.
func PreflightStdioMCP(m Manifest, opts ApplyOptions) error {
	for _, a := range m.Artifacts {
		if a.Kind != "mcp" || !artifactAuthorized(a, opts) {
			continue
		}
		content, err := singleArtifactContent(a, opts)
		if err != nil {
			return fmt.Errorf("provisioning: read mcp %s for preflight: %w", a.Name, err)
		}
		spec, err := parseMCPServerSpec(a.Name, content)
		if err != nil {
			return fmt.Errorf("provisioning: mcp %s preflight: %w", a.Name, err)
		}
		if spec.Type != "stdio" {
			continue
		}
		path := spec.Command
		if !filepath.IsAbs(path) {
			path, err = exec.LookPath(path)
			if err != nil {
				return fmt.Errorf("provisioning: mcp %q for %s: command %q not found on PATH", a.Name, opts.Provider, spec.Command)
			}
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("provisioning: mcp %q for %s: command %q unavailable: %w", a.Name, opts.Provider, spec.Command, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return fmt.Errorf("provisioning: mcp %q for %s: command %q is not an executable regular file", a.Name, opts.Provider, spec.Command)
		}
	}
	return nil
}

func normalizedMCPURL(raw string) string {
	normalized, err := NormalizeMCPHTTPURL(raw)
	if err != nil {
		return "<invalid>"
	}
	return normalized
}

// writeInstructionsBlock creates or rewrites the managed block delimited by
// instructionsBlockBegin/End in file path, with body as content:
//   - file missing → creates the file with just the block (mkdir -p the parent dir);
//   - file present with markers → replaces ONLY the text between the markers
//     (inclusive), leaving every other byte of the file untouched;
//   - file present without markers (pre-existing user file) → appends the block
//     at the bottom, separated by a blank line from the existing content.
//
// Idempotent: two calls in a row with the same body produce the same file.
func writeInstructionsBlock(path, body string) error {
	block := instructionsBlockBegin + "\n" + body + "\n" + instructionsBlockEnd + "\n"

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return writeFileNoFollow(path, []byte(block), 0o644)
	}
	if err != nil {
		return err
	}

	content := string(data)
	if replaced, ok := replaceBetweenMarkers(content, block); ok {
		return writeFileNoFollow(path, []byte(replaced), 0o644)
	}

	return writeFileNoFollow(path, []byte(appendBlock(content, block)), 0o644)
}

// removeInstructionsBlock removes the managed block (markers included) from
// file path, if present. If the file doesn't exist, no-op. If after removal the
// file is empty or whitespace-only, the file itself is removed (covers the case
// of a file dedicated to just the block, like .kiro/steering/cartographer.md);
// otherwise the residual content (entirely unrelated to Cartographer) is kept.
// Idempotent: if the block isn't present (already removed), no-op.
func removeInstructionsBlock(path string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	content := string(data)
	stripped, ok := replaceBetweenMarkers(content, "")
	if !ok {
		return nil
	}
	if strings.TrimSpace(stripped) == "" {
		return os.Remove(path)
	}
	return writeFileNoFollow(path, []byte(stripped), 0o644)
}

// replaceBetweenMarkers returns content with the text between instructionsBlockBegin
// and instructionsBlockEnd (markers included, plus a single trailing newline if
// present) replaced by replacement, and true, if both markers appear in that
// order; otherwise content unchanged and false. replacement == "" is equivalent
// to removing the block.
func replaceBetweenMarkers(content, replacement string) (string, bool) {
	return replaceBlock(content, instructionsBlockBeginPrefix, instructionsBlockEnd, replacement)
}

// replaceBlock is replaceBetweenMarkers over an arbitrary marker pair: the
// begin marker is matched by PREFIX (its tail is display text, not identity),
// the end marker in full. Used for the instructions block and for the
// provenance block (D138, stamp.go).
func replaceBlock(content, beginPrefix, end, replacement string) (string, bool) {
	beginIdx := strings.Index(content, beginPrefix)
	if beginIdx == -1 {
		return content, false
	}
	endMarkerIdx := strings.Index(content[beginIdx:], end)
	if endMarkerIdx == -1 {
		return content, false
	}
	endIdx := beginIdx + endMarkerIdx + len(end)
	// Consume a single newline after the end marker, if present, so
	// subsequent rewrites/removals don't accumulate blank lines.
	if endIdx < len(content) && content[endIdx] == '\n' {
		endIdx++
	}
	return content[:beginIdx] + replacement + content[endIdx:], true
}

// appendBlock appends block at the bottom of content, separated by a blank line.
// The existing content stays intact except for the trailing newline, normalized to
// a single one before the separator (so the append stays idempotent regardless of
// how many trailing newlines the original file had).
func appendBlock(content, block string) string {
	trimmed := strings.TrimRight(content, "\n")
	if trimmed == "" {
		return block
	}
	return trimmed + "\n\n" + block
}

// provisioningRootDirs are the top-level (relative to BaseDir) directories every
// provider's artifacts live under — the boundaries pruneEmptyDirs never removes,
// even left completely empty: they are provisioning's own "mount points", meant to
// persist across connect/disconnect cycles (and, for .claude/.codex/.opencode, often
// hold user files — settings.json, config.toml, opencode.json — pruneEmptyDirs has
// no business touching). Only the levels *created underneath* them (e.g.
// ".claude/skills/<name>", then ".claude/skills") are ever removed (D63).
var provisioningRootDirs = map[string]bool{
	".claude":                            true,
	".codex":                             true,
	".kiro":                              true,
	".opencode":                          true,
	".config":                            true,
	filepath.Join(".config", "opencode"): true,
	// The hermes inbox root (D141): pruning the last delivered skill empties
	// skill-inbox/<name>/cartographer and skill-inbox/<name>, never
	// skill-inbox itself — other sources deliver there too.
	hermesInboxRoot: true,
}

// pruneEmptyDirs removes every directory left empty by the removal of the file at
// baseDir/relPath, walking upward from relPath's parent — stopping (without
// removing) at BaseDir itself or at any of provisioningRootDirs (D63). Called after
// a ManagedFile has been os.Remove'd (or found already gone) so a disconnect/prune
// leaves no empty ".claude/skills/<name>/", ".opencode/hooks/<name>/", etc. behind.
//
// os.Remove fails on a non-empty directory: that failure (any error other than
// "already gone") is the guard against removing a directory that still holds other
// content — a sibling artifact, or a file the user placed there — so this never
// escalates to os.RemoveAll. An "already gone" parent (os.ErrNotExist, e.g. a
// sibling ManagedFile's removal already pruned it) does not stop the climb: the
// grandparent may now be empty too.
func pruneEmptyDirs(baseDir, relPath string) {
	dir := filepath.Dir(relPath)
	for dir != "." && dir != string(filepath.Separator) {
		if provisioningRootDirs[dir] {
			return
		}
		err := os.Remove(filepath.Join(baseDir, dir))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// PruneManaged removes every file in managed from baseDir. Missing files (already
// removed by other means) are not an error. DryRun computes the same return value
// (what would be pruned) without touching disk.
//
// Kind "instructions" (D56) is special-cased: managed files of this kind are a
// shared user-owned file (e.g. .claude/CLAUDE.md), not a Cartographer-only file —
// removing it means stripping the managed block (see removeInstructionsBlock), not
// os.Remove-ing the whole file; the file itself is only deleted if that leaves it
// empty/whitespace-only. Several ManagedFile entries (one per KB) can point at the
// same Path: the block is stripped once per distinct path (idempotent regardless,
// since removeInstructionsBlock no-ops once the block is gone, but the dedup avoids
// redundant I/O).
//
// Factored out of Apply's own prune step (diff.Removed) so `cartographer
// disconnect` can prune the *entire* managed set of a provider — not just the
// subset a fresh manifest diff would flag as removed, since disconnect does not
// (and should not need to) reach the server.
//
// Kind "hook" (D57, extended to codex in D58): besides removing the
// materialized files, the provider-native registration must be stripped too
// (settings.json for claude via removeHookEntries, the config.toml block for
// codex via removeHookConfigTOML) — otherwise a removed/pruned hook would keep
// firing from a dangling command that no longer exists on disk. A hook can
// have several ManagedFile entries (hook.json + script(s), same Name): the
// entry is stripped once per distinct hook name, same dedup pattern as the
// instructions block above. No provider parameter is needed — mf.Path already
// encodes which provider materialized it (destDir gives each provider its own
// hooks directory), see hookProviderFromPath.
func PruneManaged(managed []ManagedFile, baseDir string, dryRun bool) ([]ManagedFile, error) {
	pruned := make([]ManagedFile, 0, len(managed))
	blockDone := make(map[string]bool)
	hookSettingsDone := make(map[string]bool)
	mcpDone := make(map[string]bool)
	for _, mf := range managed {
		fullPath := filepath.Join(baseDir, mf.Path)
		if mf.Kind == "instructions" {
			if !dryRun && !blockDone[fullPath] {
				if err := removeInstructionsBlock(fullPath); err != nil {
					return nil, fmt.Errorf("provisioning: prune blocco instructions %s: %w", mf.Path, err)
				}
				blockDone[fullPath] = true
				pruneEmptyDirs(baseDir, mf.Path)
			}
			pruned = append(pruned, mf)
			continue
		}
		if mf.Kind == "mcp" {
			// Like "instructions": mf.Path is a shared file (the provider's
			// native config), never removed wholesale from here — only the
			// entry/block for this server (removeMCPServer, D69 WP4). A server
			// can have a single ManagedFile (unlike hook/instructions),
			// but the dedup by name stays for consistency with the same pattern.
			if !dryRun && !mcpDone[mf.Name] {
				if provider := mcpProviderFromPath(mf.Path); provider != "" {
					if err := removeMCPServer(baseDir, mf.Name, provider); err != nil {
						return nil, fmt.Errorf("provisioning: prune mcp %s: %w", mf.Name, err)
					}
				}
				mcpDone[mf.Name] = true
			}
			pruned = append(pruned, mf)
			continue
		}
		if !dryRun {
			if err := os.Remove(fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("provisioning: prune %s: %w", mf.Path, err)
			}
			pruneEmptyDirs(baseDir, mf.Path)
			if mf.Kind == "hook" && !hookSettingsDone[mf.Name] {
				switch hookProviderFromPath(mf.Path) {
				case "claude":
					if err := removeHookEntries(baseDir, mf.Name); err != nil {
						return nil, fmt.Errorf("provisioning: prune entry settings.json hook %s: %w", mf.Name, err)
					}
				case "codex":
					if err := removeHookConfigTOML(baseDir, mf.Name); err != nil {
						return nil, fmt.Errorf("provisioning: prune entry config.toml hook %s: %w", mf.Name, err)
					}
				case "opencode":
					if err := removeOpenCodePlugin(baseDir, mf.Name); err != nil {
						return nil, fmt.Errorf("provisioning: prune plugin opencode hook %s: %w", mf.Name, err)
					}
				}
				hookSettingsDone[mf.Name] = true
			}
		}
		pruned = append(pruned, mf)
	}
	return pruned, nil
}

// principalFile returns the conventional "main" file name of an artifact of the
// given kind, used only to simulate the Written path in DryRun (no file is
// actually read/written). "agent" has no principal file: it materializes to a
// single file directly (see Apply), never through this path.
func principalFile(kind string) string {
	switch kind {
	case "skill":
		return "SKILL.md"
	case "hook":
		return "hook.json"
	default:
		return ""
	}
}

// destination is one cell of the kind × provider matrix below: where an
// artifact of that kind materializes for that provider. A cell either names a
// destination or is explicitly unsupported — a *missing* cell is a
// programming error, caught by TestDestinationMatrixIsComplete, not silently
// degraded to Unsupported at apply time (D137).
type destination struct {
	// dir are the path segments, relative to the client base dir.
	dir []string
	// named marks a per-artifact destination: the artifact name is appended
	// to dir, with suffix (e.g. ".md") if any. When false, dir is the whole
	// path — one shared file for every artifact of that kind.
	named  bool
	suffix string
	// tail are extra segments appended AFTER the artifact name, for a
	// destination that nests the artifact's files one level deeper (D141:
	// skill-inbox/<name>/cartographer/, whose last segment names the
	// proposer so a second source never collides with Cartographer's).
	tail []string
	// unsupported marks a combination this provider cannot represent.
	unsupported bool
}

func at(segments ...string) destination { return destination{dir: segments} }

func perName(suffix string, segments ...string) destination {
	return destination{dir: segments, named: true, suffix: suffix}
}

// perNameIn is perName with extra segments below the artifact's own
// directory: dir/<name>/tail... .
func perNameIn(tail []string, segments ...string) destination {
	return destination{dir: segments, named: true, tail: tail}
}

var unsupportedDest = destination{unsupported: true}

// destinationMatrix is the kind × provider matrix documented in docs/sync.md,
// as data (D137). Every kind in artifactKinds must have a cell for every
// provider in configurator.Providers().
//
//   - "mcp"/"instructions" cells are one shared file per provider, the same
//     for every artifact of that kind (the name is not part of the path): each
//     MCP server occupies its own key/block inside that file (see
//     mcpsettings.go), and the instructions block concatenates all KBs
//     (applyInstructionsGroup).
//   - "skill"/"hook" cells are directories materialized by copyArtifactFiles;
//     "agent" cells are a single file, written directly by Apply.
//   - kiro has no known native subagent directory, and its hooks are declared
//     per agent rather than per machine (D140), so neither cell is
//     materializable — hence the two unsupported ones.
//   - hermes supports exactly one kind, "skill", and delivers it to an inbox
//     for the agent to adopt rather than installing it (D141). Its four other
//     cells are unsupported for stated reasons, not by omission.
var destinationMatrix = map[string]map[configurator.Provider]destination{
	"mcp": {
		configurator.ProviderClaudeCode: at(".claude.json"),
		configurator.ProviderCodex:      at(".codex", "config.toml"),
		configurator.ProviderOpenCode:   at("opencode.json"),
		configurator.ProviderKiro:       at(".kiro", "settings", "mcp.json"),
		// hermes: config.yaml is rendered by its Ansible role and recreated on
		// the next playbook run (D141).
		configurator.ProviderHermes: unsupportedDest,
	},
	"instructions": {
		configurator.ProviderClaudeCode: at(".claude", "CLAUDE.md"),
		configurator.ProviderOpenCode:   at(".config", "opencode", "AGENTS.md"),
		configurator.ProviderCodex:      at(".codex", "AGENTS.md"),
		configurator.ProviderKiro:       at(".kiro", "steering", "cartographer.md"),
		// hermes: SOUL.md, its always-on instruction slot, is operator-owned
		// and rendered from a template (D141).
		configurator.ProviderHermes: unsupportedDest,
	},
	"agent": {
		configurator.ProviderClaudeCode: perName(".md", ".claude", "agents"),
		configurator.ProviderOpenCode:   perName(".md", ".opencode", "agent"),
		configurator.ProviderCodex:      perName(".toml", ".codex", "agents"),
		configurator.ProviderKiro:       unsupportedDest,
		// hermes: no native subagent directory (D141).
		configurator.ProviderHermes: unsupportedDest,
	},
	"hook": {
		configurator.ProviderClaudeCode: perName("", ".claude", "hooks"),
		configurator.ProviderCodex:      perName("", ".codex", "hooks"),
		// Hook files (script + hook.json), same as for the other providers —
		// the JS plugin that invokes them (D59) is generated elsewhere (Apply,
		// registerOpenCodePlugin) in .config/opencode/plugins/, not here.
		configurator.ProviderOpenCode: perName("", ".opencode", "hooks"),
		// kiro: hooks are a `hooks` map inside an AGENT config
		// (~/.kiro/agents/<name>.json), not files in a directory of their own,
		// and they fire only for the agent that declares them — so no hook
		// Cartographer owns can fire for the agent the user actually runs
		// (D140). Its trigger is the scheduled timer.
		configurator.ProviderKiro: unsupportedDest,
		// hermes: no hook mechanism at all — nothing fires at conversation
		// start, so its trigger is the scheduled timer (D140/D141).
		configurator.ProviderHermes: unsupportedDest,
	},
	"skill": {
		configurator.ProviderClaudeCode: perName("", ".claude", "skills"),
		configurator.ProviderCodex:      perName("", ".codex", "skills"),
		configurator.ProviderKiro:       perName("", ".kiro", "skills"),
		configurator.ProviderOpenCode:   perName("", ".opencode", "skills"),
		// hermes: DELIVERED, not installed (D141). HERMES_HOME/skills/ belongs
		// to the agent's own curator, which rewrites what it owns; writing
		// there would destroy its learning. The proposal lands in the inbox
		// with a generated SOURCE.md and the agent adopts it via skill_manage.
		configurator.ProviderHermes: perNameIn([]string{hermesInboxSource}, hermesInboxRoot),
	},
}

// The hermes inbox (D141): <HERMES_HOME>/skill-inbox/<name>/cartographer/.
// The last segment names the proposer, so a proposal from another source never
// collides with Cartographer's. Deliberately without the timestamp the
// skill-inbox convention allows: provisioning must be idempotent, and one
// directory per sync would accumulate a copy every timer tick with no way to
// tell stale from current. SOURCE.md carries the content hash instead, so the
// agent can tell an unchanged re-delivery from a new proposal.
const (
	hermesInboxRoot   = "skill-inbox"
	hermesInboxSource = "cartographer"
	// hermesSourceFile is generated by Cartographer, never taken from the KB.
	hermesSourceFile = "SOURCE.md"
)

// artifactKinds are the kinds the matrix must cover. A manifest built by a
// newer server may carry a kind absent from this list; destDir returns "" for
// it, exactly as it does for an unsupported cell.
var artifactKinds = []string{"mcp", "instructions", "agent", "hook", "skill"}

// ManagedDestinationRoots returns the absolute paths, under baseDir, that
// provisioning writes into for this provider: one per artifact kind it
// supports, as destinationMatrix declares them, with the per-artifact name
// segment left off — the directory above it is what an operator links (e.g.
// ~/.claude/skills) and what a symlink would divert. Exported for
// `cartographer doctor`, which checks them without materializing anything (D148).
func ManagedDestinationRoots(provider configurator.Provider, baseDir string) []string {
	seen := map[string]bool{}
	var out []string
	for kind := range destinationMatrix {
		cell, ok := destinationMatrix[kind][provider]
		if !ok || cell.unsupported {
			continue
		}
		rel := filepath.Join(cell.dir...)
		if rel == "" || rel == "." {
			continue
		}
		abs := filepath.Join(baseDir, rel)
		if seen[abs] {
			continue
		}
		seen[abs] = true
		out = append(out, abs)
	}
	sort.Strings(out)
	return out
}

// destDir returns the destination path for an artifact, relative to the base-dir.
//
//   - kind "skill"/"hook": a directory, materialized by copyArtifactFiles with one
//     or more files inside.
//   - kind "agent": a single file path (agents are one-file-per-artifact: a Claude
//     subagent .md), materialized by writing its sole file's content directly to
//     that path (see Apply).
//
// Supported providers per kind are the destinationMatrix above; docs/sync.md
// §Kind × provider renders the same matrix for the reader. On claude/codex,
// Apply also registers a hook in the provider's own file (D57 settings.json,
// D58 config.toml — registerHookSettings/registerHookConfigTOML in
// hooksettings.go); on opencode it generates a whole plugin JS file instead
// (D59, registerOpenCodePlugin) when the hook's event has an OpenCode
// equivalent. All three are idempotent (re-apply never duplicates/drifts) and
// prunable (removed on diff.Removed/PruneManaged); see docs/sync.md §Hook.
// Agent content is translated per provider (D55/D58, see
// translateAgentForProvider); kiro never reaches that function because its
// "agent" cell is unsupported.
//
// Returns "" for an unsupported kind×provider combo — and likewise for a kind
// or provider this binary does not know — which causes Apply to place the
// artifact in Unsupported instead of materializing it.
func destDir(kind, name string, provider configurator.Provider) string {
	cell, ok := destinationMatrix[kind][provider]
	if !ok || cell.unsupported {
		return ""
	}
	if !cell.named {
		return filepath.Join(cell.dir...)
	}
	segments := append(append([]string{}, cell.dir...), name+cell.suffix)
	return filepath.Join(append(segments, cell.tail...)...)
}

// translateAgentForProvider adapts an "agent" artifact's content (a Claude Code
// subagent .md, see docs/sync.md §Agent e hook) for materialization under a
// specific provider (D55). Claude receives the content verbatim (native format).
// OpenCode has its own native subagent format (.opencode/agent/<name>.md,
// mode: subagent) which is *not* a superset of Claude's frontmatter: fields like
// `tools` (comma-separated, Claude-specific syntax), `model` (Claude model names)
// and `name` (OpenCode derives the agent name from the filename) are not
// reliably mappable and are dropped rather than guessed. Only `description`
// (required by OpenCode) is carried over.
//
// Parses the source frontmatter (if any) via okf.SplitFrontmatter/ParseFrontmatter,
// extracts description, and emits a minimal OpenCode frontmatter followed by the
// body verbatim. If the source has no frontmatter, the whole content becomes the
// body and description falls back to the agent name.
//
// codex has its own custom-agent TOML schema (D58, see translateAgentForCodex)
// — handled in a separate branch below. kiro never reaches here (destDir
// returns "" for kind "agent" on that provider, so Apply routes it to
// Unsupported before calling this function).
func translateAgentForProvider(provider configurator.Provider, name string, content []byte) ([]byte, error) {
	switch provider {
	case configurator.ProviderOpenCode:
		// falls through to the OpenCode translation below
	case configurator.ProviderCodex:
		return translateAgentForCodex(name, content)
	default:
		return content, nil
	}

	fmRaw, body, hasFM := okf.SplitFrontmatter(string(content))

	description := name
	if hasFM {
		fm, err := okf.ParseFrontmatter(fmRaw)
		if err != nil {
			return nil, fmt.Errorf("provisioning: parse frontmatter agent %s: %w", name, err)
		}
		if v, ok := fm.Get("description"); ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				description = s
			}
		}
	} else {
		body = string(content)
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("description: ")
	sb.WriteString(yamlQuoteScalar(description))
	sb.WriteString("\nmode: subagent\n")
	sb.WriteString("---\n")
	sb.WriteString(body)

	return []byte(sb.String()), nil
}

// translateAgentForCodex adapts an "agent" artifact's content (a Claude Code
// subagent .md) into Codex's custom-agent TOML schema (D58 —
// ~/.codex/agents/<name>.toml, see https://developers.openai.com/codex/subagents).
// Codex requires three fields: `name`, `description`, `developer_instructions`.
// `name` is the artifact name (must match the filename per Codex's own docs);
// `description` comes from the Claude frontmatter if present, falling back to
// the agent name (same policy as the OpenCode branch above); `developer_instructions`
// is the Markdown body, verbatim, as a TOML multi-line string. Fields with no
// reliable Codex equivalent — Claude's `tools` syntax, Claude-specific `model`
// names ("sonnet"/"opus"/"haiku" are not valid Codex model identifiers) — are
// dropped rather than guessed, same policy as translateAgentForProvider's
// OpenCode branch.
func translateAgentForCodex(name string, content []byte) ([]byte, error) {
	fmRaw, body, hasFM := okf.SplitFrontmatter(string(content))

	description := name
	if hasFM {
		fm, err := okf.ParseFrontmatter(fmRaw)
		if err != nil {
			return nil, fmt.Errorf("provisioning: parse frontmatter agent %s: %w", name, err)
		}
		if v, ok := fm.Get("description"); ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				description = s
			}
		}
	} else {
		body = string(content)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "name = %s\n", configurator.QuoteTOMLString(name))
	fmt.Fprintf(&sb, "description = %s\n", configurator.QuoteTOMLString(description))
	sb.WriteString("developer_instructions = ")
	sb.WriteString(configurator.QuoteTOMLMultiline(strings.TrimRight(body, "\n")))
	sb.WriteString("\n")

	return []byte(sb.String()), nil
}

// yamlQuoteScalar quotes a scalar value with double quotes (escaping backslashes,
// double quotes and newlines) if it contains characters that would otherwise break
// simple "key: value" YAML parsing (colons, newlines, leading/trailing
// whitespace, YAML indicator characters); returns it unquoted otherwise. Used only
// to serialize the OpenCode `description` field in translateAgentForProvider —
// values can come from an arbitrary (possibly multiline/quoted) Claude frontmatter.
func yamlQuoteScalar(s string) string {
	if s == "" {
		return `""`
	}
	needsQuote := s != strings.TrimSpace(s) ||
		strings.ContainsAny(s, ":#{}[]&*!|>'\"%@`\n")
	if !needsQuote {
		return s
	}
	escaped := strings.ReplaceAll(s, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
	return `"` + escaped + `"`
}

// singleArtifactContent returns the content of a single-file artifact (kind
// "agent"): a.Files[0].Content if populated (remote client via sync_pull),
// otherwise the sole file read via ReadArtifactFiles (local deployment, same
// filesystem). Errors if the artifact does not resolve to exactly one file.
func singleArtifactContent(a Artifact, opts ApplyOptions) ([]byte, error) {
	files := a.Files
	if len(files) == 0 {
		var err error
		files, err = ReadArtifactFiles(a, opts.BundleFS, opts.KBRoots)
		if err != nil {
			return nil, err
		}
	}
	if len(files) != 1 {
		return nil, fmt.Errorf("provisioning: artifact %s/%s: expected 1 file, got %d", a.Kind, a.Name, len(files))
	}
	return files[0].Content, nil
}

// copyArtifactFiles writes the artifact's files (skill or hook: one or more
// files inside a directory) into the absolute fullDestDir folder. It returns
// the paths relative to opts.BaseDir, the hash of what was actually written
// (after placeholder expansion, D75 WP3, and provenance stamping, D138 — this
// is the materialized hash, never the source hash ComputeDiff compares), and a
// non-fatal warning when the artifact could not be stamped. The content source
// is a.Files if populated (remote client via sync_pull, no filesystem shared
// with the server), otherwise BundleFS/KBRoots (local stdio deployment, same
// filesystem).
func copyArtifactFiles(a Artifact, opts ApplyOptions, fullDestDir string, tracker *expansionTracker) (relPaths []string, materializedHash, warning string, err error) {
	files := a.Files
	if len(files) == 0 {
		var err error
		files, err = ReadArtifactFiles(a, opts.BundleFS, opts.KBRoots)
		if err != nil {
			return nil, "", "", err
		}
	}

	// Files Cartographer generates on top of the KB's own (D141, hermes'
	// SOURCE.md). A collision with a KB file of the same name fails this one
	// artifact — with a warning, before anything is written — and leaves the
	// rest of the sync to complete.
	generated, collision, ok := generatedArtifactFiles(a, opts.Provider, files)
	if !ok {
		// The caller created fullDestDir just before this call: remove it
		// again so a refused delivery leaves no empty directory that looks
		// like one. os.Remove refuses a non-empty directory, so an earlier
		// successful delivery survives untouched.
		_ = os.Remove(fullDestDir)
		if rel, relErr := filepath.Rel(opts.BaseDir, fullDestDir); relErr == nil {
			pruneEmptyDirs(opts.BaseDir, rel)
		}
		return nil, "", collision, nil
	}
	files = append(append([]ArtifactFile{}, files...), generated...)

	expanded := make([]ArtifactFile, 0, len(files))
	// Only a skill's principal file carries the provenance block; every other
	// file in the folder is copied untouched (D138).
	stampTarget := ""
	if stampedKinds[a.Kind] {
		stampTarget = principalFile(a.Kind)
	}
	stamped := false
	for _, f := range files {
		// Paths can arrive from a remote server via sync_pull: reject
		// absolute paths and traversal outside the artifact's directory.
		local := filepath.FromSlash(f.Path)
		if !filepath.IsLocal(local) {
			return nil, "", "", fmt.Errorf("provisioning: invalid file path %q in %s", f.Path, a.Name)
		}
		content := expandPlaceholders(f.Content, opts, tracker)
		if stampTarget != "" && filepath.ToSlash(f.Path) == stampTarget {
			content = stampArtifact(content, a)
			stamped = true
		}
		// The materialized hash must describe what is on disk, so it records
		// the NORMALIZED mode the write below applies (D139): a hook's
		// hook.json is forced non-executable and its other files executable,
		// so hashing the source flag would report drift on the first check.
		expanded = append(expanded, ArtifactFile{Path: f.Path, Content: content, Executable: effectiveExecutable(a.Kind, local, f.Executable)})

		dstPath := filepath.Join(fullDestDir, local)
		if err := mkdirAllNoFollow(opts.BaseDir, filepath.Dir(dstPath), 0o755); err != nil {
			return nil, "", "", err
		}
		// A hook's scripts are invoked by path from the registered entry
		// (e.g. ./bootstrap.sh → absolute path in settings.json): without the
		// executable bit the hook fails with "Permission denied" on every SessionStart.
		// The explicit Chmod is needed for files that already exist: WriteFile only
		// applies mode on creation.
		mode := os.FileMode(0o644)
		if effectiveExecutable(a.Kind, local, f.Executable) {
			mode = 0o755
		}
		if err := writeFileNoFollow(dstPath, content, mode); err != nil {
			return nil, "", "", err
		}
		if err := os.Chmod(dstPath, mode); err != nil {
			return nil, "", "", err
		}
		rel, err := filepath.Rel(opts.BaseDir, dstPath)
		if err != nil {
			return nil, "", "", err
		}
		relPaths = append(relPaths, rel)
	}

	// A malformed KB artifact must not fail the whole sync: BuildManifest does
	// not require the principal file either.
	if stampTarget != "" && !stamped {
		warning = fmt.Sprintf("%s %q has no %s: materialized without a provenance block", a.Kind, a.Name, stampTarget)
	}

	// The hash of what actually landed on disk (D138), always computed: the
	// caller keeps a.ContentHash as the source hash for ComputeDiff.
	return relPaths, hashArtifactFiles(expanded), warning, nil
}

// ReadArtifactFiles reads all of an artifact's files from BundleFS (source "bundle",
// skill only: the bundle is skill-only) or from a KB in kbRoots (source "kb:<name>"),
// returning paths relative to the artifact's folder (or, for agents, just the
// filename) and content in memory. Used by copyArtifactFiles/singleArtifactContent
// for local deployment (same filesystem) and by the sync_pull MCP tool to
// serialize artifacts to a remote HTTP client with no shared filesystem.
func ReadArtifactFiles(a Artifact, bundleFS fs.FS, kbRoots map[string]string) ([]ArtifactFile, error) {
	// Content already in memory: use it directly instead of trying to read it from
	// the filesystem. Two cases: (a) kind "instructions" (D56) — the content is
	// GENERATED by BuildManifest, doesn't exist as a file on the KB, so there's
	// nothing to read for source/kind below; (b) an artifact already decoded by
	// sync_pull on the remote client side (no filesystem shared with the server).
	if len(a.Files) > 0 {
		return a.Files, nil
	}

	if a.Source == "bundle" {
		srcDir := "bundled/" + a.Name
		var files []ArtifactFile
		err := fs.WalkDir(bundleFS, srcDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel := path[len(srcDir)+1:]
			data, readErr := fs.ReadFile(bundleFS, path)
			if readErr != nil {
				return readErr
			}
			fileInfo, infoErr := d.Info()
			if infoErr != nil {
				return infoErr
			}
			files = append(files, ArtifactFile{Path: rel, Content: data, Executable: fileInfo.Mode()&0o111 != 0})
			return nil
		})
		if err != nil {
			return nil, err
		}
		return files, nil
	}

	// KB source: derive the KB name from "kb:<name>".
	kbName := strings.TrimPrefix(a.Source, "kb:")
	kbRoot, ok := kbRoots[kbName]
	if !ok {
		return nil, fmt.Errorf("KB %q not found in KBRoots", kbName)
	}

	switch a.Kind {
	case "agent":
		agentPath := filepath.Join(kbRoot, "agents", a.Name+".md")
		data, err := os.ReadFile(agentPath)
		if err != nil {
			return nil, fmt.Errorf("provisioning: read agent %s: %w", agentPath, err)
		}
		return []ArtifactFile{{Path: a.Name + ".md", Content: data}}, nil
	case "mcp":
		mcpPath := filepath.Join(kbRoot, "mcp", a.Name+".json")
		data, err := os.ReadFile(mcpPath)
		if err != nil {
			return nil, fmt.Errorf("provisioning: read mcp %s: %w", mcpPath, err)
		}
		return []ArtifactFile{{Path: a.Name + ".json", Content: data}}, nil
	case "hook":
		files, err := readDirFiles(filepath.Join(kbRoot, "hooks", a.Name))
		if err != nil {
			return nil, err
		}
		for i := range files {
			files[i].Executable = effectiveExecutable("hook", files[i].Path, files[i].Executable)
		}
		return files, nil
	default: // "skill"
		return readDirFiles(filepath.Join(kbRoot, "skills", a.Name))
	}
}

// readDirFiles walks srcDir and returns every file inside it as an
// ArtifactFile, with Path relative to srcDir (slash-separated). Factored out of
// ReadArtifactFiles because skill and hook share the same
// "directory with one or more files" schema.
func readDirFiles(srcDir string) ([]ArtifactFile, error) {
	var files []ArtifactFile
	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(srcDir, path)
		if relErr != nil {
			return relErr
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		fileInfo, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		files = append(files, ArtifactFile{Path: filepath.ToSlash(rel), Content: data, Executable: fileInfo.Mode()&0o111 != 0})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}
