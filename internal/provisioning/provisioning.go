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
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/artifactsig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/execbit"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/safewrite"
	"github.com/BeppeTemp/cartographer/internal/skill"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
	"gopkg.in/yaml.v3"
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

	// Restriction is what an "agent" artifact's source declares that a
	// non-Claude client cannot express (D283). Derived from the source content
	// by whoever builds the Artifact (BuildManifest, the client's sync_pull
	// decoding), never sent on the wire: nil for every other kind and for an
	// agent that declares no `tools`.
	Restriction *AgentRestriction `json:"-"`
}

// BuildOptions controls manifest construction. Signers is keyed by mounted KB
// name; a missing signer leaves that KB's artifacts unsigned.
type BuildOptions struct {
	Signers map[string]ed25519.PrivateKey
	// RoutedMount reports that this server serves its KBs through a single
	// routed endpoint (D187): the tools are advertised once, unprefixed, and
	// every call carries the KB as a `kb` argument. The generated instructions
	// block is the part the model actually reads, so it must name the tools as
	// the agent will see them and say which `kb` value to pass — a wrong name
	// there is worse than the duplication routing removes.
	RoutedMount bool
	// SkillDiagnostic receives one message per skill excluded from the
	// manifest because it failed validation (D191). Excluding it is what keeps
	// the rest of the KB syncing; saying so is what keeps the exclusion from
	// being silent.
	SkillDiagnostic func(string)
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
	// Issues carries the attributed reasons artifacts were left out of this
	// manifest (D191) — named KB, named artifact, named rule. It is not part of
	// the revision: excluding an artifact already changes that, and an issue
	// list that fed the hash would make a message edit look like a catalogue
	// change.
	Issues []string `json:"issues,omitempty"`
	// Skipped is client-side only: the reasons VerifiedManifest dropped
	// artifacts from this manifest (a "bundle" artifact that does not match the
	// bundle embedded in this client). Apply reports them as warnings.
	Skipped []string `json:"-"`
	// Placeholders is client-side only: the {{repo:…}}/{{path:…}} keys the
	// KBs feeding this manifest cite, "kind:key" -> KB names, as their
	// sync_pull responses listed them (D262). Like Issues it is outside the
	// revision and outside every signature: it is derived from concept bodies,
	// and a concept starting to cite a key is not a catalogue change. Nothing
	// that copies or filters a Manifest carries it — the client reads it off
	// the projection's manifest into ApplyOptions.Placeholders.
	Placeholders map[string][]string `json:"placeholders,omitempty"`
	// PathRegistries is client-side only, like Placeholders: each bound KB's
	// declared placeholder vocabulary (paths.yaml, D263), KB name -> registry
	// ("" for an unnamed endpoint), as its sync_pull listed it. Unsigned and
	// outside the revision; read off the projection's manifest into
	// ApplyOptions.PathRegistries.
	PathRegistries map[string]PathRegistry `json:"path_registries,omitempty"`
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
	// Source is the artifact's provenance ("kb:<name>" or "bundle"), recorded so
	// `status` can attribute a materialized file to the KB it came from and
	// `doctor` can spot one left behind by a KB that is no longer bound (D170).
	// Empty means "unknown": every lockfile written before D170. That is not
	// drift and never triggers re-materialization — ComputeDiff still compares
	// ContentHash only — so no migration runs.
	Source string `json:"source,omitempty"`
	// ExecutablePaths are the artifact-relative, slash-normalised paths this
	// artifact's files were written executable at — the writer's *declared*
	// intent, recorded because on a filesystem with no execute bit
	// (internal/execbit) it cannot be read back. It is the only state that
	// makes MaterializedHash reproducible there for a kind whose flag comes
	// from the KB rather than from the kind itself (a skill shipping a helper
	// script); for a hook the flag is a pure function of kind and path, so the
	// entry is redundant but recorded anyway rather than special-cased.
	//
	// Empty means "nothing was executable" and is indistinguishable from a
	// lockfile written before this field existed — deliberately, because both
	// answer the same way: readManagedFiles falls back to
	// effectiveExecutable(kind, path, false), which is exact for a hook and is
	// what every pre-existing lockfile already meant for every other kind. No
	// migration runs.
	ExecutablePaths []string `json:"executable_paths,omitempty"`
}

// Lock is the client's lockfile: applied revision + managed files.
// Written to <base-dir>/.cartographer-sync.lock.json.
type Lock struct {
	AppliedRevision string        `json:"applied_revision"`
	Provider        string        `json:"provider"`
	Managed         []ManagedFile `json:"managed"`
	// BaseDir is the directory the Path of every ManagedFile above is
	// relative to, recorded only for a workspace projection (D193: the
	// repository). Empty — a global projection, and every lockfile written
	// before D141 — means "the lockfile's own directory", so nothing migrates.
	// A lockfile written for a client since removed (D898) can carry it for a
	// global section too: the section is skipped, never resolved (clientconfig). Read it through
	// LockBaseDir, never directly: pruning or verifying against the wrong base
	// dir deletes the wrong files or silently finds nothing.
	BaseDir string `json:"base_dir,omitempty"`
	// ServerVersion is the version of the Cartographer server this state was
	// materialized against (D142). Empty means "unknown" — every lockfile
	// written before D142, and any sync that could not reach /health — and
	// never triggers the "the server changed" report: the first sync after an
	// upgrade must not tell every user something it cannot actually know.
	ServerVersion string `json:"server_version,omitempty"`
	// ProjectScope marks a lock whose managed paths were resolved through the
	// project-local half of the destination matrix (D193). Absent — every
	// lockfile written before D193, and every global projection — means the
	// global half. It is persisted rather than derived, because verifying or
	// pruning through the wrong matrix looks for the wrong paths: it would
	// report a correctly materialized workspace as missing, and leave real
	// files behind on a prune.
	ProjectScope bool `json:"project_scope,omitempty"`

	// The placeholder state of the last client-side Apply (D262), overwritten
	// by every Apply — never merged, so a key that stopped being cited or got
	// fixed disappears on the next sync. All three are keyed "kind:key".
	// Absent — every lockfile written before D262, every server-side Apply —
	// means "nothing recorded", which `status` and `paths` read as zero
	// unresolved: not knowing is not a failure, and no migration runs.
	//
	// ResolvedPlaceholders maps a key to the local path it resolved to;
	// UnresolvedPlaceholders maps a key to why it did not resolve;
	// PlaceholderSources maps a key to the KBs citing it (empty for a key only
	// a bundled artifact or an unnamed endpoint cites).
	ResolvedPlaceholders   map[string]string   `json:"resolved_placeholders,omitempty"`
	UnresolvedPlaceholders map[string]string   `json:"unresolved_placeholders,omitempty"`
	PlaceholderSources     map[string][]string `json:"placeholder_sources,omitempty"`
	// PlaceholderDecls maps a recorded key to the declaration its KB made in
	// paths.yaml (D263), so `paths list` and the connect step can show what a
	// key points at, and pre-fill its default, without a network call.
	// Absent — an older lockfile, a KB with no registry — means "nothing
	// declared", which is what those files meant.
	PlaceholderDecls map[string]PathDecl `json:"placeholder_decls,omitempty"`
	// PathsSectionHash is the hash of the placeholder section last written
	// into the instructions block (paragraph + "Local paths" table, D262). A
	// mismatch rewrites the block even when no artifact changed — the table
	// depends on concepts and on the local `paths:` map, neither of which
	// enters the manifest diff. Empty (older lockfile) triggers one rewrite,
	// which is what puts the paragraph into an existing block.
	PathsSectionHash string `json:"paths_section_hash,omitempty"`
	// SessionHookLimitAcked records that `connect`/`reconnect` has shown this
	// provider's SessionHookLimit (D300) to the operator, so `status` stops
	// repeating it on every run (D320). Absent — an older lockfile — means
	// "not shown yet", which keeps printing it, as before. Carried over by
	// every sync (clientsync), set only by connect; doctor still reports the
	// limitation every run.
	SessionHookLimitAcked bool `json:"session_hook_limit_acked,omitempty"`
}

// Scope returns the destination-matrix half this lock's paths were resolved
// through.
func (l Lock) ScopeOf() Scope {
	if l.ProjectScope {
		return ScopeProject
	}
	return ScopeGlobal
}

// LockBaseDir returns the directory lock's managed paths are relative to:
// lock.BaseDir when it records one (a workspace projection), defaultDir — the
// directory holding the lockfile — otherwise.
func LockBaseDir(lock Lock, defaultDir string) string {
	if lock.BaseDir != "" {
		return lock.BaseDir
	}
	return defaultDir
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
	// ApprovedMCP holds exact local approvals keyed as "kb:<source>\x00<ApprovalName>":
	// MCP descriptors, hooks and executable artifacts (D1141).
	// It is authorization, not verification; Signed is never mutated by it.
	ApprovedMCP map[string]string
	Lock        Lock // current lockfile

	// Scope selects which half of the destination matrix this apply writes
	// through (D193). The zero value is ScopeGlobal, so every existing caller
	// keeps the destinations it had; ScopeProject materializes into a bound
	// workspace's own project-local directories, with BaseDir set to that
	// workspace. Nothing else about Apply changes: the diff, the trust chain,
	// the placeholder expansion and the safepath refusals are the same.
	Scope Scope
	// CoOwnedPaths are managed paths (slash form) that another projection of
	// the same workspace still records (LockFile.CoOwnedPaths, D364). Apply
	// never prunes them: the file stays for the projection that owns it too.
	CoOwnedPaths map[string]bool

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
	// Placeholders are the keys to pre-resolve before anything is expanded,
	// "kind:key" -> the KBs citing it (D262): the ones the server listed for
	// the KBs bound to this projection, concept-only keys included. Apply
	// adds the keys found in the manifest's own artifacts, so the "Local
	// paths" table covers the whole KB and not only the artifacts this Apply
	// rewrites. Only read when ExpandPlaceholders is set.
	Placeholders map[string][]string
	// PathRegistries are the bound KBs' declared placeholder vocabularies
	// (D263), KB name -> registry. Merged in KBOrder (first KB wins on a
	// conflicting default or remote, with a warning); a declared remote is
	// what a repo key is looked up by, and a declared default is the fallback
	// after `paths:` and the repo index, used only if it exists. Only read
	// when ExpandPlaceholders is set.
	PathRegistries map[string]PathRegistry

	// NoHeal reports on-disk divergence (AppliedResult.Divergent) instead of
	// restoring it (D139). The escape hatch for someone deliberately
	// iterating on a materialized copy; off by default, because the default
	// must match what the D138 provenance stamp promises the reader.
	NoHeal bool

	// KBOrder, when non-empty, is the KB names in the order this provider's
	// explicit binding declares them (clientconfig.ClientBinding.KBs, D170) —
	// D182: the "instructions" block's per-KB sections follow this order
	// instead of alphabetical. A KB present in the manifest but absent from
	// KBOrder sorts alphabetically after the declared ones, so a section is
	// never dropped by an ordering change. Empty (no explicit binding, the
	// default meaning "every known KB") keeps the alphabetical fallback.
	KBOrder []string

	// InstructionsRel, when non-empty, is the project-scope file the
	// "instructions" block goes into, instead of the matrix cell (D1019): a
	// derived workspace binding must not edit a CLAUDE.md git tracks, so Claude
	// Code's block goes to CLAUDE.local.md, which it reads beside CLAUDE.md.
	// Only read for ScopeProject.
	InstructionsRel string

	// HookSettingsRel, when non-empty, is the project-scope settings file
	// Claude Code hooks are registered in, instead of .claude/settings.json:
	// a derived workspace binding never edits a settings.json git tracks, so
	// it registers in ClaudeLocalSettings (#1198). Only read for ScopeProject
	// and the claude provider.
	HookSettingsRel string
}

// claudeHookSettings is, for an Apply with HookSettingsRel, the settings
// files it strips Claude Code hooks from: HookSettingsRel, where it registers
// them, then settings.json unless git tracks it (or cannot say), so an entry an
// earlier version left there moves. nil for the default (registerHookSettings,
// removeHookEntries).
func claudeHookSettings(opts ApplyOptions) []string {
	if opts.Provider != configurator.ProviderClaudeCode || opts.Scope != ScopeProject || opts.HookSettingsRel == "" {
		return nil
	}
	if tracked, err := TrackedPaths(opts.BaseDir); err == nil && !tracked[claudeSettingsFiles[0]] {
		return []string{opts.HookSettingsRel, claudeSettingsFiles[0]}
	}
	return []string{opts.HookSettingsRel}
}

// instructionsDest is the file, relative to opts.BaseDir, the instructions
// block of this Apply lives in.
func instructionsDest(opts ApplyOptions) string {
	if opts.Scope == ScopeProject && opts.InstructionsRel != "" {
		return opts.InstructionsRel
	}
	return destDirScoped("instructions", "", opts.Provider, opts.Scope)
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
	// Relocated: artifacts rewritten at the provider's current destination
	// because the lockfile recorded them elsewhere (D360); their old files
	// are in Pruned. Distinct from Healed: nothing local was discarded.
	Relocated []ManagedFile
	// Divergent: on-disk divergence detected but NOT restored, because
	// opts.NoHeal was set.
	Divergent []DriftFinding
	// Warnings: non-fatal messages about individual aspects of materialization
	// that stay partial without failing Apply — today the only case is an
	// OpenCode hook whose KB event has no equivalent in OpenCode's plugin
	// engine (D59, see registerOpenCodePlugin in hooksettings.go): the hook's
	// files are materialized anyway, but no plugin is generated.
	Warnings []string
	// RegistryWarnings: two bound KBs declaring one placeholder key with a
	// different default or remote (D263). Kept apart from Warnings because
	// every projection bound to both KBs reports the same conflict, and the
	// caller prints each one once per sync, not once per provider.
	RegistryWarnings []string
	// PriorUnresolved: the keys the lock this Apply started from already
	// recorded as unresolved (D282). A sync reports in full only the keys
	// that are not here — new since the last sync — and summarizes the rest.
	PriorUnresolved []string
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
		exec := execbit.IsExecutable(info.Mode())
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
	artifacts := make([]Artifact, 0, len(m.Artifacts))
	var skipped []string
	for _, orig := range m.Artifacts {
		artifacts = append(artifacts, orig)
		a := &artifacts[len(artifacts)-1]
		a.Signed = false
		a.BuiltIn = false
		if a.Source == "bundle" {
			// Built-in means "embedded in this binary", verified locally: the
			// server's source label alone proves nothing. A mismatch is normal
			// version skew or a foreign artifact: skipped, not a failed sync.
			if a.Signature != nil {
				// A signature belongs to a KB artifact: this is a relabelled
				// one, and stays a hard failure as it always was.
				return Manifest{}, fmt.Errorf("provisioning: signature source mismatch for %s/%s", a.Kind, a.Name)
			}
			if !matchesEmbeddedBundle(*a) {
				// Version skew: a bundled skill this client also embeds is
				// replaced by the client's own copy, so it is not pruned.
				if own, ok := embeddedBundleArtifact(a.Kind, a.Name); ok {
					*a = own
					a.BuiltIn = true
					skipped = append(skipped, fmt.Sprintf("bundled skill %s: server and client bundles differ, installing this client's version", a.Name))
					continue
				}
				skipped = append(skipped, fmt.Sprintf("bundle artifact %s/%s from the server does not match this client's embedded bundle: skipped", a.Kind, a.Name))
				artifacts = artifacts[:len(artifacts)-1]
				continue
			}
			// Even on a match the files installed are this client's own, never
			// the ones the server carried (or fetches later).
			own, _ := embeddedBundleArtifact(a.Kind, a.Name)
			*a = own
			a.BuiltIn = true
			continue
		}
		if a.Signature == nil {
			// A pinned KB signs every artifact it ships (BuildManifest signs each
			// kb: artifact, instructions and mcp included), so an unsigned one
			// under a pin is a stripped signature, not a legacy artifact: fail
			// closed (D906).
			if strings.HasPrefix(a.Source, "kb:") && len(pins[strings.TrimPrefix(a.Source, "kb:")]) > 0 {
				return Manifest{}, fmt.Errorf("provisioning: KB %q has a pinned signing key but %s/%s carries no signature", strings.TrimPrefix(a.Source, "kb:"), a.Kind, a.Name)
			}
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
	return Manifest{Revision: m.Revision, GeneratedAt: m.GeneratedAt, Artifacts: artifacts, Skipped: skipped}, nil
}

var (
	embeddedBundleOnce      sync.Once
	embeddedBundleArtifacts map[string]Artifact
)

// embeddedBundleArtifact returns this client's own artifact for a bundled
// skill, built by BuildManifest from the embedded bundle (files included).
func embeddedBundleArtifact(kind, name string) (Artifact, bool) {
	embeddedBundleOnce.Do(func() {
		embeddedBundleArtifacts = map[string]Artifact{}
		m, err := BuildManifest(skillbundle.FS, nil, BuildOptions{})
		if err != nil {
			return
		}
		for _, a := range m.Artifacts {
			if a.Source != "bundle" {
				continue
			}
			files, err := ReadArtifactFiles(a, skillbundle.FS, nil)
			if err != nil {
				continue
			}
			a.Files = files
			embeddedBundleArtifacts[a.Kind+"\x00"+a.Name] = a
		}
	})
	a, ok := embeddedBundleArtifacts[kind+"\x00"+name]
	return a, ok
}

// matchesEmbeddedBundle reports whether an artifact labelled "bundle" is this
// client's embedded artifact: same kind, name and content hash, and, when files
// are carried, files that hash to it.
func matchesEmbeddedBundle(a Artifact) bool {
	own, ok := embeddedBundleArtifact(a.Kind, a.Name)
	if !ok || own.ContentHash != a.ContentHash {
		return false
	}
	return len(a.Files) == 0 || ContentHashFiles(a.Files) == a.ContentHash
}

// ContentHashDirOSForKind is contentHashDirOS exported for callers that must
// reproduce a materialized artifact's hash — the client's own status check
// and its tests (D139). It derives the executable flag from the kind and the
// filesystem alone: on a filesystem with no execute bit that is exact for a
// hook and blind for every other kind, so a caller verifying a *lockfile* entry
// there wants contentHashDirManaged instead, which reads the recorded intent.
func ContentHashDirOSForKind(dirPath, kind string) (string, error) {
	return contentHashDirOS(dirPath, kind)
}

func contentHashDirOS(dirPath, kind string) (string, error) {
	return contentHashDir(os.DirFS(dirPath), ".", func(path string, executable bool) bool {
		return effectiveExecutable(kind, path, executable)
	})
}

// contentHashDirManaged is contentHashDirOS for a directory that a lockfile
// entry describes: the executable flag is resolved through mf, so it survives a
// filesystem that cannot report one (see ManagedFile.executableOnDisk).
func contentHashDirManaged(mf ManagedFile, dirPath string) (string, error) {
	return contentHashDir(os.DirFS(dirPath), ".", func(path string, executable bool) bool {
		return mf.executableOnDisk(path, executable)
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
	var issues []string

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

		// 2. Skill (skills/<name>/SKILL.md, several files per artifact). A KB with
		// no skills/ folder or an unreadable directory: kbSkills is empty,
		// silent skip (no fatal error).
		kbSkills, loadErrs := skill.LoadAllSkills(kbRoot)
		var skillNames []string // the valid ones, for the brief instructions section (D1182)
		for _, loadErr := range loadErrs {
			// Previously discarded. An unreadable SKILL.md is not fatal — the
			// rest of the KB must still sync — but it must not be invisible
			// either (D191).
			issue := fmt.Sprintf("KB %q: skill not loaded: %v", kbName, loadErr)
			issues = append(issues, issue)
			if opts.SkillDiagnostic != nil {
				opts.SkillDiagnostic(issue)
			}
		}
		for _, s := range kbSkills {
			// One validator for both channels (D191). An error-severity issue
			// excludes this skill from the manifest and nothing else: a KB with
			// one malformed skill still syncs its valid artifacts, where before
			// the mismatch reached ReadArtifactFiles and could take sync_pull
			// down for the whole KB.
			if bad := skill.FirstError(skill.Validate(&s)); bad != nil {
				issue := fmt.Sprintf("KB %q: skill %q excluded (%s): %s", kbName, s.DirPath, bad.Rule, bad.Message)
				issues = append(issues, issue)
				if opts.SkillDiagnostic != nil {
					opts.SkillDiagnostic(issue)
				}
				continue
			}
			skillNames = append(skillNames, s.Name)
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
				agent := Artifact{
					Kind:        "agent",
					Name:        name,
					Source:      "kb:" + kbName,
					ContentHash: hash,
				}
				if raw, rerr := os.ReadFile(filepath.Join(agentsDir, e.Name())); rerr == nil {
					agent.Restriction = ParseAgentRestriction(raw)
				}
				artifacts = append(artifacts, agent)
			}
		}

		// 4. Hook (hooks/<name>/, script + hook.json — aggregate hash like
		// multi-file skills). No hooks/ folder → zero hook artifacts. A KB
		// that named a hook BootstrapHookName ("cartographer-bootstrap", D60) or
		// WriteFindingsHookName ("cartographer-write-findings", D353) is
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
		// descriptor needs its own hash-bound approval, signed or not (D906). A
		// D114 signature remains independently verifiable.
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
				_, specErr := parseMCPServerSpec(name, data)
				if specErr != nil {
					return Manifest{}, fmt.Errorf("provisioning: %w", specErr)
				}
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
		// 6. Instructions (kind "instructions", D56): an "imprinting" artifact
		// per KB, always present (unlike skill/agent/hook it doesn't depend on
		// an optional folder) — tells the LLM agent that this KB exists and how
		// to use it. Unlike other kinds, the content doesn't live on disk: it's
		// GENERATED by generateKBInstructions (a pure, deterministic function, no
		// timestamp) and placed directly in Artifact.Files, so ReadArtifactFiles
		// doesn't need to read anything from the KB for this kind (see its
		// "len(a.Files) > 0" check at the top of the function).
		instrContent := []byte(generateKBInstructions(kbName, kbRoot, opts.RoutedMount, skillNames))
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
	m := MergeArtifacts(artifacts)
	m.Issues = issues
	return m, nil
}

// contentHashBytes computes a deterministic sha256 hex of raw data, with no
// filename prefix — used for artifacts whose content is generated in memory
// instead of read from a file with its own name (kind "instructions", D56).
func contentHashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

// generateKBInstructions generates the content (in English) of the "instructions"
// imprinting artifact for the KB kbName rooted at kbRoot (D56): the BRIEF
// section of the managed block (D1182). It tells the LLM agent that this KB
// exists, lists the names of its top-level archives (§KBArchiveNames) and the
// names of its valid skills (skills, capped, §skillsSentence). The operational
// bullets are written once per block by applyInstructionsGroup, and the optional
// curated file <kbRoot>/instructions.md (§KBInstructionsFile, D61) joins the
// section only when the KB opts in with `block: full`; otherwise it is served
// on demand by kb_guide.
//
// Agent names are NOT listed here: D61 emitted them, D154 moved the subagent
// sentence to applyInstructionsGroup so it can be written per provider (see the
// note in the body).
//
// The block is stable imprinting, not state: no page counts (D65) or
// timestamps, archives and skills sorted by name — so the ContentHash (computed
// on the result of this function, see BuildManifest) changes only when the
// set of archives/skills or the opt-ins change, not on every page added.
func generateKBInstructions(kbName, kbRoot string, routed bool, skills []string) string {
	var sb strings.Builder

	// D1182: the brief section. Everything else (the operational bullets, the
	// KB's own directives, the skill descriptions) is not paid on every model
	// call: the bullets are written once per block by applyInstructionsGroup and
	// the rest comes from kb_guide.
	curated, d := KBInstructionsFile(kbRoot)

	if d.SkipPreamble {
		sb.WriteString(preambleNoneComment + "\n")
	}
	sb.WriteString(routingSentence(kbName, routed))
	sb.WriteString(archivesSentence(KBArchiveNames(kbRoot), false))
	sb.WriteString(skillsSentence(skills))

	// The subagent sentence is NOT emitted here (D154). This function has no
	// provider argument and BuildManifest only ever runs server-side, so the
	// list could only be what the KB *declares* — and a provider whose "agent"
	// cell is unsupported receives none of them, while being told to delegate to
	// them. applyInstructionsGroup appends the sentence per provider instead,
	// the same way the D75 WP4 paths table is appended client-side.

	if d.Full && curated != "" {
		sb.WriteString("\n")
		sb.WriteString(scopeSentence(kbName))
		sb.WriteString(curated)
		sb.WriteString("\n")
	}

	return sb.String()
}

// preambleNoneRe and blockFullRe match the opt-in/opt-out directives on the
// LEADING lines of instructions.md (D154, D1182). Leading run only, and an exact
// spelling, so a KB can document a directive in its own prose without
// triggering it — the same trap as the placeholder syntax (D162).
var (
	preambleNoneRe = regexp.MustCompile(`(?i)^<!--\s*cartographer:\s*preamble:\s*none\s*-->\s*$`)
	blockFullRe    = regexp.MustCompile(`(?i)^<!--\s*cartographer:\s*block:\s*full\s*-->\s*$`)
)

// KBCuratedInstructions reads the optional curated file
// <kbRoot>/instructions.md (D61) and reports whether it opted out of the
// generated "Operational instructions" bullets (D154).
//
// The file holds free-form markdown orchestration directives hand-written by
// the operator, included in the block AFTER the auto-generated part. It lives
// in the ROOT of the KB, alongside data/, skills/, agents/ — never inside
// data/, so it isn't scanned by kb.Walk/the indexes (it doesn't become a
// concept) and isn't materialized as a file of its own: only its content flows
// into the generated text. If it has frontmatter (tolerated but not required
// for a file meant as free-form markdown) it is discarded and only the body is
// used. Missing file → empty string (no section).
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
func KBCuratedInstructions(kbRoot string) (body string, skipPreamble bool) {
	body, d := KBInstructionsFile(kbRoot)
	return body, d.SkipPreamble
}

// InstructionsDirectives are the opt-ins a KB declares on the leading lines of
// its instructions.md (D154, D1182).
type InstructionsDirectives struct {
	// SkipPreamble is `preamble: none`: no generated operational text.
	SkipPreamble bool
	// Full is `block: full`: the curated body stays in the managed block
	// instead of being served by kb_guide.
	Full bool
}

// KBInstructionsFile is KBCuratedInstructions plus every directive of the
// leading run (D1182). The run is the lines at the top of the file that are a
// directive, one per line in any order, blank lines tolerated between them; it
// ends at the first line that is not one, and a directive after that (or in
// prose) is just text (D162). The directive lines are stripped from the body.
func KBInstructionsFile(kbRoot string) (body string, d InstructionsDirectives) {
	data, cut, err := readInstructionsBounded(filepath.Join(kbRoot, "instructions.md"))
	if err != nil {
		return "", d
	}
	_, content, hasFM := okf.SplitFrontmatter(string(data))
	if !hasFM {
		content = string(data)
		if cut > 0 && strings.HasPrefix(content, "---") {
			// The cut fell inside the frontmatter: there is no body in the
			// kept prefix, and the half-read keys must not pass as text.
			content = ""
		}
	}
	lines := strings.Split(content, "\n")
run:
	for len(lines) > 0 {
		t := strings.TrimSpace(lines[0])
		switch {
		case t == "":
		case preambleNoneRe.MatchString(t):
			d.SkipPreamble = true
		case blockFullRe.MatchString(t):
			d.Full = true
		default:
			break run
		}
		lines = lines[1:]
	}
	body = strings.TrimSpace(strings.Join(lines, "\n"))
	if cut > 0 {
		sep := "\n\n"
		if body == "" {
			sep = ""
		}
		body += fmt.Sprintf(sep+"[instructions.md was cut at %d bytes: %d bytes left out. The operator must shorten the file.]", len(data), cut)
	}
	return body, d
}

// MaxInstructionsBytes caps the curated instructions.md (D1110): the handshake
// guide and the managed block read at most this much of it, and artifact_write
// refuses a larger body. A push can still bring a bigger file into the KB; the
// readers then truncate it.
const MaxInstructionsBytes = 64 * 1024

// readInstructionsBounded reads at most MaxInstructionsBytes+1 bytes of the
// file. Past the cap it returns the text cut at the last line boundary within
// the cap and the number of bytes left out (0 when nothing was cut).
//
// A symlinked instructions.md is refused (same outcome as a missing file): a
// file outside the KB must reach neither the guide nor the managed block.
func readInstructionsBounded(path string) (data []byte, cut int64, err error) {
	if fi, lerr := os.Lstat(path); lerr != nil {
		return nil, 0, lerr
	} else if fi.Mode()&os.ModeSymlink != 0 {
		return nil, 0, fmt.Errorf("instructions.md is a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	data, err = io.ReadAll(io.LimitReader(f, MaxInstructionsBytes+1))
	if err != nil {
		return nil, 0, err
	}
	if len(data) <= MaxInstructionsBytes {
		return data, 0, nil
	}
	total := int64(len(data))
	if fi, statErr := f.Stat(); statErr == nil && fi.Size() > total {
		total = fi.Size()
	}
	if i := bytes.LastIndexByte(data[:MaxInstructionsBytes], '\n'); i >= 0 {
		data = data[:i+1]
	} else {
		// No line boundary: back off to a rune boundary, never split one.
		n := MaxInstructionsBytes
		for n > 0 && !utf8.RuneStart(data[n]) {
			n--
		}
		data = data[:n]
	}
	return data, total - int64(len(data)), nil
}

// directiveRe matches a session-global directive declaration line (D183):
// "<!-- cartographer:directive:<key>:<value> -->", matched as a full line
// (after trimming) so a KB can document the syntax in its own prose without
// declaring anything — the same defensive shape as preambleNoneRe and D182's
// "cartographer:kb:<name>:begin/end" markers, and the same D162 metasyntax
// trap both of those had to account for. Unlike preambleNoneRe, a directive
// may appear anywhere in the curated body, not only the first line.
//
// <key> and <value> are each required to be non-empty and to contain no ':',
// so the split stays unambiguous — a line with an extra ':' inside either
// part, or an empty key/value, is simply not recognised: strict rather than
// clever. <key> additionally excludes whitespace (it names a machine token,
// like "language" or "timezone"); <value> may contain internal spaces.
var directiveRe = regexp.MustCompile(`(?i)^<!--\s*cartographer:\s*directive:\s*([^:\s]+):([^:]+?)\s*-->\s*$`)

// directiveFenceMarkers are the code-fence delimiters extractDirectives
// recognises (pragmatic CommonMark subset) — the same pair
// okf.headingEligibleLines uses for the identical "documenting the syntax"
// concern with markdown headings. Kept local rather than exported from okf:
// this is the only caller, and the two scanners have no other reason to
// share code.
var directiveFenceMarkers = [...]string{"```", "~~~"}

// directiveFenceOpenMarker returns the fence marker ("```" or "~~~") if the
// trimmed line opens a code fence, or "" otherwise.
func directiveFenceOpenMarker(trimmed string) string {
	for _, m := range directiveFenceMarkers {
		if strings.HasPrefix(trimmed, m) {
			return m
		}
	}
	return ""
}

// extractDirectives scans every line of content — the generated content of a
// KB's "instructions" artifact, curated body included — for directive
// declarations and returns the key→value pairs found. Only the curated body
// can ever match: the generated wrapper text around it (the routing
// sentence, the operational bullets, the D182 scope sentence) is fixed,
// hardcoded English prose that never takes the "<!-- cartographer:...-->"
// shape, so scanning the whole artifact content is equivalent to scanning
// only the curated section.
//
// Lines inside a fenced code block are never eligible: an operator
// documenting the marker's syntax with a worked example — the marker alone
// on its own line inside a ``` fence, the natural way to show it — must not
// declare anything either. Full-line matching alone only protects a marker
// embedded mid-paragraph (D162's metasyntax trap); a standalone example line
// inside a fence needs this additional check.
//
// A key declared twice within the SAME content keeps its LAST value,
// silently: there is only one author to ask, so there is nothing to
// adjudicate the way there is between two KBs (DetectDirectiveCollisions).
func extractDirectives(content string) map[string]string {
	var out map[string]string
	inFence := false
	fenceMarker := ""
	for _, l := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(l)
		if inFence {
			if strings.HasPrefix(trimmed, fenceMarker) {
				inFence = false
			}
			continue
		}
		if marker := directiveFenceOpenMarker(trimmed); marker != "" {
			inFence = true
			fenceMarker = marker
			continue
		}
		m := directiveRe.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}
		key, value := m[1], strings.TrimSpace(m[2])
		if key == "" || value == "" {
			continue
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[key] = value
	}
	return out
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

// Collision is one kind+name claimed by two or more KBs (D171). Sources are the
// full source strings ("kb:<name>"), sorted, so a report is deterministic.
type Collision struct {
	Kind    string
	Name    string
	Sources []string
}

// DetectCollisions reports every kind+name claimed by two or more distinct KBs.
//
// The bundle never participates: a kind+name held by one KB and by the bundle is
// the deliberate override MergeArtifacts resolves in the KB's favour, and the
// bundle is single, so that choice is unambiguous. Two KBs are a different
// matter — preferArtifact picks the alphabetically first source, silently, and
// nothing downstream records that the other copy ever existed. Worse, the choice
// is unstable: unmounting the winning KB makes the losing one appear, changing
// what an agent reads with nothing announcing it.
//
// The same source appearing twice is not a collision. That is the ordinary
// shape of the client's merge, where every per-KB sync_pull response carries the
// same bundled artifacts.
func DetectCollisions(artifacts []Artifact) []Collision {
	sourcesByKey := make(map[string]map[string]bool)
	for _, a := range artifacts {
		if !strings.HasPrefix(a.Source, "kb:") {
			continue
		}
		k := a.Kind + "\x00" + a.Name
		if sourcesByKey[k] == nil {
			sourcesByKey[k] = make(map[string]bool)
		}
		sourcesByKey[k][a.Source] = true
	}

	var out []Collision
	for key, sources := range sourcesByKey {
		if len(sources) < 2 {
			continue
		}
		kind, name, _ := strings.Cut(key, "\x00")
		names := make([]string, 0, len(sources))
		for source := range sources {
			names = append(names, source)
		}
		sort.Strings(names)
		out = append(out, Collision{Kind: kind, Name: name, Sources: names})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// DirectiveValue pairs one declared value of a session-global directive with
// the KB source that declared it ("kb:<name>").
type DirectiveValue struct {
	Value  string
	Source string
}

// DirectiveCollision is one session-global directive key (D183, the
// "<!-- cartographer:directive:<key>:<value> -->" marker recognised by
// directiveRe) declared with two or more different values among the KBs
// compared. Values are sorted by Source, so a report is deterministic.
type DirectiveCollision struct {
	Key    string
	Values []DirectiveValue
}

// DetectDirectiveCollisions reports every session-global directive key
// declared with two or more different values among the given artifacts'
// kind "instructions" entries.
//
// Scoped the same way DetectCollisions is: it inspects exactly the artifacts
// it is given, and every caller already narrows that slice to one provider's
// bound KBs before calling MergeArtifactsStrict (D170/D171) — two KBs that
// never reach the same client cannot collide on a directive either, for the
// same reason they cannot collide on kind+name.
//
// Same key + same value across two (or more) KBs is not reported: they
// agree, there is nothing to adjudicate. Same key + different value is —
// consistent with D171's "error, not warning" stance for the structural
// kind+name collision.
func DetectDirectiveCollisions(artifacts []Artifact) []DirectiveCollision {
	// key -> source -> value
	bySource := make(map[string]map[string]string)
	for _, a := range artifacts {
		if a.Kind != "instructions" || !strings.HasPrefix(a.Source, "kb:") || len(a.Files) == 0 {
			continue
		}
		for key, value := range extractDirectives(string(a.Files[0].Content)) {
			if bySource[key] == nil {
				bySource[key] = make(map[string]string)
			}
			bySource[key][a.Source] = value
		}
	}

	var out []DirectiveCollision
	for key, bySrc := range bySource {
		values := make(map[string]bool, len(bySrc))
		for _, v := range bySrc {
			values[v] = true
		}
		if len(values) < 2 {
			continue
		}
		dvs := make([]DirectiveValue, 0, len(bySrc))
		for source, value := range bySrc {
			dvs = append(dvs, DirectiveValue{Value: value, Source: source})
		}
		sort.Slice(dvs, func(i, j int) bool { return dvs[i].Source < dvs[j].Source })
		out = append(out, DirectiveCollision{Key: key, Values: dvs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// CollisionError reports the collisions that stopped a merge — structural
// (kind+name, D171) and/or directive (key+value, D183). It renders as a
// multi-line report rather than one line: a caller printing "Error: <err>" must
// still produce something an operator can act on without a second command.
type CollisionError struct {
	Collisions []Collision
	Directives []DirectiveCollision
}

func (e *CollisionError) Error() string {
	var sb strings.Builder
	if len(e.Collisions) > 0 {
		sb.WriteString("the same artifact is claimed by more than one KB:\n")
		for _, c := range e.Collisions {
			fmt.Fprintf(&sb, "  %s/%s: claimed by %s\n", c.Kind, c.Name, strings.Join(c.Sources, ", "))
		}
		sb.WriteString("rename the artifact in all but one of those KBs, or stop binding them to the same client\n")
	}
	if len(e.Directives) > 0 {
		sb.WriteString("the same session-global directive key is declared with different values by more than one KB:\n")
		for _, d := range e.Directives {
			parts := make([]string, len(d.Values))
			for i, v := range d.Values {
				parts[i] = fmt.Sprintf("%s=%q by %s", d.Key, v.Value, v.Source)
			}
			fmt.Fprintf(&sb, "  %s: %s\n", d.Key, strings.Join(parts, ", "))
		}
		sb.WriteString("change the directive's value to agree in all but one of those KBs, or stop binding them to the same client\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// MergeArtifactsStrict is MergeArtifacts with the KB↔KB collision — structural
// (kind+name, D171) or directive (key+value, D183) — turned into an error
// instead of an alphabetical coin flip or a silently unresolved disagreement.
// It is what the client uses: a warning about an artifact or a directive the
// agent then actually reads is worse than a failed sync, because at that
// point the wrong answer is silent.
//
// BuildManifest keeps using MergeArtifacts: server-side a manifest is built for
// one KB plus the bundle, where a KB↔KB collision cannot arise by construction.
func MergeArtifactsStrict(artifacts []Artifact) (Manifest, error) {
	collisions := DetectCollisions(artifacts)
	directives := DetectDirectiveCollisions(artifacts)
	if len(collisions) > 0 || len(directives) > 0 {
		return Manifest{}, &CollisionError{Collisions: collisions, Directives: directives}
	}
	return MergeArtifacts(artifacts), nil
}

// preferArtifact chooses, between two artifacts with the same kind+name, the one
// that wins in materialization. The KB ("kb:*") beats the bundle; for the same
// source type, the choice is deterministic by alphabetical source. Between two
// KBs this is a silent, unstable choice, which is why the client merges through
// MergeArtifactsStrict and never reaches this branch (D171).
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
	// Workspaces holds the per-workspace applied state (D193), keyed by the
	// workspace's canonical path. It is a SEPARATE namespace from Providers on
	// purpose: prune deletes every managed file its lock names, so a key that
	// could resolve to the wrong projection would delete another workspace's
	// files. Two maps make that mistake unrepresentable rather than unlikely.
	// Absent — every lockfile written before D193 — means "no workspace
	// projection", and the global one is untouched.
	Workspaces map[string]WorkspaceLocks `json:"workspaces,omitempty"`
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
	// BootstrapHookName, and the write-findings hook of D353) is excluded: it's a client-side artifact, never present in
	// the server manifest by construction — treating it as "removed" would make
	// it disappear on every sync instead of staying stable between one
	// EnsureBootstrapHook and the next (see also the twin carry-forward in Apply).
	for _, mf := range lock.Managed {
		if isClientOwned(mf) {
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
	var out Manifest
	for _, a := range m.Artifacts {
		if destDir(a.Kind, a.Name, provider) != "" && !strictAgentSkipped(a, provider) {
			out.Artifacts = append(out.Artifacts, a)
		}
	}
	// The revision describes the artifacts, so it must be recomputed rather
	// than inherited (D170). Two providers receiving different sets under one
	// revision string make ComputeDiff.InSync lie, and — once bindings decide
	// those sets — changing a binding would not change the revision, so no
	// drift would ever be detected. Filtering preserves relative order, and m
	// arrives sorted from MergeArtifacts, so computeRevision's precondition
	// still holds.
	out.Revision = computeRevision(out.Artifacts)
	// Skipped carries the warnings VerifiedManifest left for Apply to
	// print; a real sync applies the filtered manifest, so they must survive.
	out.Skipped = m.Skipped
	return out
}

// SelectForSources keeps the artifacts a provider bound to `allowed` may
// receive (D170).
//
// It must be applied to the per-KB sync_pull responses, BEFORE the merge, and
// callers must pass only the responses of the allowed KBs. Filtering the merged
// manifest instead is wrong: MergeArtifacts has already discarded candidates,
// so if `kb-A` overrides a bundled skill and the provider is bound only to
// `kb-B`, the merge keeps `kb-A`'s copy and a source filter then deletes it —
// the provider loses the skill instead of receiving the bundled one. The server
// performs the same KB-over-bundle merge inside each single-KB pull, so a
// candidate the client never received cannot be reconstructed.
//
// Given that, the check here is a local guarantee rather than the load-bearing
// one: a bundled artifact reaching this function arrived in an allowed KB's
// response by construction, and a `kb:` artifact from outside `allowed` would
// mean the server answered for a KB that was not asked for.
//
// `allowed` is always explicit — nil is "no KBs", never a wildcard. Callers
// resolve the default through clientconfig.Config.BoundKBs.
func SelectForSources(candidates []Artifact, allowed []string) []Artifact {
	permitted := make(map[string]bool, len(allowed))
	for _, kb := range allowed {
		permitted["kb:"+kb] = true
	}
	out := make([]Artifact, 0, len(candidates))
	for _, a := range candidates {
		if a.Source == "bundle" || permitted[a.Source] {
			out = append(out, a)
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

// Apply materializes built-in and KB artifacts into BaseDir, prunes stale managed
// entries and writes the updated lockfile.
//
// Safety rules:
//   - BuiltIn:true or a KB-sourced artifact, signed or not → copy/update into
//     BaseDir for the given provider (D906: the client has no trust switch; what
//     a KB ships is installed, after the pull's signature and path checks).
//   - an MCP descriptor → only with a local approval bound to its content hash
//     (ApprovedMCP), signed or not; otherwise NeedsApproval.
//   - a KB hook, or any KB artifact shipping an executable file (D1141) → the
//     same approval, and a signature does not count, pinned key or not (it
//     proves the origin, never consent); otherwise NeedsApproval and what is
//     installed stays untouched.
//
// Providers supported for direct materialization:
//   - claude: skill in .claude/skills/<name>/, agent in .claude/agents/<name>.md,
//     hook in .claude/hooks/<name>/ — and registered/updated in
//     .claude/settings.json (D57, see registerHookSettings)
//   - opencode: skill in .config/opencode/skills/<name>/, agent in .config/opencode/agents/<name>.md
//     (D55 — translated content, see translateAgentForProvider, not verbatim);
//     hook in .opencode/hooks/<name>/ — and, if the event has an equivalent in
//     OpenCode's plugin engine, a generated JS plugin in
//     .config/opencode/plugins/cartographer-<name>.js (D59, see
//     registerOpenCodePlugin/generateOpenCodePlugin in hooksettings.go).
//   - codex: skill in .codex/skills/<name>/; agent in .codex/agents/<name>.toml
//     (D58 — content translated into Codex's subagent schema, see
//     translateAgentForProvider); hook in .codex/hooks/<name>/ — and registered
//     in .codex/hooks.json (D230, see registerCodexHook in hooksettings.go).
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
	// Every key the bound KBs cite is resolved up front (D262), through one
	// resolver that walks the search roots at most once, so the "Local paths"
	// table covers concept-only keys and artifacts that are not rewritten in
	// this run.
	var sources map[string][]string
	if opts.ExpandPlaceholders {
		var declaredBy map[string][]string
		var conflicts []string
		tracker.decls, declaredBy, conflicts = MergePathRegistries(opts.KBOrder, opts.PathRegistries)
		result.RegistryWarnings = conflicts
		sources = placeholderSources(m, opts)
		ids := make([]string, 0, len(sources))
		for id := range sources {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		tracker.preResolve(ids, opts)
		tracker.resolveDeclared(sources, declaredBy, opts)
	}

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
	// Relocation (D360): an artifact still in the manifest whose recorded files
	// sit outside the provider's current destination (the destination moved in
	// a release) is rewritten at the new one; the old files leave through the
	// orphan path below. Decided before the heal loop so a moved skill is not
	// also reported as "missing" at its new path.
	relocated := make(map[string]bool)
	for _, a := range m.Artifacts {
		key := a.Kind + "\x00" + a.Name
		if toWriteKeys[key] || !artifactAuthorized(a, opts) {
			continue
		}
		destRel := destDirScoped(a.Kind, a.Name, opts.Provider, opts.Scope)
		if len(movedArtifactFiles(opts.Lock.Managed, a, destRel)) == 0 {
			continue
		}
		toWrite = append(toWrite, a)
		toWriteKeys[key] = true
		relocated[key] = true
	}
	// Resolution drift (#967): placeholders are expanded when an artifact is
	// written, so a changed resolution (clone moved, `paths set`) leaves an
	// artifact whose source did not change with the old expansion on disk. The
	// provider's own lock records what each key resolved to when it last
	// applied, so a cited key that now resolves differently is a rewrite,
	// decided per client like a source change.
	driftedIDs := make(map[string][]string)
	for _, a := range m.Artifacts {
		key := a.Kind + "\x00" + a.Name
		if toWriteKeys[key] || !artifactAuthorized(a, opts) {
			continue
		}
		ids := resolutionDriftIDs(a, opts, tracker)
		if len(ids) == 0 {
			continue
		}
		toWrite = append(toWrite, a)
		toWriteKeys[key] = true
		driftedIDs[key] = ids
	}
	// Registration move (#1198): a derived Claude Code projection registers in
	// settings.local.json, so a hook an earlier version installed and that is
	// not registered there is rewritten, which moves its registration. One still
	// registered in a tracked settings.json stays as it is, for `doctor` to
	// report: registering it again would make it fire twice.
	claudeStrip := claudeHookSettings(opts)
	if claudeStrip != nil {
		for _, a := range m.Artifacts {
			key := a.Kind + "\x00" + a.Name
			if a.Kind != "hook" || toWriteKeys[key] || !artifactAuthorized(a, opts) || !hookInLock(opts.Lock.Managed, a.Name) {
				continue
			}
			if claudeHookToMove(opts.BaseDir, destDirScoped(a.Kind, a.Name, opts.Provider, opts.Scope), a.Name, claudeStrip) {
				toWrite = append(toWrite, a)
				toWriteKeys[key] = true
			}
		}
	}
	healed := make(map[string]bool)
	for _, f := range VerifyManaged(opts.Lock, opts.Provider, opts.BaseDir) {
		if !f.Healable() || relocated[f.Kind+"\x00"+f.Name] {
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

	// A hook or an executable artifact waiting for approval (D1141) is not
	// written, and what an earlier sync installed stays exactly as it is: its
	// files are not refreshed, not registered anew and not deleted, and its
	// lock entries are kept. It is reported as pending on every run, whether or
	// not its content changed.
	heldKeys := make(map[string]bool)
	for _, a := range m.Artifacts {
		if a.Kind == "mcp" || a.Kind == "instructions" || isReservedHook(a.Kind, a.Name) ||
			destDirScoped(a.Kind, a.Name, opts.Provider, opts.Scope) == "" || artifactAuthorized(a, opts) {
			continue
		}
		key := a.Kind + "\x00" + a.Name
		heldKeys[key] = true
		if !toWriteKeys[key] {
			result.NeedsApproval = append(result.NeedsApproval, a)
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
		if isClientOwned(mf) {
			newManaged = append(newManaged, mf)
			continue
		}
		k := mf.Kind + "\x00" + mf.Name
		// Keep the managed entries that are unchanged and not removed.
		if manifestKeys[k] && (!toWriteKeys[k] || heldKeys[k]) {
			newManaged = append(newManaged, mf)
		}
	}

	// Files a rewritten artifact no longer declares, collected as the writes
	// happen and pruned through the same path as any other removal (D178).
	var orphaned []ManagedFile
	// Hooks that stay installed while a file of theirs is dropped (D178): the
	// prune below removes that file only and leaves the registration alone.
	keepHooks := make(map[string]bool)

	// refuseArtifact is refuse for a write that Apply skips: besides reporting
	// it, the artifact's previous lock entries are carried forward unchanged. A
	// refused update leaves the previous content on disk, so the lockfile must
	// keep saying we own it; dropping the entries would leave a prune or a
	// disconnect unaware of those files until a retry succeeds.
	refuseArtifact := func(a Artifact, err error) bool {
		if !refuse(&result, a, err) {
			return false
		}
		for _, mf := range opts.Lock.Managed {
			if mf.Kind == a.Kind && mf.Name == a.Name {
				newManaged = append(newManaged, mf)
			}
		}
		return true
	}

	// Materialize the authorized artifacts.
hookLoop:
	for _, a := range toWrite {
		if a.Kind == "instructions" {
			// Handled as a group by applyInstructionsGroup below, not here.
			continue
		}
		if isReservedHook(a.Kind, a.Name) {
			// Name reserved for the client-side bootstrap (D60, EnsureBootstrapHook):
			// a KB defining a hook with this very name would otherwise be
			// written over the files EnsureBootstrapHook manages on its own —
			// ignored with a warning, never materialized.
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"hook %q: name reserved by Cartographer (client-generated hook), KB artifact ignored", a.Name))
			continue
		}
		// No destination for this provider: no approval unblocks it, so it is
		// Unsupported and never waits for one (D50).
		if destDirScoped(a.Kind, a.Name, opts.Provider, opts.Scope) == "" {
			result.Unsupported = append(result.Unsupported, a)
			continue
		}
		if !artifactAuthorized(a, opts) {
			// Not approved: notify, don't write.
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
		destRel := destDirScoped(a.Kind, a.Name, opts.Provider, opts.Scope)
		if destRel == "" {
			result.Unsupported = append(result.Unsupported, a)
			continue
		}
		if strictAgentSkipped(a, opts.Provider) {
			// A restricted agent that opted out of being widened (D283):
			// not written here, and whatever an earlier sync wrote for it
			// leaves with it.
			result.Unsupported = append(result.Unsupported, a)
			for _, mf := range opts.Lock.Managed {
				if mf.Kind == a.Kind && mf.Name == a.Name {
					orphaned = append(orphaned, mf)
				}
			}
			continue
		}

		var relPaths []string
		// materializedHash is the hash of the bytes written to disk (D138);
		// a.ContentHash stays the source hash ComputeDiff compares.
		var materializedHash string
		// execPaths is the subset of this artifact's own files written
		// executable, recorded so the hash above stays reproducible where the
		// filesystem cannot report the bit (ManagedFile.ExecutablePaths).
		var execPaths []string

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
				rp, warnings, regErr := registerMCPServer(opts.BaseDir, a.Name, spec, opts.Provider, opts.Scope)
				if regErr != nil {
					if refuseArtifact(a, regErr) {
						continue
					}
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
					if refuseArtifact(a, err) {
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
				// kind (D50) — it only differs based on the
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
					if refuseArtifact(a, err) {
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
				// A hook whose registration target is refused is refused before
				// its script is copied: the copy would overwrite the installed
				// script while the registration, and so the lock entry, still
				// describes the previous one.
				if a.Kind == "hook" {
					if err := checkHookRegistrationTarget(opts.BaseDir, opts.Provider, a.Name, destRel); err != nil {
						if refuseArtifact(a, err) {
							continue
						}
						return AppliedResult{}, err
					}
				}
				if err := mkdirAllNoFollow(opts.BaseDir, fullDestDir, 0o755); err != nil {
					if refuseArtifact(a, err) {
						continue
					}
					return AppliedResult{}, fmt.Errorf("provisioning: mkdir %s: %w", fullDestDir, err)
				}
				var err error
				var stampWarning string
				relPaths, execPaths, materializedHash, stampWarning, err = copyArtifactFiles(a, opts, fullDestDir, tracker)
				if stampWarning != "" {
					result.Warnings = append(result.Warnings, stampWarning)
				}
				if err != nil {
					if refuseArtifact(a, err) {
						continue
					}
					return AppliedResult{}, fmt.Errorf("provisioning: copy %s %s: %w", a.Kind, a.Name, err)
				}
				if a.Kind == "hook" {
					// Register the hook in the provider's native mechanism
					// (D137's hookMechanisms table: settings.json D57,
					// hooks.json D230, generated plugin D59), starting from
					// the hook.json just materialized. Best-effort on a
					// malformed hook.json (see registerHookSettings/
					// registerCodexHook): doesn't fail materialization.
					if mechanism, ok := hookMechanisms[opts.Provider]; ok {
						// The files the update dropped are not pruned yet, so a
						// dropped hook.json is removed first, or the registrar would
						// read it again and register the old spec.
						previous := hookInLock(opts.Lock.Managed, a.Name)
						if previous {
							for _, d := range droppedArtifactFiles(opts.Lock.Managed, a, destRel, relPaths) {
								if filepath.Base(d.Path) == "hook.json" {
									if err := safewrite.Remove(opts.BaseDir, filepath.Join(opts.BaseDir, d.Path)); err != nil && !errors.Is(err, os.ErrNotExist) {
										if refuseArtifact(a, err) {
											continue hookLoop
										}
										return AppliedResult{}, err
									}
								}
							}
						}
						register := mechanism.register
						if claudeStrip != nil {
							register = claudeMovingRegistrar(claudeStrip)
						}
						pluginRel, warning, registered, regErr := register(opts.BaseDir, a.Name, fullDestDir)
						if regErr != nil {
							// Refused (D148): a new hook is not recorded as installed,
							// an updated one keeps its previous lock entry; the next
							// sync retries.
							if refuseArtifact(a, regErr) {
								continue
							}
							return AppliedResult{}, regErr
						}
						if warning != "" {
							result.Warnings = append(result.Warnings, warning)
						}
						// An update the registrar skipped (no hook.json, an invalid
						// one, an event the client never fires) must not leave the
						// previous registration firing its old entry.
						if !registered && previous {
							stale := ManagedFile{Kind: "hook", Name: a.Name, Path: filepath.Join(destRel, "hook.json")}
							if err := removeHookRegistration(opts.BaseDir, stale, claudeStrip); err != nil {
								if refuseArtifact(a, err) {
									continue hookLoop
								}
								return AppliedResult{}, err
							}
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

		// A file this artifact used to own and no longer writes must go with
		// it (D178). Without this it stayed on disk AND left the lock — an
		// orphan nothing prunes, nothing reports, and that an agent keeps
		// reading, since it still sits inside a live skill/hook directory.
		// Under DryRun relPaths carries only the simulated principal file, so
		// the comparison uses the artifact's own declared files: a plan must
		// not report every other file of the artifact as removed.
		written := relPaths
		if opts.DryRun {
			written = declaredRelPaths(a, destRel)
		}
		orphaned = append(orphaned, droppedArtifactFiles(opts.Lock.Managed, a, destRel, written)...)
		if a.Kind == "hook" {
			keepHooks[a.Name] = true
		}
		if relocated[a.Kind+"\x00"+a.Name] {
			orphaned = append(orphaned, movedArtifactFiles(opts.Lock.Managed, a, destRel)...)
		}

		for _, rp := range relPaths {
			mf := ManagedFile{
				Kind:             a.Kind,
				Name:             a.Name,
				Path:             rp,
				ContentHash:      a.ContentHash,
				MaterializedHash: materializedHash,
				Source:           a.Source,
				ExecutablePaths:  execPaths,
			}
			newManaged = append(newManaged, mf)
			result.Written = append(result.Written, mf)
			if healed[a.Kind+"\x00"+a.Name] {
				result.Healed = append(result.Healed, mf)
			}
			if relocated[a.Kind+"\x00"+a.Name] {
				result.Relocated = append(result.Relocated, mf)
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
	var pathsHash string
	if err := applyInstructionsGroup(m, diff, opts, tracker, &result, &newManaged, forceInstructions, &pathsHash); err != nil {
		return AppliedResult{}, err
	}

	result.Warnings = append(result.Warnings, m.Skipped...)
	result.Warnings = append(result.Warnings, tracker.warnings...)
	result.Warnings = append(result.Warnings, unsupportedKindWarnings(m, opts.Provider)...)
	result.Warnings = append(result.Warnings, legacySkillWarnings(m)...)
	result.Warnings = append(result.Warnings, widenedAgentWarnings(m, opts)...)
	result.Warnings = append(result.Warnings, absentMCPServerWarnings(m, opts)...)

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
	genericRemoved = append(genericRemoved, orphaned...)
	genericRemoved = WithoutCoOwned(genericRemoved, opts.CoOwnedPaths)
	pruned, err := pruneManaged(genericRemoved, opts.BaseDir, opts.DryRun, keepHooks, claudeStrip)
	var pruneRefused *PruneRefusedError
	if errors.As(err, &pruneRefused) {
		// A refused removal is per artifact, like a refused write: it is
		// reported, the rest of the pass proceeds, and the file stays in the
		// lockfile so the next sync retries.
		for _, r := range pruneRefused.Refusals {
			refusePrune(&result, r.File, r.Err)
			newManaged = append(newManaged, r.File)
		}
		err = nil
	}
	if err != nil {
		return AppliedResult{}, err
	}
	result.Pruned = append(result.Pruned, pruned...)

	// Build the new Lock.
	result.NewLock = Lock{
		AppliedRevision:  m.Revision,
		Provider:         string(opts.Provider),
		Managed:          newManaged,
		PathsSectionHash: pathsHash,
	}
	for id := range opts.Lock.UnresolvedPlaceholders {
		result.PriorUnresolved = append(result.PriorUnresolved, id)
	}
	if opts.ExpandPlaceholders {
		// placeholderSources already lists every key an authorized artifact
		// cites; this only guarantees the invariant `paths list` relies on —
		// every key in the two maps has a sources entry.
		for id := range tracker.resolved {
			if _, ok := sources[id]; !ok {
				sources[id] = nil
			}
		}
		for id := range tracker.unresolved {
			if _, ok := sources[id]; !ok {
				sources[id] = nil
			}
		}
		recResolved, recUnresolved := tracker.resolved, tracker.unresolved
		// An artifact that drifted but was not written (refused, unauthorized
		// or unsupported here) still shows the old expansion: its keys keep
		// the previous record, so the drift is reported again next time.
		if len(driftedIDs) > 0 {
			written := make(map[string]bool, len(result.Written))
			for _, w := range result.Written {
				written[w.Kind+"\x00"+w.Name] = true
			}
			recResolved, recUnresolved = maps.Clone(recResolved), maps.Clone(recUnresolved)
			for key, ids := range driftedIDs {
				if written[key] {
					continue
				}
				for _, id := range ids {
					delete(recResolved, id)
					delete(recUnresolved, id)
					if v, ok := opts.Lock.ResolvedPlaceholders[id]; ok {
						recResolved[id] = v
					}
					if v, ok := opts.Lock.UnresolvedPlaceholders[id]; ok {
						recUnresolved[id] = v
					}
				}
			}
		}
		if len(recResolved) > 0 {
			result.NewLock.ResolvedPlaceholders = recResolved
		}
		if len(recUnresolved) > 0 {
			result.NewLock.UnresolvedPlaceholders = recUnresolved
		}
		if len(sources) > 0 {
			result.NewLock.PlaceholderSources = sources
		}
		// The declaration of every recorded key, for `paths list` and the
		// connect step (D263); a key no KB declares has none.
		for id := range sources {
			if d, ok := tracker.decls[id]; ok {
				if result.NewLock.PlaceholderDecls == nil {
					result.NewLock.PlaceholderDecls = map[string]PathDecl{}
				}
				result.NewLock.PlaceholderDecls[id] = d
			}
		}
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

// declaredRelPaths is where the artifact's files would land under destDirRel,
// used to compute a dry run's removals without writing anything.
func declaredRelPaths(a Artifact, destDirRel string) []string {
	out := make([]string, 0, len(a.Files))
	for _, f := range a.Files {
		out = append(out, filepath.Join(destDirRel, f.Path))
	}
	return out
}

// droppedArtifactFiles returns the entries a's previous lock owned inside its
// own destination directory that the write just performed no longer produces.
//
// The removal set comes from the PREVIOUS LOCK, never from a directory
// listing: a user may legitimately have put a file inside a managed directory,
// and deleting by listing would take it — the same guarantee the instructions
// prune is careful to keep. Only paths under destDirRel are considered, so a
// registration written elsewhere (a hook's generated plugin file) is never
// mistaken for a dropped file.
//
// destDirRel must be the artifact's destination DIRECTORY; a kind whose
// destination is a single file (agent, mcp) has no per-file ownership to lose
// and is skipped by the caller passing its file path, which is then always in
// `written`.
func droppedArtifactFiles(previous []ManagedFile, a Artifact, destDirRel string, written []string) []ManagedFile {
	if destDirRel == "" {
		return nil
	}
	keep := make(map[string]bool, len(written))
	for _, rp := range written {
		keep[filepath.Clean(rp)] = true
	}
	prefix := filepath.Clean(destDirRel) + string(filepath.Separator)
	var out []ManagedFile
	for _, mf := range previous {
		if mf.Kind != a.Kind || mf.Name != a.Name {
			continue
		}
		clean := filepath.Clean(mf.Path)
		if keep[clean] || !strings.HasPrefix(clean, prefix) {
			continue
		}
		out = append(out, mf)
	}
	return out
}

// movedArtifactFiles returns the lockfile entries of artifact a that sit
// outside destDirRel, the destination the provider declares today (D360).
// Non-empty means the destination moved since the artifact was written. For
// an mcp server destDirRel is the config file itself, so an entry recorded in
// another file (a workspace Claude entry written to .claude.json before D363,
// #654) is moved: registered in the current file, removed from the old one by
// the mcp prune, which addresses the recorded path. The emitter's file equals
// the mcp cell for every provider, so a global entry never reads as moved.
// Never applies to instructions (a group block, no per-artifact path), to the
// reserved client-generated hooks, nor to hooks at all: a hook's entries
// include generated companions (an OpenCode plugin under
// .config/opencode/plugins) that live outside its directory by design.
func movedArtifactFiles(previous []ManagedFile, a Artifact, destDirRel string) []ManagedFile {
	if destDirRel == "" || a.Kind == "instructions" || a.Kind == "hook" || isReservedHook(a.Kind, a.Name) {
		return nil
	}
	dest := filepath.Clean(destDirRel)
	var out []ManagedFile
	for _, mf := range previous {
		if mf.Kind != a.Kind || mf.Name != a.Name {
			continue
		}
		clean := filepath.Clean(mf.Path)
		if clean == dest || strings.HasPrefix(clean, dest+string(filepath.Separator)) {
			continue
		}
		out = append(out, mf)
	}
	return out
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

// kbSectionBegin/kbSectionEnd delimit ONE KB's snippet inside the
// instructions block (D182 WP1): the agent can tell which KB a directive
// comes from, and that it is that KB's perimeter rather than a session-wide
// law. Deliberately a different marker family from
// instructionsBlockBeginPrefix/instructionsBlockEnd — "cartographer:kb:"
// rather than "cartographer:instructions:" — so diagnose.go's malformed-block
// detection, which counts occurrences of THOSE two markers, keeps counting
// exactly one of each no matter how many KBs contribute sections.
//
// name is the KB name, already sanitised by config before it ever reaches
// BuildManifest, and each marker occupies one full line by construction —
// the same class of trap as D162's metasyntax: a curated instructions.md line
// that merely LOOKS like "cartographer:kb:<name>:begin" text embedded
// mid-paragraph is not, itself, a marker line, and nothing in this package
// re-parses the body to look for one, so it can never forge a section
// boundary.
func kbSectionBegin(name string) string {
	return fmt.Sprintf("<!-- cartographer:kb:%s:begin -->", name)
}
func kbSectionEnd(name string) string { return fmt.Sprintf("<!-- cartographer:kb:%s:end -->", name) }

// wrapKBSection wraps content — one KB's fully rendered instructions
// snippet — in its named begin/end markers. Wrapping only: the KB's markdown
// inside is never rewritten, reflowed or re-levelled (D154's principle,
// restated by D182 for the multi-KB case) — Cartographer owns the envelope,
// the KB owns the prose. No markdown heading is introduced either: a curated
// instructions.md may open with its own "#" heading, and nesting it under a
// generated "##" would force Cartographer to demote it.
func wrapKBSection(name, content string) string {
	return kbSectionBegin(name) + "\n" + content + "\n" + kbSectionEnd(name)
}

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
//
// pathsHash receives the hash of the placeholder section the block carries
// after this call (D262), for the new Lock; "" when there is no block or no
// client-side expansion.
func applyInstructionsGroup(m Manifest, diff Diff, opts ApplyOptions, tracker *expansionTracker, result *AppliedResult, newManaged *[]ManagedFile, force bool, pathsHash *string) error {
	destRel := instructionsDest(opts)
	if destRel == "" {
		// Provider with no known destination for instructions: keep this handled
		// consistently with the other kinds' unsupported/needs_approval schema,
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
	// from the whole manifest. Ordered by opts.KBOrder when the provider has
	// an explicit binding (D182 WP2) — Name is the KB name for this kind
	// (:577) — falling back to alphabetical, the deterministic default for a
	// provider bound to every known KB.
	var current []Artifact
	for _, a := range m.Artifacts {
		if a.Kind == "instructions" {
			current = append(current, a)
		}
	}
	rank := make(map[string]int, len(opts.KBOrder))
	for i, name := range opts.KBOrder {
		rank[name] = i
	}
	sort.Slice(current, func(i, j int) bool {
		ri, oki := rank[current[i].Name]
		rj, okj := rank[current[j].Name]
		if oki && okj {
			return ri < rj
		}
		if oki != okj {
			// Declared KBs sort before ones absent from the binding order,
			// so an ordering change can never drop a section.
			return oki
		}
		return current[i].Name < current[j].Name
	})

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

		// The lock records the manifest's hash, the one ComputeDiff compares on
		// the next run. Hashing the expanded text here made every block with a
		// placeholder look "updated" forever: a change in what a placeholder
		// resolves to is caught by the paths-section hash below instead.
		contentHash := a.ContentHash
		contentHashes[a.Name] = contentHash
		*newManaged = append(*newManaged, ManagedFile{
			Kind: a.Kind, Name: a.Name, Path: destRel, ContentHash: contentHash, Source: a.Source,
		})
	}

	// A pure reorder — same KB set, different sequence (D182 WP2) — changes
	// no ContentHash and enters neither Added, Updated nor Removed, so the
	// checks above never see it; without this it would never rewrite the file.
	if !triggered {
		triggered = instructionsOrderChanged(opts.Lock.Managed, signed)
	}

	// The placeholder section (D262): the fixed paragraph, always, plus the
	// "Local paths" table — every repo:/path: placeholder resolved during
	// THIS Apply, pre-resolved from the whole KB's key list (not just the
	// instructions content) — so the agent learns the mechanism exists even
	// when nothing resolved, and doesn't have to re-run `cartographer
	// resolve` for the keys already known. Client-side only
	// (opts.ExpandPlaceholders): the server never expands anything, so it has
	// nothing to explain or tabulate. The table depends on concepts and on the
	// local `paths:` map, neither of which enters the diff above, so a change
	// in its rendering is a trigger of its own, recorded in the lock the same
	// way a reorder is detected.
	//
	// The shared operational section (D1182) is client-owned text of the same
	// kind: a release that edits it changes no artifact ContentHash, so no
	// Added/Updated fires. It is folded into the hash recorded here, which
	// rewrites the block once after such a release and converges.
	var pathsSection, sharedSection string
	if len(signed) > 0 {
		sections := make([]string, 0, len(signed))
		for _, a := range signed {
			sections = append(sections, string(expandedContent[a.Name]))
		}
		if sharedSectionWanted(sections) {
			sharedSection = SharedOperationalSection()
		}
	}
	if opts.ExpandPlaceholders && len(signed) > 0 {
		pathsSection = buildPathsSection(tracker.resolved)
		*pathsHash = pathsSectionHash(sharedSection + "\x00" + pathsSection)
		if !triggered && *pathsHash != opts.Lock.PathsSectionHash {
			triggered = true
		}
	}

	// D293: does the AGENTS.md import state need to change? A project-scope
	// Claude CLAUDE.md must import @AGENTS.md when the project root has one,
	// and drop the import when it doesn't. An appearing/disappearing
	// AGENTS.md changes the rendered body, so it is a trigger of its own.
	wantImport := needsAgentsMDImport(opts)
	if !triggered && len(signed) > 0 {
		fullPath := filepath.Join(opts.BaseDir, destRel)
		if wantImport != blockHasAgentsMDImport(fullPath) {
			triggered = true
		}
	}

	if !triggered {
		return nil
	}

	fullPath := filepath.Join(opts.BaseDir, destRel)

	if len(signed) == 0 {
		// A block another projection of this workspace still renders into the
		// same file stays (D364): Codex, OpenCode and Antigravity share AGENTS.md.
		if !opts.DryRun && !opts.CoOwnedPaths[filepath.ToSlash(destRel)] {
			if err := removeInstructionsBlock(opts.BaseDir, fullPath); err != nil {
				if !errors.Is(err, ErrSymlinkDestination) {
					return fmt.Errorf("provisioning: remove instructions block %s: %w", destRel, err)
				}
				// Refused (D148): the sections stay in the lockfile and are
				// reported, the next sync retries. The block is still on disk,
				// so the paths-section hash stays as it was.
				*pathsHash = opts.Lock.PathsSectionHash
				kept := result.Pruned[:0]
				for _, mf := range result.Pruned {
					if mf.Kind == "instructions" {
						refusePrune(result, mf, err)
						*newManaged = append(*newManaged, mf)
						continue
					}
					kept = append(kept, mf)
				}
				result.Pruned = kept
				return nil
			}
			pruneEmptyDirs(opts.BaseDir, destRel)
		}
		return nil
	}

	// Each KB's snippet is individually attributed (D182 WP1): current is
	// already in the order applyInstructionsGroup will render (WP2's
	// opts.KBOrder-aware sort above), so signed — a filtered copy of current —
	// carries that same order through to the wrap.
	snippets := make([]string, 0, len(signed))
	for _, a := range signed {
		content := strings.TrimRight(string(expandedContent[a.Name]), "\n")
		snippets = append(snippets, wrapKBSection(a.Name, content))
	}
	body := strings.Join(snippets, "\n\n")

	// D1182: the operational bullets, once per block, after the last section.
	if sharedSection != "" {
		body += "\n\n" + strings.TrimRight(sharedSection, "\n")
	}

	// D293: a project-scope Claude CLAUDE.md that lives alongside an
	// AGENTS.md must import it, or creating/keeping CLAUDE.md hides the
	// repository's own instructions from every Claude session.
	if wantImport {
		body = "@AGENTS.md\n\n" + body
	}

	if pathsSection != "" {
		body += "\n\n" + pathsSection
	}

	// The subagent sentence, per provider and per KB (D154): it must describe
	// what THIS client received, not what the KB declares. A client whose "agent"
	// cell is unsupportedDest (crush) receives none, and the sentence must not
	// tell it to delegate to subagents that are not there; installedSubagentSentence
	// derives it from destDir.
	if sentence := installedSubagentSentence(m, opts); sentence != "" {
		body += "\n\n" + sentence
	}

	if !opts.DryRun {
		if err := writeInstructionsBlock(opts.BaseDir, fullPath, body); err != nil {
			if !errors.Is(err, ErrSymlinkDestination) {
				return fmt.Errorf("provisioning: write instructions block %s: %w", destRel, err)
			}
			// Refused (D148): per artifact, like a skill. None of the sections is
			// recorded as written. The old block is still on disk, so the
			// previous instructions entries and the paths-section hash are
			// carried forward unchanged and the next sync retries.
			for _, a := range signed {
				refuse(result, a, err)
			}
			kept := (*newManaged)[:0]
			for _, mf := range *newManaged {
				if mf.Kind != "instructions" {
					kept = append(kept, mf)
				}
			}
			for _, mf := range opts.Lock.Managed {
				if mf.Kind == "instructions" {
					kept = append(kept, mf)
				}
			}
			*newManaged = kept
			*pathsHash = opts.Lock.PathsSectionHash
			return nil
		}
	}

	for _, a := range signed {
		result.Written = append(result.Written, ManagedFile{
			Kind: a.Kind, Name: a.Name, Path: destRel, ContentHash: contentHashes[a.Name], Source: a.Source,
		})
	}
	return nil
}

// instructionsOrderChanged reports whether newOrder (this run's authorized
// "instructions" artifacts, already sorted for rendering) names the same KBs
// as previous's "instructions" entries but in a different sequence (D182
// WP2). previous is opts.Lock.Managed: each prior applyInstructionsGroup run
// appended its ManagedFile entries in the order it rendered them, so a JSON
// round-trip through the lockfile preserves that order as a record of "what
// was rendered last time" with no new field.
//
// A changed SET (a KB added or removed) is already caught by
// diff.Added/diff.Removed, so this only needs to fire on the one case those
// miss: same names, different positions. A length mismatch is therefore
// always a set change and is left to that existing path.
func instructionsOrderChanged(previous []ManagedFile, newOrder []Artifact) bool {
	var prevNames []string
	for _, mf := range previous {
		if mf.Kind == "instructions" {
			prevNames = append(prevNames, mf.Name)
		}
	}
	if len(prevNames) != len(newOrder) {
		return false
	}
	for i, a := range newOrder {
		if prevNames[i] != a.Name {
			return true
		}
	}
	return false
}

// needsAgentsMDImport reports whether the instructions block should start with
// an @AGENTS.md import line (D293). The import is needed when all three hold:
//   - the provider is Claude Code (the only one whose CLAUDE.md hides AGENTS.md);
//   - the scope is project (user-scope ~/.claude/CLAUDE.md is irrelevant);
//   - the project root contains an AGENTS.md file.
//
// When the file at fullPath already contains @AGENTS.md OUTSIDE the managed
// block, the import is NOT added inside (a repo following the D213 convention
// already has one — duplicating it would break the file).
func needsAgentsMDImport(opts ApplyOptions) bool {
	if opts.Provider != configurator.ProviderClaudeCode || opts.Scope != ScopeProject {
		return false
	}
	if opts.InstructionsRel != "" {
		// A block beside a CLAUDE.md the repository already tracks hides
		// nothing: that file is read as it is, and so is AGENTS.md through it.
		return false
	}
	agentsPath := filepath.Join(opts.BaseDir, "AGENTS.md")
	if _, err := os.Lstat(agentsPath); err != nil {
		return false
	}
	// If the target file already imports @AGENTS.md outside the managed block,
	// skip: the user (or D213) already wrote one and a duplicate would be wrong.
	destRel := instructionsDest(opts)
	fullPath := filepath.Join(opts.BaseDir, destRel)
	return !fileImportsAgentsMDOutsideBlock(fullPath)
}

// fileImportsAgentsMDOutsideBlock reports whether the file at path contains an
// @AGENTS.md import line outside the Cartographer-managed block. Used by D293
// to avoid adding a duplicate import inside the block when the user already
// wrote one.
func fileImportsAgentsMDOutsideBlock(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	// Strip the managed block so only the user's own text is checked.
	stripped, ok := replaceBetweenMarkers(content, "")
	if !ok {
		stripped = content
	}
	return containsAgentsMDImport(stripped)
}

// blockHasAgentsMDImport reports whether the existing managed block at path
// contains the @AGENTS.md import line. Used to detect when the import state
// changed (AGENTS.md appeared or disappeared) and trigger a rewrite.
func blockHasAgentsMDImport(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	beginIdx := strings.Index(content, instructionsBlockBeginPrefix)
	if beginIdx == -1 {
		return false
	}
	endIdx := strings.Index(content[beginIdx:], instructionsBlockEnd)
	if endIdx == -1 {
		return false
	}
	block := content[beginIdx : beginIdx+endIdx+len(instructionsBlockEnd)]
	return containsAgentsMDImport(block)
}

// containsAgentsMDImport reports whether text contains a line that is exactly
// "@AGENTS.md" (the Claude Code import syntax).
func containsAgentsMDImport(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "@AGENTS.md" {
			return true
		}
	}
	return false
}

// widenedAgentWarnings names, on every run, each KB agent that declares a
// `tools` allow-list this provider drops (D283): in Claude Code an omitted
// `tools` means every tool, so the agent installed here is broader than the
// one its description promises. Computed from the manifest like
// unsupportedKindWarnings, so a widened agent is not reported once and then
// forgotten. An artifact not installed (awaiting approval, or skipped by
// strict_tools) is not reported: nothing was widened.
func widenedAgentWarnings(m Manifest, opts ApplyOptions) []string {
	var out []string
	for _, a := range m.Artifacts {
		if !agentWidened(a, opts.Provider) || a.Restriction.Strict {
			continue
		}
		if destDirScoped(a.Kind, a.Name, opts.Provider, opts.Scope) == "" || !artifactAuthorized(a, opts) {
			continue
		}
		out = append(out, fmt.Sprintf(
			"agent %[1]q declares tools: %[2]s; %[3]s cannot express that restriction, so it receives the agent unrestricted (write that client's own restriction under `providers.%[3]s` in its frontmatter, or add `strict_tools: true` to skip it there instead)",
			a.Name, a.Restriction.Tools, opts.Provider))
	}
	return out
}

// absentMCPServerWarnings names, on every run, each agent whose
// `providers.<client>` tools cite an `@<server>` this machine's client has no
// MCP entry for (D320): the client then hands the subagent nothing from that
// server, and nothing else says so. Advisory — the entry is still copied
// verbatim (D291). Only the global scope is checked: a project-scope apply
// inherits the global MCP servers as well, so its own config alone would
// report servers that are in fact there. An unreadable config reports nothing.
func absentMCPServerWarnings(m Manifest, opts ApplyOptions) []string {
	if opts.Scope != ScopeGlobal {
		return nil
	}
	var configured map[string]bool
	var out []string
	for _, a := range m.Artifacts {
		if a.Kind != "agent" || destDirScoped(a.Kind, a.Name, opts.Provider, opts.Scope) == "" || !artifactAuthorized(a, opts) {
			continue
		}
		content, err := singleArtifactContent(a, opts)
		if err != nil {
			continue
		}
		fmRaw, _, ok := okf.SplitFrontmatter(string(content))
		if !ok {
			continue
		}
		fm, err := okf.ParseFrontmatter(fmRaw)
		if err != nil {
			continue
		}
		fields, err := nativeFieldsFor(fm, opts.Provider)
		if err != nil {
			continue
		}
		refs := nativeMCPServerRefs(fields)
		if len(refs) == 0 {
			continue
		}
		if configured == nil {
			names, err := MCPServerEntryNames(opts.BaseDir, opts.Provider)
			if err != nil {
				return out
			}
			configured = map[string]bool{}
			for _, n := range names {
				configured[n] = true
			}
		}
		for _, server := range refs {
			if !configured[server] {
				out = append(out, fmt.Sprintf(
					"agent %[1]q on %[2]s: providers.%[2]s.tools references @%[3]s, which is not configured on this machine's %[2]s — the agent will only see built-in tools",
					a.Name, opts.Provider, server))
			}
		}
	}
	return out
}

// legacySkillWarnings reports, once per KB, that the KB carries a copy of a
// bundled skill under its pre-D958 name (kb-create, now cartographer-kb-create).
// The client receives that copy next to the bundled skill, so the agent sees
// one skill twice; kb_repair (legacy_skill_name) moves the copy to the new
// name. Computed from the manifest, so it repeats on every run until fixed.
func legacySkillWarnings(m Manifest) []string {
	found := map[string][]string{}
	for _, a := range m.Artifacts {
		if !strings.HasPrefix(a.Source, "kb:") || a.Kind != "skill" {
			continue
		}
		if _, ok := skillbundle.Renamed(a.Name); ok {
			name := strings.TrimPrefix(a.Source, "kb:")
			found[name] = append(found[name], a.Name)
		}
	}
	kbs := make([]string, 0, len(found))
	for name := range found {
		kbs = append(kbs, name)
	}
	sort.Strings(kbs)
	out := make([]string, 0, len(kbs))
	for _, name := range kbs {
		skills := found[name]
		sort.Strings(skills)
		out = append(out, fmt.Sprintf("KB %q still carries skill(s) under pre-D958 names (%s): the client gets them next to the bundled cartographer-* skills — run kb_repair check=legacy_skill_name to move them",
			name, strings.Join(skills, ", ")))
	}
	return out
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
		if destDirScoped("agent", a.Name, opts.Provider, opts.Scope) == "" || strictAgentSkipped(a, opts.Provider) {
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

// ArtifactAuthorized reports whether Apply would install a: built in, not
// gated, or approved for its exact content hash (opts.ApprovedMCP).
func ArtifactAuthorized(a Artifact, opts ApplyOptions) bool { return artifactAuthorized(a, opts) }

func artifactAuthorized(a Artifact, opts ApplyOptions) bool {
	if a.BuiltIn {
		return true
	}
	if ArtifactRequiresApproval(a, opts) {
		// An MCP server receives the agent's data, so a KB descriptor always
		// needs the client's hash-bound approval: a signature proves only which
		// server it came from, and since D906 any rw token on the KB can write
		// one. What runs on this machine (a hook, any executable file, D1141)
		// follows the same contract: the server signs whatever a token wrote, so
		// Signed proves the origin and is never consent.
		return a.ContentHash != "" && opts.ApprovedMCP[a.Source+"\x00"+ApprovalName(a)] == a.ContentHash
	}
	// Everything else a KB ships is installed (D906): the pull has already
	// rejected a path that escapes its directory and an artifact whose signature
	// fails against a pinned key (D40, D114), so Signed does not gate this.
	return a.Signed || strings.HasPrefix(a.Source, "kb:")
}

// ApprovalName is the name an artifact's local approval is stored under inside
// its source KB: the bare name for an MCP descriptor (the form D115 wrote), and
// "<kind>/<name>" for everything else, so a hook and a descriptor of one name
// never share an approval. An artifact name is a single path segment, so the
// two forms cannot collide.
func ApprovalName(a Artifact) string {
	if a.Kind == "mcp" {
		return a.Name
	}
	return a.Kind + "/" + a.Name
}

// ArtifactRequiresApproval reports whether a KB artifact waits for the client's
// hash-bound approval before it is installed (D906, D1141): an MCP descriptor, a
// hook, any artifact that ships a file with the execute bit, and a skill or
// agent whose frontmatter declares hooks or MCP servers. The bundle is
// built into the binary and never asks. An artifact whose files cannot be read
// is treated as executable: it fails closed.
func ArtifactRequiresApproval(a Artifact, opts ApplyOptions) bool {
	if a.BuiltIn || !strings.HasPrefix(a.Source, "kb:") {
		return false
	}
	if a.Kind == "mcp" || a.Kind == "hook" {
		return true
	}
	files := a.Files
	if len(files) == 0 {
		var err error
		files, err = ReadArtifactFiles(a, opts.BundleFS, opts.KBRoots)
		if err != nil {
			// With nowhere to read from (a caller that holds only the manifest,
			// like status) nothing can be installed from it either.
			return opts.KBRoots != nil
		}
	}
	for _, f := range files {
		if f.Executable {
			return true
		}
		if (a.Kind == "skill" && strings.EqualFold(f.Path, "SKILL.md")) || a.Kind == "agent" {
			if FrontmatterDeclaresCommands(f.Content) {
				return true
			}
		}
	}
	return false
}

// FrontmatterDeclaresCommands reports whether a skill's or agent's frontmatter
// carries a `hooks` or MCP-server key at any depth, so also inside a per-client
// `providers:` block: the client runs those commands. The frontmatter is parsed
// as YAML into a node tree and every key is compared after decoding, so quoting,
// escapes, flow mappings, complex keys, anchors and merge keys cannot hide one.
// It answers true (approval needed) whenever it cannot tell: a file that opens
// like frontmatter but never closes, or whose YAML does not parse.
func FrontmatterDeclaresCommands(content []byte) bool {
	text := strings.TrimPrefix(string(content), "\ufeff")
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(text, "\n")
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	// A delimiter starts at column 0: an indented "---" is text inside a block
	// scalar, and taking it for the close would hide the keys after it.
	if start >= len(lines) || strings.TrimRight(lines[start], " \t") != "---" {
		return false // no frontmatter
	}
	end := -1
	for i := start + 1; i < len(lines); i++ {
		if t := strings.TrimRight(lines[i], " \t"); t == "---" || t == "..." {
			end = i
			break
		}
	}
	if end < 0 {
		return true
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(strings.Join(lines[start+1:end], "\n")), &doc); err != nil {
		return true
	}
	return yamlNodeDeclaresCommands(&doc, map[*yaml.Node]bool{})
}

func yamlNodeDeclaresCommands(n *yaml.Node, seen map[*yaml.Node]bool) bool {
	if n == nil || seen[n] {
		return false
	}
	seen[n] = true
	switch n.Kind {
	case yaml.AliasNode:
		return yamlNodeDeclaresCommands(n.Alias, seen)
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind == yaml.ScalarNode {
				switch strings.ToLower(strings.TrimSpace(k.Value)) {
				case "hooks", "mcpservers", "mcp_servers", "mcp-servers":
					return true
				}
			}
			if yamlNodeDeclaresCommands(k, seen) || yamlNodeDeclaresCommands(n.Content[i+1], seen) {
				return true
			}
		}
		return false
	}
	for _, c := range n.Content {
		if yamlNodeDeclaresCommands(c, seen) {
			return true
		}
	}
	return false
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
		// Where the filesystem has no execute bit, being executable is decided
		// by the extension (PATHEXT) and exec.LookPath above already applied
		// it; asking the permissions would reject every command on Windows.
		if !info.Mode().IsRegular() || (execbit.Supported && !execbit.IsExecutable(info.Mode())) {
			return fmt.Errorf("provisioning: mcp %q for %s: command %q is not an executable regular file", a.Name, opts.Provider, spec.Command)
		}
	}
	return nil
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
//
// baseDir is the client directory path lives under: a file, or a directory
// component below baseDir, that is a symlink is refused (D148).
func writeInstructionsBlock(baseDir, path, body string) error {
	block := instructionsBlockBegin + "\n" + body + "\n" + instructionsBlockEnd + "\n"

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := mkdirAllNoFollow(baseDir, filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return writeFileNoFollow(path, []byte(block), 0o644)
	}
	if err != nil {
		return err
	}

	// An existing file is written through its directory chain too: the walk
	// refuses a symlinked directory the read above went through.
	if err := safewrite.CheckPath(baseDir, path); err != nil {
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
// Idempotent: if the block isn't present (already removed), no-op — and a no-op
// never refuses. When the block is there, a file or directory component below
// baseDir that is a symlink is refused (D148).
func removeInstructionsBlock(baseDir, path string) error {
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
	if err := safewrite.CheckPath(baseDir, path); err != nil {
		return err
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
// TestEveryDestinationRootIsAPruneBoundary checks it against both destination
// matrices: a provider or a workspace cell added later fails there until its root is
// here. Writers outside the matrix (hook registrations, bootstrap) sit under these too.
var provisioningRootDirs = map[string]bool{
	".claude":                            true,
	".codex":                             true,
	".kiro":                              true,
	".opencode":                          true,
	".config":                            true,
	filepath.Join(".config", "opencode"): true,
	filepath.Join(".config", "crush"):    true,
	".gemini":                            true,
	".copilot":                           true,
	// Workspace scope only (projectDestinationMatrix).
	".agents": true,
	".crush":  true,
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
		// A symlinked directory, or one under a symlink, is someone else's:
		// os.Remove would unlink the first and empty the target of the second.
		if ensureSafeDir(baseDir, dir) != nil {
			return
		}
		if fi, err := os.Lstat(filepath.Join(baseDir, dir)); err == nil && !fi.IsDir() {
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
// (settings.json for claude via removeHookEntries, hooks.json for codex via
// removeCodexHook) — otherwise a removed/pruned hook would keep
// firing from a dangling command that no longer exists on disk. A hook can
// have several ManagedFile entries (hook.json + script(s), same Name): the
// entry is stripped once per distinct hook name, same dedup pattern as the
// instructions block above. No provider parameter is needed — mf.Path already
// encodes which provider materialized it (destDir gives each provider its own
// hooks directory), see hookProviderFromPath. Apply removes a file dropped from
// a hook that stays in the manifest without this strip (pruneManaged).
func PruneManaged(managed []ManagedFile, baseDir string, dryRun bool) ([]ManagedFile, error) {
	return pruneManaged(managed, baseDir, dryRun, nil, nil)
}

// pruneManaged is PruneManaged with the hooks that stay installed: a file of a
// hook named in keepHooks leaves with its own entry only, because the hook is
// still in the manifest and its registration must go on firing. The
// registration is stripped when the hook itself is pruned (its name no longer
// in the manifest, or a disconnect, which passes none).
func pruneManaged(managed []ManagedFile, baseDir string, dryRun bool, keepHooks map[string]bool, claudeSettings []string) ([]ManagedFile, error) {
	pruned := make([]ManagedFile, 0, len(managed))
	// What each shared target (an instructions file, an MCP server, a hook's
	// registration) came to: present with a nil error once removed, with the
	// refusal if it was refused — so a later entry sharing the target is
	// reported as refused too, never as pruned.
	blockResult := make(map[string]error)
	hookResult := make(map[string]error)
	mcpResult := make(map[string]error)
	var refusals []PruneRefusal
	// refused records a symlink refusal against one managed file and reports
	// whether the pass carries on with the next one. The file is left out of
	// the returned list; anything else is a real error and stays fatal.
	refused := func(mf ManagedFile, err error) bool {
		if !errors.Is(err, ErrSymlinkDestination) {
			return false
		}
		refusals = append(refusals, PruneRefusal{File: mf, Err: err})
		return true
	}
	for _, mf := range managed {
		fullPath := filepath.Join(baseDir, mf.Path)
		if dryRun {
			// A preview refuses what the real run would: a managed file, or the
			// shared file holding our block or entry, that sits behind a link.
			if err := dryRunPruneCheck(baseDir, mf); err != nil && refused(mf, err) {
				continue
			}
			pruned = append(pruned, mf)
			continue
		}
		if mf.Kind == "instructions" {
			err, seen := blockResult[fullPath]
			if !seen {
				err = removeInstructionsBlock(baseDir, fullPath)
				if err == nil || errors.Is(err, ErrSymlinkDestination) {
					blockResult[fullPath] = err
				}
				if err == nil {
					pruneEmptyDirs(baseDir, mf.Path)
				}
			}
			if err != nil {
				if refused(mf, err) {
					continue
				}
				return nil, fmt.Errorf("provisioning: prune blocco instructions %s: %w", mf.Path, err)
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
			// Keyed by name and target file: the same name registered in two
			// config files (a relocated entry) is two removals.
			mcpKey := mf.Name + "\x00" + filepath.ToSlash(mf.Path)
			err, seen := mcpResult[mcpKey]
			if !seen {
				if provider := mcpProviderFromPath(mf.Path); provider != "" {
					err = removeMCPServer(baseDir, mf.Name, provider, filepath.ToSlash(mf.Path))
				}
				if err == nil || errors.Is(err, ErrSymlinkDestination) {
					mcpResult[mcpKey] = err
				}
			}
			if err != nil {
				if refused(mf, err) {
					continue
				}
				return nil, fmt.Errorf("provisioning: prune mcp %s: %w", mf.Name, err)
			}
			pruned = append(pruned, mf)
			continue
		}
		// A hook's registration goes before its files: a refused registration
		// must leave the script it points at in place.
		if mf.Kind == "hook" && !keepHooks[mf.Name] {
			err, seen := hookResult[mf.Name]
			if !seen {
				err = removeHookRegistration(baseDir, mf, claudeSettings)
				if err == nil || errors.Is(err, ErrSymlinkDestination) {
					hookResult[mf.Name] = err
				}
			}
			if err != nil {
				if refused(mf, err) {
					continue
				}
				return nil, err
			}
		}
		if err := safewrite.Remove(baseDir, fullPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			if refused(mf, err) {
				continue
			}
			return nil, fmt.Errorf("provisioning: prune %s: %w", mf.Path, err)
		}
		pruneEmptyDirs(baseDir, mf.Path)
		pruned = append(pruned, mf)
	}
	if len(refusals) > 0 {
		return pruned, &PruneRefusedError{Refusals: refusals}
	}
	return pruned, nil
}

// removeHookRegistration strips mf's hook from the provider-native mechanism
// that registered it (settings.json, hooks.json, a plugin, ...). Providers with
// no registration to strip return nil. claudeSettings names the Claude Code
// settings file to strip, nil for both (removeHookEntries).
func removeHookRegistration(baseDir string, mf ManagedFile, claudeSettings []string) error {
	var err error
	switch hookProviderFromPath(mf.Path) {
	case "claude":
		if err = removeHookEntries(baseDir, mf.Name, claudeSettings...); err != nil {
			err = fmt.Errorf("provisioning: prune entry settings.json hook %s: %w", mf.Name, err)
		}
	case "codex":
		if err = removeCodexHook(baseDir, mf.Name); err != nil {
			err = fmt.Errorf("provisioning: prune entry hooks.json hook %s: %w", mf.Name, err)
		}
	case "opencode":
		if err = removeOpenCodePlugin(baseDir, mf.Name); err != nil {
			err = fmt.Errorf("provisioning: prune plugin opencode hook %s: %w", mf.Name, err)
		}
	case "antigravity":
		if err = removeAntigravityHook(baseDir, mf.Name, mf.Path); err != nil {
			err = fmt.Errorf("provisioning: prune entry Antigravity hooks.json hook %s: %w", mf.Name, err)
		}
	case "copilot":
		if err = removeCopilotHook(baseDir, mf.Name); err != nil {
			err = fmt.Errorf("provisioning: prune Copilot hook file %s: %w", mf.Name, err)
		}
	case "crush":
		if err = removeCrushHook(baseDir, mf.Name, mf.Path); err != nil {
			err = fmt.Errorf("provisioning: prune entry Crush hook %s: %w", mf.Name, err)
		}
	case "kiro":
		if err = removeKiroHook(baseDir, mf.Name); err != nil {
			err = fmt.Errorf("provisioning: prune entry Kiro cartographer.json hook %s: %w", mf.Name, err)
		}
	}
	return err
}

// dryRunPruneCheck is the refusal a real prune of mf would meet, without
// changing anything: nil when there is nothing there to change (an absent file,
// an instructions file with no block of ours), which a prune never refuses.
func dryRunPruneCheck(baseDir string, mf ManagedFile) error {
	fullPath := filepath.Join(baseDir, mf.Path)
	if mf.Kind == "instructions" {
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return nil
		}
		if _, ok := replaceBetweenMarkers(string(data), ""); !ok {
			return nil
		}
	} else if _, err := os.Lstat(fullPath); err != nil {
		return nil
	}
	return safewrite.CheckPath(baseDir, fullPath)
}

// PruneRefusal is one managed file PruneManaged left in place because removing
// it, or its registration in a shared config file, would have gone through a
// symlink (ErrSymlinkDestination).
type PruneRefusal struct {
	File ManagedFile
	Err  error
}

// PruneRefusedError is what PruneManaged returns, next to the list of what it
// did prune, when it refused one or more files. The refusal is per file and the
// pass carries on, like a refused write: the caller keeps the refused files in
// its lockfile so that the next run retries, and reports them.
type PruneRefusedError struct {
	Refusals []PruneRefusal
}

func (e *PruneRefusedError) Error() string {
	msgs := make([]string, 0, len(e.Refusals))
	for _, r := range e.Refusals {
		msgs = append(msgs, fmt.Sprintf("%s/%s not removed: %v", r.File.Kind, r.File.Name, r.Err))
	}
	return strings.Join(msgs, "; ")
}

// Unwrap exposes every refusal, so errors.Is(err, ErrSymlinkDestination) holds.
func (e *PruneRefusedError) Unwrap() []error {
	errs := make([]error, 0, len(e.Refusals))
	for _, r := range e.Refusals {
		errs = append(errs, r.Err)
	}
	return errs
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
	// unsupported marks a combination this provider cannot represent.
	unsupported bool
}

func at(segments ...string) destination { return destination{dir: segments} }

func perName(suffix string, segments ...string) destination {
	return destination{dir: segments, named: true, suffix: suffix}
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
//   - kiro's "hook" cell holds the hook's files outside the directory its
//     engine scans; the registration is one Cartographer-owned file beside
//     them (D300). Its "agent" cell is a JSON config in the global agent
//     directory (D195).
//   - crush documents no user-level subagent directory, so that cell is
//     unsupported (D225); its hook cell is supported since D363.
//   - copilot fills all five cells (D676); its agent files end in ".agent.md".
var destinationMatrix = map[string]map[configurator.Provider]destination{
	"mcp": {
		configurator.ProviderClaudeCode: at(".claude.json"),
		configurator.ProviderCodex:      at(".codex", "config.toml"),
		// OpenCode's global config, not "$HOME/opencode.json" (read only as a
		// project file, #1172): an entry locked there relocates on sync.
		configurator.ProviderOpenCode:    at(".config", "opencode", "opencode.json"),
		configurator.ProviderKiro:        at(".kiro", "settings", "mcp.json"),
		configurator.ProviderAntigravity: at(".gemini", "config", "mcp_config.json"),
		configurator.ProviderCrush:       at(".config", "crush", "crush.json"),
		// copilot: probed on 1.0.94, D676.
		configurator.ProviderCopilot: at(".copilot", "mcp-config.json"),
	},
	"instructions": {
		configurator.ProviderClaudeCode:  at(".claude", "CLAUDE.md"),
		configurator.ProviderOpenCode:    at(".config", "opencode", "AGENTS.md"),
		configurator.ProviderCodex:       at(".codex", "AGENTS.md"),
		configurator.ProviderKiro:        at(".kiro", "steering", "cartographer.md"),
		configurator.ProviderAntigravity: at(".gemini", "GEMINI.md"),
		// The Crush-specific global context file, chosen over the cross-tool
		// ~/.config/AGENTS.md it also loads for the same reason the codex and
		// opencode cells point at their own file: the shared one belongs to
		// whatever else the user runs.
		// https://github.com/charmbracelet/crush#global-context-files
		configurator.ProviderCrush: at(".config", "crush", "CRUSH.md"),
		// copilot: `copilot instruction list` reports it as "Personal
		// instructions" (probed on 1.0.94, D676).
		configurator.ProviderCopilot: at(".copilot", "copilot-instructions.md"),
	},
	"agent": {
		configurator.ProviderClaudeCode: perName(".md", ".claude", "agents"),
		// The global config directory OpenCode reports (`opencode debug paths`,
		// 2.0.25) and the documented 1.x global path, so one cell serves both
		// majors (D360). The former `.opencode/agent` (D192) is not read by
		// 2.x; TestOpenCodeDeclaresItsConfigDirectory fails if it drifts again.
		configurator.ProviderOpenCode: perName(".md", ".config", "opencode", "agents"),
		configurator.ProviderCodex:    perName(".toml", ".codex", "agents"),
		// kiro: a JSON agent config in the global agent directory (D195).
		// D140 read this cell as unsupported because Kiro's agents were
		// top-level personas the user selects, not delegates a main agent can
		// invoke. That is no longer true: the shipped default agent documents a
		// subagent system and a `use_subagent` tool that selects by
		// description, and `kiro-cli agent list` reports a config dropped here
		// as "Global". Verified against Kiro CLI 2.21.3 — the format is JSON,
		// not the Markdown the vendor documentation describes.
		configurator.ProviderKiro:        perName(".json", ".kiro", "agents"),
		configurator.ProviderAntigravity: perName(".md", ".gemini", "config", "agents"),
		// crush: neither its README nor its docs/config page describes a
		// user-level subagent directory, and a destination is declared, never
		// invented.
		configurator.ProviderCrush: unsupportedDest,
		// copilot: `~/.copilot/agents/<name>.agent.md`, frontmatter name and
		// description; a session reported seeing the agent (probed on 1.0.94, D676).
		configurator.ProviderCopilot: perName(".agent.md", ".copilot", "agents"),
	},
	"hook": {
		configurator.ProviderClaudeCode: perName("", ".claude", "hooks"),
		configurator.ProviderCodex:      perName("", ".codex", "hooks"),
		// Hook files (script + hook.json), same as for the other providers —
		// the JS plugin that invokes them (D59) is generated elsewhere (Apply,
		// registerOpenCodePlugin) in .config/opencode/plugins/, not here.
		configurator.ProviderOpenCode: perName("", ".opencode", "hooks"),
		// kiro (D300): the registration is ~/.kiro/hooks/cartographer.json, a
		// standalone v1 hook file Cartographer owns whole (registerKiroHook).
		// The scripts sit one level below it, in ~/.kiro/hooks/cartographer/<n>/,
		// because the engine's loader does not recurse: it reads only the
		// *.json files directly in ~/.kiro/hooks (KAS loadHooksDir, and a probe
		// on 2.27.0 that loaded one hook file of the two placed at the top level
		// and in a subdirectory). If a release makes it recurse, every hook.json
		// here would be read as a malformed hook file, and this cell must move
		// out of ~/.kiro/hooks. These hooks fire only in `kiro-cli chat --v3
		// --tui`, so the scheduled timer is still advised (hookMechanism.sessionHookLimit).
		configurator.ProviderKiro:        perName("", ".kiro", "hooks", "cartographer"),
		configurator.ProviderAntigravity: perName("", ".gemini", "config", "hooks"),
		// crush: hooks fire from `hooks` in crush.json, PreToolUse only (probed
		// on v0.98.0, D363), registered by registerCrushHook. No session event,
		// so its sync trigger stays the scheduled timer.
		configurator.ProviderCrush: perName("", ".config", "crush", "hooks"),
		// copilot: Copilot loads ~/.copilot/hooks/ RECURSIVELY and parses every
		// *.json below it (probed on 1.0.94: a foreign JSON in a subdirectory
		// logs "Invalid hook configuration"), so the KB hook's own hook.json
		// must not live there. The files go to ~/.copilot/cartographer-hooks/<n>/
		// and only the registration, ~/.copilot/hooks/cartographer-<n>.json,
		// sits in hooks/ (registerCopilotHook, D676).
		configurator.ProviderCopilot: perName("", ".copilot", "cartographer-hooks"),
	},
	"skill": {
		configurator.ProviderClaudeCode: perName("", ".claude", "skills"),
		// Divergence from the documented path, working and deliberate (D192):
		// Codex's public documentation names `$HOME/.agents/skills` for the user
		// scope (https://developers.openai.com/codex/skills), while
		// `codex debug prompt-input` confirms client 0.153.4 catalogues them
		// here. The right target is the *repository* path, which only exists
		// once a workspace scope does (D193), so moving it now would mean doing
		// the migration twice.
		configurator.ProviderCodex: perName("", ".codex", "skills"),
		configurator.ProviderKiro:  perName("", ".kiro", "skills"),
		// Under the config directory `opencode debug paths` reports; 2.0.25 does
		// not list skills under `.opencode/skills` globally (D360).
		configurator.ProviderOpenCode:    perName("", ".config", "opencode", "skills"),
		configurator.ProviderAntigravity: perName("", ".gemini", "config", "skills"),
		// crush implements the Agent Skills standard and scans this directory
		// globally, among others it shares with Claude Code and the .agents
		// convention; its own is the one Cartographer owns.
		// https://github.com/charmbracelet/crush#agent-skills
		configurator.ProviderCrush: perName("", ".config", "crush", "skills"),
		// copilot: "Personal skills" (probed on 1.0.94, D676).
		configurator.ProviderCopilot: perName("", ".copilot", "skills"),
	},
}

// artifactKinds are the kinds the matrix must cover. A manifest built by a
// newer server may carry a kind absent from this list; destDir returns "" for
// it, exactly as it does for an unsupported cell. A kind added here needs its
// KindInfo in kindInfos too (TestKindsMatchTheMatrix fails without it, D864).
var artifactKinds = []string{"mcp", "instructions", "agent", "hook", "skill"}

// KindInfo describes one kind of artifact a KB can hold: where its files go in
// the KB and where the reader is told how it works. It is the contract between
// provisioning and the Atlas Artifacts page (D864), which lists every kind a KB
// can provide even when it has none, so a reader learns what a KB is able to
// give to its agent clients.
type KindInfo struct {
	// Kind is the artifact kind, as the manifest and the UI API name it.
	Kind string
	// Title is the kind in a heading ("Subagents").
	Title string
	// KBPath is where an artifact of this kind lives in the KB, with <name>
	// (<slug> for a template, which concept_new selects by slug) standing for
	// the artifact's name. Slash-separated, relative to the KB root.
	KBPath string
	// Doc is the repository path of the page that documents the kind, with the
	// anchor of the heading that does: "docs/sync.md#agents-and-hooks".
	Doc string
	// KBOnly marks a kind that never leaves the KB: it has no row in the
	// destination matrix and no client receives it.
	KBOnly bool
}

// kindInfos is the list behind Kinds. The layout is what BuildManifest scans
// (skills/, agents/, hooks/, mcp/, instructions.md) plus the two KB-only kinds
// the manifest ignores (templates/ and secrets/): TestKinds_PathsAreWhatTheScannerFinds
// writes a file at each KBPath and checks the scanner picks it up, and
// TestKindsMatchTheMatrix checks every matrix kind has a row here, so a kind
// added to either place without the other fails the build.
var kindInfos = []KindInfo{
	{Kind: "skill", Title: "Skills", KBPath: "skills/<name>/SKILL.md", Doc: "docs/skills-services-secrets.md#skills"},
	{Kind: "agent", Title: "Subagents", KBPath: "agents/<name>.md", Doc: "docs/sync.md#agents-and-hooks"},
	{Kind: "hook", Title: "Hooks", KBPath: "hooks/<name>/hook.json", Doc: "docs/sync.md#agents-and-hooks"},
	{Kind: "mcp", Title: "MCP servers", KBPath: "mcp/<name>.json", Doc: "docs/sync.md#mcp-servers-d69-d116"},
	{Kind: "instructions", Title: "Instructions", KBPath: "instructions.md", Doc: "docs/sync.md#instructions-imprinting"},
	{Kind: "template", Title: "Templates", KBPath: "templates/<slug>.md", Doc: "docs/data-plane.md#templates-are-page-schemas-d352", KBOnly: true},
	{Kind: "secret", Title: "Secrets", KBPath: "secrets/<name>.sops.yaml", Doc: "docs/skills-services-secrets.md#sops-files", KBOnly: true},
}

// Kinds lists every kind of artifact a KB can hold, in reading order: what an
// agent does, then what it is told, then what stays in the KB. The result is a
// copy, safe to modify.
func Kinds() []KindInfo {
	return append([]KindInfo(nil), kindInfos...)
}

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
// D230 hooks.json — registerHookSettings/registerCodexHook in
// hooksettings.go); on opencode it generates a whole plugin JS file instead
// (D59, registerOpenCodePlugin) when the hook's event has an OpenCode
// equivalent. All three are idempotent (re-apply never duplicates/drifts) and
// prunable (removed on diff.Removed/PruneManaged); see docs/sync.md §Agents and hooks.
// Agent content is translated per provider (D55/D58, see
// translateAgentForProvider); kiro never reaches that function because its
// "agent" cell is unsupported.
//
// Returns "" for an unsupported kind×provider combo — and likewise for a kind
// or provider this binary does not know — which causes Apply to place the
// artifact in Unsupported instead of materializing it.
// Destinations lists, in registry order, the providers an artifact of this kind
// is materialized for: the providers whose destination cell is supported. It
// reads the same matrix as destDir, so what the Atlas UI shows as an
// artifact's clients (D238) cannot drift from what sync writes.
func Destinations(kind, name string) []configurator.Provider {
	var out []configurator.Provider
	for _, d := range configurator.Providers() {
		if destDir(kind, name, d.Provider) != "" {
			out = append(out, d.Provider)
		}
	}
	return out
}

func destDir(kind, name string, provider configurator.Provider) string {
	cell, ok := destinationMatrix[kind][provider]
	if !ok || cell.unsupported {
		return ""
	}
	// Slash, like destDirScoped's: a destination is written into provider
	// configuration and into the lockfile, both read on every platform.
	if !cell.named {
		return path.Join(cell.dir...)
	}
	segments := append(append([]string{}, cell.dir...), name+cell.suffix)
	return path.Join(segments...)
}

// translateAgentForProvider adapts an "agent" artifact's content (a Claude Code
// subagent .md, see docs/sync.md §Agents and hooks) for materialization under a
// specific provider (D55). Claude receives the content verbatim (native format).
// OpenCode has its own native subagent format (.config/opencode/agents/<name>.md,
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
// codex (TOML, D58), kiro (JSON, D195) and antigravity have their own agent
// formats and are dispatched to their translators by the switch below; claude
// takes the source verbatim. Providers whose "agent" cell is unsupported never
// reach here: destDir returns "" for them, so Apply routes them to Unsupported
// before calling this function.
func translateAgentForProvider(provider configurator.Provider, name string, content []byte) ([]byte, error) {
	switch provider {
	case configurator.ProviderOpenCode:
		// falls through to the OpenCode translation below
	case configurator.ProviderCodex:
		return translateAgentForCodex(name, content)
	case configurator.ProviderAntigravity:
		return translateAgentForAntigravity(name, content)
	case configurator.ProviderCopilot:
		return translateAgentForCopilot(name, content)
	case configurator.ProviderKiro:
		return translateAgentForKiro(name, content)
	default:
		return content, nil
	}

	fmRaw, body, hasFM := okf.SplitFrontmatter(string(content))

	description := name
	var native []nativeField
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
		if native, err = nativeFieldsFor(fm, provider); err != nil {
			return nil, fmt.Errorf("provisioning: agent %s: %w", name, err)
		}
	} else {
		body = string(content)
	}
	extra, err := renderNativeYAML(native)
	if err != nil {
		return nil, fmt.Errorf("provisioning: agent %s: %w", name, err)
	}

	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("description: ")
	sb.WriteString(yamlQuoteScalar(description))
	sb.WriteString("\nmode: subagent\n")
	sb.WriteString(extra)
	sb.WriteString("---\n")
	sb.WriteString(body)

	return []byte(sb.String()), nil
}

// translateAgentForAntigravity emits the global Antigravity subagent format.
// The source prompt body and description are preserved; Claude-specific tool
// and model declarations are dropped because their names are not portable.
func translateAgentForAntigravity(name string, content []byte) ([]byte, error) {
	fmRaw, body, hasFM := okf.SplitFrontmatter(string(content))
	description := name
	var native []nativeField
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
		if native, err = nativeFieldsFor(fm, configurator.ProviderAntigravity); err != nil {
			return nil, fmt.Errorf("provisioning: agent %s: %w", name, err)
		}
	} else {
		body = string(content)
	}
	extra, err := renderNativeYAML(native)
	if err != nil {
		return nil, fmt.Errorf("provisioning: agent %s: %w", name, err)
	}

	var sb strings.Builder
	sb.WriteString("---\nname: ")
	sb.WriteString(yamlQuoteScalar(name))
	sb.WriteString("\ndescription: ")
	sb.WriteString(yamlQuoteScalar(description))
	sb.WriteString("\nmainAgent: false\nsubagent: true\n")
	sb.WriteString(extra)
	sb.WriteString("---\n")
	sb.WriteString(body)
	return []byte(sb.String()), nil
}

// translateAgentForCopilot emits ~/.copilot/agents/<name>.agent.md (D676,
// probed on 1.0.94): frontmatter `name` and `description`, then the body.
// Claude-only keys (`tools`, `model`) are not copied — their names are not
// portable, as for the other translators — and the `providers.copilot` entry
// is appended through nativeFieldsFor.
func translateAgentForCopilot(name string, content []byte) ([]byte, error) {
	fmRaw, body, hasFM := okf.SplitFrontmatter(string(content))
	description := name
	var native []nativeField
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
		if native, err = nativeFieldsFor(fm, configurator.ProviderCopilot); err != nil {
			return nil, fmt.Errorf("provisioning: agent %s: %w", name, err)
		}
	} else {
		body = string(content)
	}
	extra, err := renderNativeYAML(native)
	if err != nil {
		return nil, fmt.Errorf("provisioning: agent %s: %w", name, err)
	}

	var sb strings.Builder
	sb.WriteString("---\nname: ")
	sb.WriteString(yamlQuoteScalar(name))
	sb.WriteString("\ndescription: ")
	sb.WriteString(yamlQuoteScalar(description))
	sb.WriteString("\n")
	sb.WriteString(extra)
	sb.WriteString("---\n")
	sb.WriteString(body)
	return []byte(sb.String()), nil
}

// translateAgentForKiro adapts an "agent" artifact's content (a Claude Code
// subagent .md) into Kiro's agent config (D195 — ~/.kiro/agents/<name>.json).
//
// The format is **JSON**, established by generating a config with
// `kiro-cli agent create -f kiro_default` on 2.21.3 and reading what the client
// itself writes: `name`, `description` and `prompt` are the fields that carry
// an agent, and a config holding only those is listed by `kiro-cli agent list`.
// The vendor documentation describes a Markdown form; the shipped client does
// not discover one, which is why this is derived from the client rather than
// from the docs.
//
// `tools` and `model` are dropped, as they are for Codex and Antigravity: their
// names are not portable across clients, and inventing a mapping would hand the
// agent capabilities its author never granted. Omitting `tools` leaves Kiro's
// own default in force.
func translateAgentForKiro(name string, content []byte) ([]byte, error) {
	fmRaw, body, hasFM := okf.SplitFrontmatter(string(content))
	description := name
	var native []nativeField
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
		if native, err = nativeFieldsFor(fm, configurator.ProviderKiro); err != nil {
			return nil, fmt.Errorf("provisioning: agent %s: %w", name, err)
		}
	} else {
		// No frontmatter: the whole file is the prompt and the name stands in
		// for the description, the same fallback the other translations use.
		body = string(content)
	}

	// Built member by member so the author's `providers.kiro` fields (D291) land
	// after ours, in their own order, without a round trip through a Go map.
	members := make([]string, 0, 3+len(native))
	for _, kv := range [][2]string{{"name", name}, {"description", description}, {"prompt", body}} {
		k, _ := json.Marshal(kv[0])
		v, err := json.Marshal(kv[1])
		if err != nil {
			return nil, fmt.Errorf("provisioning: encode agent %s for kiro: %w", name, err)
		}
		members = append(members, string(k)+": "+string(v))
	}
	extra, err := renderNativeJSONPairs(native)
	if err != nil {
		return nil, fmt.Errorf("provisioning: agent %s: %w", name, err)
	}
	members = append(members, extra...)
	var out bytes.Buffer
	if err := json.Indent(&out, []byte("{"+strings.Join(members, ", ")+"}"), "", "  "); err != nil {
		return nil, fmt.Errorf("provisioning: encode agent %s for kiro: %w", name, err)
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
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
	var native []nativeField
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
		if native, err = nativeFieldsFor(fm, configurator.ProviderCodex); err != nil {
			return nil, fmt.Errorf("provisioning: agent %s: %w", name, err)
		}
	} else {
		body = string(content)
	}
	extra, err := renderNativeTOML(native)
	if err != nil {
		return nil, fmt.Errorf("provisioning: agent %s: %w", name, err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "name = %s\n", configurator.QuoteTOMLString(name))
	fmt.Fprintf(&sb, "description = %s\n", configurator.QuoteTOMLString(description))
	sb.WriteString("developer_instructions = ")
	sb.WriteString(configurator.QuoteTOMLMultiline(strings.TrimRight(body, "\n")))
	sb.WriteString("\n")
	// The author's `providers.codex` fields (D291): top-level keys after ours, so
	// they cannot fall inside a table.
	sb.WriteString(extra)

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

// resolutionDriftIDs returns the placeholders cited by a (a file-expanded
// kind) that resolve, in this Apply, to something other than what the lock
// recorded when it was last applied (#967); nil when none does. A lock that
// recorded no placeholder state at all (written before D262, or by a
// server-side Apply) knows nothing to compare against, so it never reports
// drift. A file that cannot be read is reported as a warning, never fatal.
func resolutionDriftIDs(a Artifact, opts ApplyOptions, tracker *expansionTracker) []string {
	if !opts.ExpandPlaceholders || a.Kind == "instructions" || a.Kind == "mcp" {
		return nil
	}
	if opts.Lock.ResolvedPlaceholders == nil && opts.Lock.UnresolvedPlaceholders == nil {
		return nil
	}
	files := a.Files
	if len(files) == 0 {
		var err error
		if files, err = ReadArtifactFiles(a, opts.BundleFS, opts.KBRoots); err != nil {
			tracker.warnings = append(tracker.warnings, fmt.Sprintf(
				"%s %q: could not read its files to check placeholder resolutions: %v", a.Kind, a.Name, err))
			return nil
		}
	}
	var out []string
	seen := map[string]bool{}
	for _, f := range files {
		for _, id := range okf.Placeholders(string(f.Content)) {
			kind, key, ok := okf.SplitPlaceholderID(id)
			if !ok || seen[id] {
				continue
			}
			seen[id] = true
			cur, resolved := tracker.resolve(kind, key, opts)
			if resolved {
				if cur != opts.Lock.ResolvedPlaceholders[id] {
					out = append(out, id)
				}
			} else if _, was := opts.Lock.UnresolvedPlaceholders[id]; !was {
				out = append(out, id)
			}
		}
	}
	return out
}

// ComputeDiffResolved is ComputeDiff plus resolution drift (#967): an artifact
// whose cited placeholders resolve differently from what lock recorded joins
// Updated and the diff is no longer in sync. opts carries what Apply would
// resolve with (Paths, SearchRoots, Placeholders, PathRegistries, KBOrder,
// KBRoots, BundleFS, ExpandPlaceholders); status uses it to agree with sync.
func ComputeDiffResolved(m Manifest, lock Lock, opts ApplyOptions) Diff {
	d := ComputeDiff(m, lock)
	if !opts.ExpandPlaceholders {
		return d
	}
	opts.Lock = lock
	tracker := newExpansionTracker()
	var declaredBy map[string][]string
	tracker.decls, declaredBy, _ = MergePathRegistries(opts.KBOrder, opts.PathRegistries)
	sources := placeholderSources(m, opts)
	ids := make([]string, 0, len(sources))
	for id := range sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	tracker.preResolve(ids, opts)
	tracker.resolveDeclared(sources, declaredBy, opts)
	queued := make(map[string]bool, len(d.Added)+len(d.Updated))
	for _, a := range append(append([]Artifact(nil), d.Added...), d.Updated...) {
		queued[a.Kind+"\x00"+a.Name] = true
	}
	for _, a := range m.Artifacts {
		if queued[a.Kind+"\x00"+a.Name] || !artifactAuthorized(a, opts) {
			continue
		}
		if len(resolutionDriftIDs(a, opts, tracker)) > 0 {
			d.Updated = append(d.Updated, a)
			d.InSync = false
		}
	}
	// The instructions block carries no per-artifact resolution list: sync
	// rewrites it when the hash of its client-owned tail (shared section and
	// "Local paths" table) changes, so status asks the same question (#1225).
	for _, a := range instructionsResolutionDrift(m, opts, tracker) {
		if !queued[a.Kind+"\x00"+a.Name] {
			d.Updated = append(d.Updated, a)
			d.InSync = false
		}
	}
	return d
}

// instructionsResolutionDrift returns the authorized instructions artifacts
// when the paths-section hash Apply would record differs from the lock's: the
// same comparison applyInstructionsGroup makes. tracker must already be
// pre-resolved the way Apply does.
func instructionsResolutionDrift(m Manifest, opts ApplyOptions, tracker *expansionTracker) []Artifact {
	if instructionsDest(opts) == "" {
		return nil
	}
	var signed []Artifact
	var sections []string
	for _, a := range m.Artifacts {
		if a.Kind != "instructions" || !artifactAuthorized(a, opts) {
			continue
		}
		content, err := singleArtifactContent(a, opts)
		if err != nil {
			return nil
		}
		signed = append(signed, a)
		sections = append(sections, string(expandPlaceholders(content, opts, tracker)))
	}
	if len(signed) == 0 {
		return nil
	}
	var shared string
	if sharedSectionWanted(sections) {
		shared = SharedOperationalSection()
	}
	if pathsSectionHash(shared+"\x00"+buildPathsSection(tracker.resolved)) == opts.Lock.PathsSectionHash {
		return nil
	}
	return signed
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
// the paths relative to opts.BaseDir, the artifact-relative paths written
// executable (recorded in the lockfile, see ManagedFile.ExecutablePaths), the
// hash of what was actually written (after placeholder expansion, D75 WP3, and
// provenance stamping, D138 — this is the materialized hash, never the source
// hash ComputeDiff compares), and a non-fatal warning when the artifact could
// not be stamped. The content source is a.Files if populated (remote client via
// sync_pull, no filesystem shared with the server), otherwise BundleFS/KBRoots
// (local stdio deployment, same filesystem).
func copyArtifactFiles(a Artifact, opts ApplyOptions, fullDestDir string, tracker *expansionTracker) (relPaths, execPaths []string, materializedHash, warning string, err error) {
	files := a.Files
	if len(files) == 0 {
		var err error
		files, err = ReadArtifactFiles(a, opts.BundleFS, opts.KBRoots)
		if err != nil {
			return nil, nil, "", "", err
		}
	}

	// Files Cartographer generates on top of the KB's own (SOURCE.env, D316). A collision with a KB file of the same name fails this one
	// artifact — with a warning, before anything is written — and leaves the
	// rest of the sync to complete.
	generated, collision, ok := generatedArtifactFiles(a, opts, files)
	if !ok {
		// The caller created fullDestDir just before this call: remove it
		// again so a refused delivery leaves no empty directory that looks
		// like one. os.Remove refuses a non-empty directory, so an earlier
		// successful delivery survives untouched.
		_ = os.Remove(fullDestDir)
		if rel, relErr := filepath.Rel(opts.BaseDir, fullDestDir); relErr == nil {
			pruneEmptyDirs(opts.BaseDir, rel)
		}
		return nil, nil, "", collision, nil
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
	dstPaths := make([]string, 0, len(files))
	for _, f := range files {
		// Paths can arrive from a remote server via sync_pull: reject
		// absolute paths and traversal outside the artifact's directory.
		local := filepath.FromSlash(f.Path)
		if !filepath.IsLocal(local) {
			return nil, nil, "", "", fmt.Errorf("provisioning: invalid file path %q in %s", f.Path, a.Name)
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
		executable := effectiveExecutable(a.Kind, local, f.Executable)
		expanded = append(expanded, ArtifactFile{Path: f.Path, Content: content, Executable: executable})
		// The same flag, recorded in the lockfile: it is the only way a client
		// whose filesystem has no execute bit can reproduce this hash on the
		// next check (ManagedFile.ExecutablePaths).
		if executable {
			execPaths = append(execPaths, filepath.ToSlash(f.Path))
		}

		// Every destination is checked before the first one is written: a
		// refusal part-way would leave the earlier files rewritten and the
		// later ones unrecorded, so a refused delivery touches nothing.
		dstPath := filepath.Join(fullDestDir, local)
		if err := safewrite.CheckPath(opts.BaseDir, dstPath); err != nil {
			return nil, nil, "", "", err
		}
		dstPaths = append(dstPaths, dstPath)
	}

	for i, f := range expanded {
		dstPath := dstPaths[i]
		if err := mkdirAllNoFollow(opts.BaseDir, filepath.Dir(dstPath), 0o755); err != nil {
			return nil, nil, "", "", err
		}
		// A hook's scripts are invoked by path from the registered entry
		// (e.g. ./bootstrap.sh → absolute path in settings.json): without the
		// executable bit the hook fails with "Permission denied" on every SessionStart.
		// The explicit Chmod is needed for files that already exist: WriteFile only
		// applies mode on creation.
		mode := os.FileMode(0o644)
		if f.Executable {
			mode = 0o755
		}
		if err := writeFileNoFollow(dstPath, f.Content, mode); err != nil {
			return nil, nil, "", "", err
		}
		if err := os.Chmod(dstPath, mode); err != nil {
			return nil, nil, "", "", err
		}
		rel, err := filepath.Rel(opts.BaseDir, dstPath)
		if err != nil {
			return nil, nil, "", "", err
		}
		// A written path is reported and recorded, not walked: slash, like the
		// destination it was joined from.
		relPaths = append(relPaths, filepath.ToSlash(rel))
	}

	// A malformed KB artifact must not fail the whole sync: BuildManifest does
	// not require the principal file either.
	if stampTarget != "" && !stamped {
		warning = fmt.Sprintf("%s %q has no %s: materialized without a provenance block", a.Kind, a.Name, stampTarget)
	}

	// The hash of what actually landed on disk (D138), always computed: the
	// caller keeps a.ContentHash as the source hash for ComputeDiff.
	return relPaths, execPaths, hashArtifactFiles(expanded), warning, nil
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
			files = append(files, ArtifactFile{Path: rel, Content: data, Executable: execbit.IsExecutable(fileInfo.Mode())})
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
		files = append(files, ArtifactFile{Path: filepath.ToSlash(rel), Content: data, Executable: execbit.IsExecutable(fileInfo.Mode())})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// AgentRestriction is the part of a Claude Code subagent's frontmatter that
// only Claude Code enforces (D283).
type AgentRestriction struct {
	// Tools is the declared `tools` allow-list, rendered as one line.
	Tools string
	// Strict is `strict_tools: true`: the author prefers the agent not exist on
	// a client that cannot enforce Tools over existing there unrestricted.
	Strict bool
	// Native lists, sorted, the clients with a non-empty `providers:` entry
	// (D291): the author wrote that client's own restriction, so the agent is
	// not widened there — it is neither warned about nor skipped by Strict.
	Native []string
}

// ParseAgentRestriction reads an agent's source frontmatter and returns its
// restriction, or nil when it declares no `tools` (nothing to widen) or the
// frontmatter cannot be parsed (the translation reports that on its own).
func ParseAgentRestriction(content []byte) *AgentRestriction {
	fmRaw, _, hasFM := okf.SplitFrontmatter(string(content))
	if !hasFM {
		return nil
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		return nil
	}
	v, ok := fm.Get("tools")
	if !ok {
		return nil
	}
	var tools string
	switch t := v.(type) {
	case string:
		tools = strings.TrimSpace(t)
	case []string:
		tools = strings.Join(t, ", ")
	}
	if tools == "" {
		return nil
	}
	r := &AgentRestriction{Tools: tools, Native: nativeProvidersDeclared(fm)}
	if sv, ok := fm.Get("strict_tools"); ok {
		if str, ok := sv.(string); ok && strings.EqualFold(strings.TrimSpace(str), "true") {
			r.Strict = true
		}
	}
	return r
}

// agentWidened reports that installing this agent on provider drops a `tools`
// allow-list. Claude Code receives the source verbatim, so it enforces it, and a
// client the author gave a `providers:` entry (D291) is not widened either.
func agentWidened(a Artifact, provider configurator.Provider) bool {
	if a.Kind != "agent" || a.Restriction == nil || provider == configurator.ProviderClaudeCode {
		return false
	}
	// A `providers.<client>` entry is the author's own native restriction for
	// that client (D291): nothing is widened there.
	for _, p := range a.Restriction.Native {
		if p == string(provider) {
			return false
		}
	}
	return true
}

// strictAgentSkipped reports that the agent opted out of being widened
// (strict_tools) and provider cannot enforce its restriction.
func strictAgentSkipped(a Artifact, provider configurator.Provider) bool {
	return agentWidened(a, provider) && a.Restriction.Strict
}
