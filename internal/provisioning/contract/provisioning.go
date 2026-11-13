package contract

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/artifactsig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/execbit"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/skill"
)

// Artifact describes a single provisioning artifact.
// Kind makes it extensible to "hook", "agent" etc. without changing the flow.
type Artifact struct {
	Kind        string `json:"kind"` // "skill" (extensible)
	Name        string `json:"name"`
	Source      string `json:"source"` // "bundle" | "kb:<name>" | "plugin:<name>"
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
	// manifest exposed by sync_check/sync_pull (that uses dedicated
	// structures).
	Files []ArtifactFile `json:"-"`

	// Restriction is what an "agent" artifact's source declares that a
	// non-Claude client cannot express (D283). Derived from the source content
	// by whoever builds the Artifact (BuildManifest, the client's sync_pull
	// decoding), never sent on the wire: nil for every other kind and for an
	// agent that declares no `tools`.
	Restriction *AgentRestriction `json:"-"`

	// Clients is the `clients:` allow-list of an "agent" artifact: the ids of the
	// clients it is for, empty for every client. Derived like Restriction, never
	// sent; AgentSkipped is its one reader.
	Clients []string `json:"-"`

	// DerivedFrom is set on a skill the client derived from a KB agent because
	// it has no subagents (projection.go). Client-side only, never sent, outside
	// the hash and every signature; approval and provenance resolve to the origin.
	DerivedFrom *DerivedFrom `json:"-"`
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
	// IncludeCLISkill lists the bundled CLISkillName skill. False for every
	// built-in caller: no built-in provider reaches Cartographer through
	// `cartographer call`, so the server leaves the skill out of sync_check
	// and sync_pull and an older client, which has no client-side
	// filter, never receives it. sync_pull sets it only when the client asks
	// (include_cli_skill), which every current client does (it filters the skill
	// per provider, cliskill.go).
	IncludeCLISkill bool
}

// CLISkillName is the bundled skill that teaches `cartographer call`.
const CLISkillName = "cartographer-cli"

// ArtifactFile is a single file of an Artifact, with content in memory.
type ArtifactFile struct {
	Path       string // path relative to the artifact's (skill) folder
	Content    []byte
	Executable bool
	// Oversized is the size of a file over ArtifactMaxFileSize: its Content is
	// then nil, the file was never read, and no consumer may write or ship it.
	// Only ReadArtifactFilesCapped returns one; ReadArtifactFiles fails instead.
	Oversized int64
}

// ArtifactMaxFileSize caps one file of a KB artifact read from disk (256 KiB,
// the cap artifact_write enforces): a file over it is never read into memory.
const ArtifactMaxFileSize = 256 * 1024

// oversizedMarker stands for the bytes of an over-cap file in the content hash:
// the hash must not need the file, and a changed size still changes it.
func oversizedMarker(size int64) []byte { return []byte(fmt.Sprintf("oversized:%d", size)) }

// AgentFrontmatterIssuePrefix starts the manifest issue BuildManifest adds for
// an agent whose frontmatter is not valid YAML. The agent stays in the
// catalogue, held for approval, so a client that shows the issue list (status)
// recognises the line by this prefix and names the agent as held instead of as
// left out.
//
// The text is a contract between a server and clients of other versions: a
// client matches it against the issues of a server it was not built with, so
// change it only with a fallback (the old text still recognised) and a decision.
// TestAgentFrontmatterIssuePrefix_IsStable pins it.
func AgentFrontmatterIssuePrefix(kb, agent string) string {
	return fmt.Sprintf("KB %q: agent %q: its frontmatter is not valid YAML", kb, agent)
}

// KBIssue is one manifest issue and the KB whose sync_pull reported it.
type KBIssue struct{ KB, Text string }

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
	// KBIssues is client-side only: the issues of the KBs a projection reads,
	// each with its KB ("" for the unnamed endpoint), and ReadKBs those KBs.
	// The run-level list deduplicates on (KB, issue) and prefixes the KB only
	// when the run as a whole reads more than one (#1799).
	KBIssues []KBIssue `json:"-"`
	ReadKBs  []string  `json:"-"`
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
	// FeedbackMode is client-side only: the feedback.destination mode the
	// pulled KBs' server reported ("" is github). Read off the projection's
	// manifest into ApplyOptions.FeedbackMode.
	FeedbackMode string `json:"-"`
}

// ContentHashDir computes a deterministic sha256 hex of the entire dir folder
// inside fsys. Walks files in alphabetical order of relative path, including
// relative path and each file's bytes in the hash.
//
// Use this for Artifact.ContentHash: a skill is a folder, not just SKILL.md.
func ContentHashDir(fsys fs.FS, dir string) (string, error) {
	return ContentHashDirWith(fsys, dir, nil, nil, 0)
}

// ContentHashDirWith hashes dir. With maxFile > 0 (a KB's own artifact on disk) a
// symlink is a *contract.KBSymlinkError, never followed (contract/kblinks.go), and a file over maxFile is hashed by its
// size without being read.
func ContentHashDirWith(fsys fs.FS, dir string, executable func(string, bool) bool, include func(rel string) bool, maxFile int64) (string, error) {
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

	var total int64
	count := 0
	for _, p := range paths {
		// Compute the path relative to the root dir.
		rel := p
		prefix := dir + "/"
		if strings.HasPrefix(p, prefix) {
			rel = p[len(prefix):]
		}
		// Case dir == ".": WalkDir returns paths without "./" prefix, rel = path.
		if include != nil && !include(rel) {
			continue
		}
		stat := fs.Stat
		if maxFile > 0 {
			stat = fs.Lstat
		}
		info, statErr := stat(fsys, p)
		if statErr != nil {
			return "", fmt.Errorf("provisioning: stat %s: %w", p, statErr)
		}
		if maxFile > 0 && info.Mode()&fs.ModeSymlink != 0 {
			return "", &KBSymlinkError{Rel: rel} // a KB's tree is never read through a link (contract/kblinks.go)
		}
		exec := execbit.IsExecutable(info.Mode())
		if executable != nil {
			exec = executable(rel, exec)
		}
		if maxFile > 0 && info.Size() > maxFile {
			files = append(files, ArtifactFile{Path: rel, Content: oversizedMarker(info.Size()), Executable: exec})
			continue
		}
		if maxFile > 0 {
			// The directory budget of ReadDirFiles: past it nothing more is
			// read, and the hash records the file that crossed it.
			count++
			if count > ArtifactMaxFiles || total+info.Size() > ArtifactMaxTotalSize {
				files = append(files, ArtifactFile{Path: rel, Content: oversizedMarker(max(info.Size(), 1)), Executable: exec})
				break
			}
			total += info.Size()
		}
		data, readErr := fs.ReadFile(fsys, p)
		if readErr != nil {
			return "", fmt.Errorf("provisioning: read %s: %w", p, readErr)
		}
		files = append(files, ArtifactFile{Path: rel, Content: data, Executable: exec})
	}
	return HashArtifactFiles(files), nil
}

func EffectiveExecutable(kind, path string, executable bool) bool {
	if kind != "hook" {
		return executable
	}
	return filepath.Base(path) != "hook.json"
}

// ContentHashDirTracked hashes a directory of a KB's tree, restricted to the
// files include accepts (paths relative to dirPath); nil accepts all. The
// server uses it to hash only what git tracks in a KB (#1325), and it refuses a
// symlink among them (contract/kblinks.go).
func ContentHashDirTracked(dirPath, kind string, include func(rel string) bool) (string, error) {
	return ContentHashDirWith(os.DirFS(dirPath), ".", func(path string, executable bool) bool {
		return EffectiveExecutable(kind, path, executable)
	}, include, ArtifactMaxFileSize)
}

// ValidArtifactFilePath reports whether p is a file path a server may send for
// an artifact: already in canonical slash form (path.Clean(p) == p, so no "./",
// "..", doubled or trailing separator, and no backslash where it separates),
// relative and inside the artifact directory. Every comparison against a file
// name ("SKILL.md" for the approval gate and the provenance stamp) is then
// exact, because the spelling on the wire is the spelling written to disk.
func ValidArtifactFilePath(p string) bool {
	if p == "" || p == "." || path.Clean(p) != p {
		return false
	}
	local := filepath.FromSlash(p)
	if !filepath.IsLocal(local) || filepath.ToSlash(filepath.Clean(local)) != p || strings.ContainsAny(p, "\x00:") || strings.IndexFunc(p, unicode.IsControl) >= 0 {
		return false
	}
	// Windows writes "SKILL.md." and "SKILL.md " as "SKILL.md" and resolves a
	// device name in any component: every component is checked, on every OS, so
	// the path on the wire is the path on disk.
	for _, c := range strings.Split(p, "/") {
		if strings.HasSuffix(c, ".") || strings.HasSuffix(c, " ") || WindowsReservedName(c) {
			return false
		}
	}
	return true
}

// ValidArtifactName reports whether name can be the single directory or file
// segment an agent or hook is materialized under: not empty, one local segment
// (no separator of either kind, no "..", no Windows reserved name where
// filepath.IsLocal knows them), no control character (C0, DEL and C1: unicode.IsControl), no surrounding blanks.
// BuildManifest excludes an artifact with a bad name (D191 for skills) and Apply
// refuses one per artifact, since a manifest can come from a remote server.
func ValidArtifactName(name string) bool {
	if name == "" || name == "." || name == ".." || name != filepath.Base(name) || !filepath.IsLocal(name) {
		return false
	}
	if WindowsReservedName(name) {
		return false
	}
	// Windows drops a trailing dot or blank and reads ":" as an NTFS stream:
	// "foo." would be written as "foo", so none of them is a spelling of a
	// different name on any OS.
	return !strings.ContainsAny(name, "\\/:") && strings.IndexFunc(name, unicode.IsControl) < 0 && strings.TrimSpace(name) == name && !strings.HasSuffix(name, ".")
}

// WindowsReservedName reports a device name Windows resolves to a device
// whatever the extension ("con", "NUL.txt", "com1"), checked on every OS so a
// manifest means the same thing on all of them.
func WindowsReservedName(name string) bool {
	base := name
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	switch b := strings.ToUpper(strings.TrimRight(base, " ")); b {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$":
		return true
	default:
		if len(b) == 4 && (strings.HasPrefix(b, "COM") || strings.HasPrefix(b, "LPT")) && b[3] >= '0' && b[3] <= '9' {
			return true
		}
	}
	return false
}

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
			if name == CLISkillName && !opts.IncludeCLISkill {
				continue
			}
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
		tracked, err := KBTrackedFilter(kbRoot)
		if err != nil {
			// One KB whose git cannot answer withholds its own artifacts and
			// nothing else (D1325).
			issue := fmt.Sprintf("KB %q: artifacts withheld: %v", kbName, err)
			issues = append(issues, issue)
			if opts.SkillDiagnostic != nil {
				opts.SkillDiagnostic(issue)
			}
			continue
		}

		// A link refuses one artifact (or one kind's directory) and names it;
		// every other artifact of the KB is built as usual (contract/kblinks.go).
		linkOut := func(what, link string) {
			issue := KBLinkIssue(kbName, what, link)
			issues = append(issues, issue)
			if opts.SkillDiagnostic != nil {
				opts.SkillDiagnostic(issue)
			}
		}
		kindDirLinked := func(dir string) bool {
			link, lerr := FirstKBLink(kbRoot, dir)
			if lerr != nil || link == "" {
				return false
			}
			linkOut(dir+"/", link)
			return true
		}

		// 2. Skill (skills/<name>/SKILL.md, several files per artifact). A KB with
		// no skills/ folder or an unreadable directory: kbSkills is empty,
		// silent skip (no fatal error).
		var kbSkills []skill.Skill
		var loadErrs []error
		if !kindDirLinked("skills") {
			kbSkills, loadErrs = skill.LoadAllSkills(kbRoot)
			// LoadAllSkills lists real directories only: a link in their place
			// is named here instead of vanishing.
			if linked, rerr := os.ReadDir(filepath.Join(kbRoot, "skills")); rerr == nil {
				for _, e := range linked {
					if e.Type()&fs.ModeSymlink != 0 && tracked.Has("skills/"+e.Name()) {
						linkOut("skill skills/"+e.Name(), "skills/"+e.Name())
					}
				}
			}
		}
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
			skillInclude := tracked.Under(s.DirPath)
			if !tracked.Has(s.DirPath + "/SKILL.md") {
				continue // nothing of it is tracked: not an artifact (#1325)
			}
			hash, err := ContentHashDirTracked(skillDir, "skill", skillInclude)
			if err != nil {
				var linkErr *KBSymlinkError
				if errors.As(err, &linkErr) {
					skillNames = skillNames[:len(skillNames)-1]
					linkOut("skill "+s.DirPath, s.DirPath+"/"+linkErr.Rel)
					continue
				}
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
		if err == nil && !kindDirLinked("agents") {
			for _, e := range agentEntries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
					continue
				}
				name := strings.TrimSuffix(e.Name(), ".md")
				if !tracked.Has("agents/" + e.Name()) {
					continue
				}
				if !ValidArtifactName(name) {
					issues = append(issues, fmt.Sprintf("KB %q: agent %q excluded: not a valid artifact name", kbName, e.Name()))
					continue
				}
				if e.Type()&fs.ModeSymlink != 0 {
					linkOut("agent agents/"+e.Name(), "agents/"+e.Name())
					continue
				}
				hash, err := ContentHashFile(filepath.Join(agentsDir, e.Name()))
				if errors.Is(err, ErrNotRegular) {
					// One tracked symlink leaves out that artifact, not the KB.
					issues = append(issues, fmt.Sprintf("KB %q: agent %q left out: not a regular file", kbName, name))
					continue
				}
				if err != nil {
					return Manifest{}, fmt.Errorf("provisioning: hash kb:%s/agents/%s: %w", kbName, name, err)
				}
				agent := Artifact{
					Kind:        "agent",
					Name:        name,
					Source:      "kb:" + kbName,
					ContentHash: hash,
				}
				if raw, size, rerr := ReadFileCapped(filepath.Join(agentsDir, e.Name())); rerr == nil && size == 0 {
					agent.Restriction = ParseAgentRestriction(raw)
					agent.Clients = AgentClientsOf(raw)
					// A file that came by git skipped artifact_write's check. The
					// agent stays (the approval gate cannot tell what it declares,
					// so a client installs it only once this content is approved),
					// but the reason is named. The manifest is built without
					// knowing approvals: the line must hold whatever they are.
					if yerr := ValidateFrontmatterYAML(raw); yerr != nil {
						issue := AgentFrontmatterIssuePrefix(kbName, name) + fmt.Sprintf(" (%v), so the approval gate cannot read what it declares: "+
							"a client installs it only once this content is approved; quoting the value fixes it (the usual cause is an unquoted value containing \": \")", yerr)
						issues = append(issues, issue)
						if opts.SkillDiagnostic != nil {
							opts.SkillDiagnostic(issue)
						}
					} else if cerr := validateClientsOf(raw); cerr != nil {
						// Parses, but `clients` is not usable: the agent goes to every client.
						issue := AgentFrontmatterIssuePrefix(kbName, name) + fmt.Sprintf(" (%v), so it is projected to every client", cerr)
						issues = append(issues, issue)
						if opts.SkillDiagnostic != nil {
							opts.SkillDiagnostic(issue)
						}
					}
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
		if err == nil && !kindDirLinked("hooks") {
			for _, e := range hookEntries {
				if e.Type()&fs.ModeSymlink != 0 && tracked.Has("hooks/"+e.Name()) {
					linkOut("hook hooks/"+e.Name(), "hooks/"+e.Name())
					continue
				}
				if !e.IsDir() {
					continue
				}
				name := e.Name()
				if !tracked.HasAny("hooks/" + name + "/") {
					continue
				}
				if !ValidArtifactName(name) {
					issues = append(issues, fmt.Sprintf("KB %q: hook %q excluded: not a valid artifact name", kbName, name))
					continue
				}
				hash, err := ContentHashDirTracked(filepath.Join(hooksDir, name), "hook", tracked.Under("hooks/"+name))
				if err != nil {
					var linkErr *KBSymlinkError
					if errors.As(err, &linkErr) {
						linkOut("hook hooks/"+name, "hooks/"+name+"/"+linkErr.Rel)
						continue
					}
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
		if err == nil && !kindDirLinked("mcp") {
			for _, e := range mcpEntries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
					continue
				}
				name := strings.TrimSuffix(e.Name(), ".json")
				if !tracked.Has("mcp/" + e.Name()) {
					continue
				}
				if e.Type()&fs.ModeSymlink != 0 {
					linkOut("mcp mcp/"+e.Name(), "mcp/"+e.Name())
					continue
				}
				mcpPath := filepath.Join(mcpDir, e.Name())
				data, mcpSize, readErr := ReadFileCapped(mcpPath)
				if errors.Is(readErr, ErrNotRegular) {
					issues = append(issues, fmt.Sprintf("KB %q: mcp %q left out: not a regular file", kbName, name))
					continue
				}
				if readErr != nil {
					return Manifest{}, fmt.Errorf("provisioning: read kb:%s/mcp/%s: %w", kbName, name, readErr)
				}
				if mcpSize == 0 { // an over-cap descriptor is never parsed; sync_pull leaves it out
					if _, specErr := ParseMCPServerSpec(name, data); specErr != nil {
						return Manifest{}, fmt.Errorf("provisioning: %w", specErr)
					}
				}
				hash, hashErr := ContentHashFile(mcpPath)
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
			ContentHash: HashArtifactFiles([]ArtifactFile{{Path: "instructions.md", Content: instrContent}}),
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
		sb.WriteString(PreambleNoneComment + "\n")
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

var preambleNoneRe = regexp.MustCompile(`(?i)^<!--\s*cartographer:\s*preamble:\s*none\s*-->\s*$`)

var blockFullRe = regexp.MustCompile(`(?i)^<!--\s*cartographer:\s*block:\s*full\s*-->\s*$`)

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

// ErrNotRegular is what ReadFileCapped wraps for a symlink, a device or any
// other file that is not a regular one: a caller that answers per artifact
// skips that artifact instead of failing the KB.
var ErrNotRegular = errors.New("not a regular file")

const ArtifactMaxTotalSize = 8 << 20

const ArtifactMaxFiles = 512

// ReadFileCapped reads a KB's own file without following a symlink and without
// reading one over ArtifactMaxFileSize: for that, data is nil and size is the
// file's size. A file that is not regular is an ErrNotRegular. The read goes
// through the opened handle and a LimitReader, so a file that grows or is
// swapped after the Lstat is still read at most cap+1 bytes.
func ReadFileCapped(path string) (data []byte, size int64, err error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, fmt.Errorf("%s: %w", path, ErrNotRegular)
	}
	if info.Size() > ArtifactMaxFileSize {
		return nil, info.Size(), nil
	}
	return readOpenedCapped(path, info)
}

// readOpenedCapped opens path, checks it is still the regular file lstat saw,
// and reads at most ArtifactMaxFileSize+1 bytes of it.
func readOpenedCapped(path string, lstat os.FileInfo) (data []byte, size int64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	if !fi.Mode().IsRegular() || !os.SameFile(lstat, fi) {
		return nil, 0, fmt.Errorf("%s: %w", path, ErrNotRegular)
	}
	data, err = io.ReadAll(io.LimitReader(f, ArtifactMaxFileSize+1))
	if err != nil {
		return nil, 0, err
	}
	if len(data) > ArtifactMaxFileSize {
		return nil, int64(len(data)), nil
	}
	return data, 0, nil
}

// ContentHashFile computes the versioned artifact hash of a single,
// non-executable file (agents and MCP descriptors).
func ContentHashFile(path string) (string, error) {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return "", &KBSymlinkError{Rel: filepath.Base(path)}
	}
	data, size, err := ReadFileCapped(path)
	if err != nil {
		return "", fmt.Errorf("provisioning: read %s: %w", path, err)
	}
	if size > 0 {
		data = oversizedMarker(size)
	}
	return HashArtifactFiles([]ArtifactFile{{Path: filepath.Base(path), Content: data}}), nil
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
		Revision:  ComputeRevision(merged),
		Artifacts: merged,
	}
}

// preferArtifact chooses, between two artifacts with the same kind+name, the one
// that wins in materialization. The KB ("kb:*") beats the bundle; for the same
// source type, the choice is deterministic by alphabetical source. Between two
// KBs this is a silent, unstable choice, which is why the client merges through
// MergeArtifactsStrict and never reaches this branch (D171).
func preferArtifact(a, b Artifact) Artifact {
	// A plugin never beats a bundled or KB artifact (callers drop such a
	// collision before they get here: DropPluginCollisions).
	if aP, bP := IsPluginSource(a.Source), IsPluginSource(b.Source); aP != bP {
		if aP {
			return b
		}
		return a
	}
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

// ComputeRevision computes the Manifest's aggregate revision.
// Artifacts must already be sorted to guarantee determinism.
func ComputeRevision(artifacts []Artifact) string {
	h := sha256.New()
	for _, a := range artifacts {
		fmt.Fprintf(h, "%s|%s|%s|%s\n", a.Kind, a.Name, a.Source, a.ContentHash)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// destination is one cell of the kind × provider matrix below: where an
// artifact of that kind materializes for that provider. A cell either names a
// destination or is explicitly unsupported — a *missing* cell is a
// programming error, caught by TestDestinationMatrixIsComplete, not silently
// degraded to Unsupported at apply time (D137).
type Destination struct {
	// dir are the path segments, relative to the client base dir.
	Dir []string
	// named marks a per-artifact destination: the artifact name is appended
	// to dir, with suffix (e.g. ".md") if any. When false, dir is the whole
	// path — one shared file for every artifact of that kind.
	Named  bool
	suffix string
	// unsupported marks a combination this provider cannot represent.
	Unsupported bool
}

func At(segments ...string) Destination { return Destination{Dir: segments} }

func PerName(suffix string, segments ...string) Destination {
	return Destination{Dir: segments, Named: true, suffix: suffix}
}

var unsupportedDest = Destination{Unsupported: true}

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
//   - pi fills mcp, instructions and skill; it has no subagents and its hook
//     cell waits for the generated extension. Everything lives under
//     $HOME/.pi/agent: PI_CODING_AGENT_DIR is not honoured (D676, D755).
var DestinationMatrix = map[string]map[configurator.Provider]Destination{
	"mcp": {
		configurator.ProviderClaudeCode: At(".claude.json"),
		configurator.ProviderCodex:      At(".codex", "config.toml"),
		// OpenCode's global config, not "$HOME/opencode.json" (read only as a
		// project file, #1172): an entry locked there relocates on sync.
		configurator.ProviderOpenCode:    At(".config", "opencode", "opencode.json"),
		configurator.ProviderKiro:        At(".kiro", "settings", "mcp.json"),
		configurator.ProviderAntigravity: At(".gemini", "config", "mcp_config.json"),
		configurator.ProviderCrush:       At(".config", "crush", "crush.json"),
		// copilot: probed on 1.0.94, D676.
		configurator.ProviderCopilot: At(".copilot", "mcp-config.json"),
		// pi: global <agent-dir>/mcp.json, bridge connected with direct
		// exposure (probe 1 on Pi 1.5.0, docs/harnesses.md).
		configurator.ProviderPi: At(".pi", "agent", "mcp.json"),
		// cline: type "stdio"|"streamableHttp"|"sse" under mcpServers; the file
		// is Cline's own and hot-reloaded (bundle 4.1.26, docs/harnesses.md).
		configurator.ProviderCline: At(".cline", "data", "settings", "cline_mcp_settings.json"),
	},
	"instructions": {
		configurator.ProviderClaudeCode:  At(".claude", "CLAUDE.md"),
		configurator.ProviderOpenCode:    At(".config", "opencode", "AGENTS.md"),
		configurator.ProviderCodex:       At(".codex", "AGENTS.md"),
		configurator.ProviderKiro:        At(".kiro", "steering", "cartographer.md"),
		configurator.ProviderAntigravity: At(".gemini", "GEMINI.md"),
		// The Crush-specific global context file, chosen over the cross-tool
		// ~/.config/AGENTS.md it also loads for the same reason the codex and
		// opencode cells point at their own file: the shared one belongs to
		// whatever else the user runs.
		// https://github.com/charmbracelet/crush#global-context-files
		configurator.ProviderCrush: At(".config", "crush", "CRUSH.md"),
		// copilot: `copilot instruction list` reports it as "Personal
		// instructions" (probed on 1.0.94, D676).
		configurator.ProviderCopilot: At(".copilot", "copilot-instructions.md"),
		// pi: reads <agent-dir>/AGENTS.md globally (probe 2 on Pi 1.5.0); the
		// cross-tool CLAUDE.md fallback belongs to the user.
		configurator.ProviderPi: At(".pi", "agent", "AGENTS.md"),
		// cline: a file Cartographer owns whole in the first entry of Cline's
		// global rules list. Always-on only because that loader has no
		// path-conditional frontmatter in 4.1.26 (watch item in
		// docs/harnesses.md): a plain .md there is a global rule. The user's own
		// rules stay in their own files.
		configurator.ProviderCline: At(".cline", "rules", "cartographer.md"),
	},
	"agent": {
		configurator.ProviderClaudeCode: PerName(".md", ".claude", "agents"),
		// The global config directory OpenCode reports (`opencode debug paths`,
		// 2.0.25) and the documented 1.x global path, so one cell serves both
		// majors (D360). The former `.opencode/agent` (D192) is not read by
		// 2.x; TestOpenCodeDeclaresItsConfigDirectory fails if it drifts again.
		configurator.ProviderOpenCode: PerName(".md", ".config", "opencode", "agents"),
		configurator.ProviderCodex:    PerName(".toml", ".codex", "agents"),
		// kiro: a JSON agent config in the global agent directory (D195).
		// D140 read this cell as unsupported because Kiro's agents were
		// top-level personas the user selects, not delegates a main agent can
		// invoke. That is no longer true: the shipped default agent documents a
		// subagent system and a `use_subagent` tool that selects by
		// description, and `kiro-cli agent list` reports a config dropped here
		// as "Global". Verified against Kiro CLI 2.21.3 — the format is JSON,
		// not the Markdown the vendor documentation describes.
		configurator.ProviderKiro:        PerName(".json", ".kiro", "agents"),
		configurator.ProviderAntigravity: PerName(".md", ".gemini", "config", "agents"),
		// crush: neither its README nor its docs/config page describes a
		// user-level subagent directory, and a destination is declared, never
		// invented.
		configurator.ProviderCrush: unsupportedDest,
		// copilot: `~/.copilot/agents/<name>.agent.md`, frontmatter name and
		// description; a session reported seeing the agent (probed on 1.0.94, D676).
		configurator.ProviderCopilot: PerName(".agent.md", ".copilot", "agents"),
		// pi: has no subagents (vendor docs), so there is nothing to project.
		configurator.ProviderPi: unsupportedDest,
		// cline: ~/.cline/agents/<name>.yaml, a YAML frontmatter block plus a
		// non-empty Markdown body as the system prompt; only .yml/.yaml are
		// loaded (bundle 4.1.26, translateAgentForCline).
		configurator.ProviderCline: PerName(".yaml", ".cline", "agents"),
	},
	"hook": {
		configurator.ProviderClaudeCode: PerName("", ".claude", "hooks"),
		configurator.ProviderCodex:      PerName("", ".codex", "hooks"),
		// Hook files (script + hook.json), same as for the other providers —
		// the JS plugin that invokes them (D59) is generated elsewhere (Apply,
		// registerOpenCodePlugin) in .config/opencode/plugins/, not here.
		configurator.ProviderOpenCode: PerName("", ".opencode", "hooks"),
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
		configurator.ProviderKiro:        PerName("", ".kiro", "hooks", "cartographer"),
		configurator.ProviderAntigravity: PerName("", ".gemini", "config", "hooks"),
		// crush: hooks fire from `hooks` in crush.json, PreToolUse only (probed
		// on v0.98.0, D363), registered by registerCrushHook. No session event,
		// so its sync trigger stays the scheduled timer.
		configurator.ProviderCrush: PerName("", ".config", "crush", "hooks"),
		// copilot: Copilot loads ~/.copilot/hooks/ RECURSIVELY and parses every
		// *.json below it (probed on 1.0.94: a foreign JSON in a subdirectory
		// logs "Invalid hook configuration"), so the KB hook's own hook.json
		// must not live there. The files go to ~/.copilot/cartographer-hooks/<n>/
		// and only the registration, ~/.copilot/hooks/cartographer-<n>.json,
		// sits in hooks/ (registerCopilotHook, D676).
		configurator.ProviderCopilot: PerName("", ".copilot", "cartographer-hooks"),
		// pi: Pi has no hook file; each hook is a generated extension
		// ~/.pi/agent/extensions/cartographer-<n>.ts (registerPiHook). Pi also
		// loads extensions/*/index.ts and a KB hook directory may carry an
		// index.ts, so the hook's own files must not live under extensions/:
		// they go to ~/.pi/agent/cartographer-hooks/<n>/.
		configurator.ProviderPi: PerName("", ".pi", "agent", "cartographer-hooks"),
		// cline: the hook's own files are Cartographer's, here; the only thing
		// in Documents/Cline/Hooks, the one global directory the VS Code
		// extension reads, is a dispatcher per event (registerClineHook).
		configurator.ProviderCline: PerName("", ".cline", "cartographer-hooks"),
	},
	"skill": {
		configurator.ProviderClaudeCode: PerName("", ".claude", "skills"),
		// Divergence from the documented path, working and deliberate (D192):
		// Codex's public documentation names `$HOME/.agents/skills` for the user
		// scope (https://developers.openai.com/codex/skills), while
		// `codex debug prompt-input` confirms client 0.153.4 catalogues them
		// here. The right target is the *repository* path, which only exists
		// once a workspace scope does (D193), so moving it now would mean doing
		// the migration twice.
		configurator.ProviderCodex: PerName("", ".codex", "skills"),
		configurator.ProviderKiro:  PerName("", ".kiro", "skills"),
		// Under the config directory `opencode debug paths` reports; 2.0.25 does
		// not list skills under `.opencode/skills` globally (D360).
		configurator.ProviderOpenCode:    PerName("", ".config", "opencode", "skills"),
		configurator.ProviderAntigravity: PerName("", ".gemini", "config", "skills"),
		// crush implements the Agent Skills standard and scans this directory
		// globally, among others it shares with Claude Code and the .agents
		// convention; its own is the one Cartographer owns.
		// https://github.com/charmbracelet/crush#agent-skills
		configurator.ProviderCrush: PerName("", ".config", "crush", "skills"),
		// copilot: "Personal skills" (probed on 1.0.94, D676).
		configurator.ProviderCopilot: PerName("", ".copilot", "skills"),
		// pi: <agent-dir>/skills/, loaded without project trust (probe 3 on
		// Pi 1.5.0).
		configurator.ProviderPi: PerName("", ".pi", "agent", "skills"),
		// cline: the global skills directory under CLINE_DIR's default root
		// (.cline); $HOME/.claude/skills is read only at project level.
		configurator.ProviderCline: PerName("", ".cline", "skills"),
	},
}

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

// DestDir returns the destination path for an artifact, relative to the base-dir.
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
// reads the same matrix as DestDir, so what the Atlas UI shows as an
// artifact's clients (D238) cannot drift from what sync writes.
func Destinations(kind, name string) []configurator.Provider {
	var out []configurator.Provider
	for _, d := range configurator.Providers() {
		if DestDir(kind, name, d.Provider) != "" {
			out = append(out, d.Provider)
		}
	}
	return out
}

func DestDir(kind, name string, provider configurator.Provider) string {
	cell, ok := CellFor(ScopeGlobal, kind, provider)
	if !ok || cell.Unsupported {
		return ""
	}
	// Slash, like DestDirScoped's: a destination is written into provider
	// configuration and into the lockfile, both read on every platform.
	if !cell.Named {
		return path.Join(cell.Dir...)
	}
	segments := append(append([]string{}, cell.Dir...), name+cell.suffix)
	return path.Join(segments...)
}

// YamlQuoteScalar quotes a scalar value with double quotes (escaping backslashes,
// double quotes and newlines) if it contains characters that would otherwise break
// simple "key: value" YAML parsing (colons, newlines, leading/trailing
// whitespace, YAML indicator characters); returns it unquoted otherwise. Used only
// to serialize the OpenCode `description` field in translateAgentForProvider —
// values can come from an arbitrary (possibly multiline/quoted) Claude frontmatter.
func YamlQuoteScalar(s string) string {
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

// ReadArtifactFiles reads all of an artifact's files from BundleFS (source "bundle",
// skill only: the bundle is skill-only) or from a KB in kbRoots (source "kb:<name>"),
// returning paths relative to the artifact's folder (or, for agents, just the
// filename) and content in memory. Used by copyArtifactFiles/singleArtifactContent
// for local deployment (same filesystem) and by the sync_pull MCP tool to
// serialize artifacts to a remote HTTP client with no shared filesystem.
func ReadArtifactFiles(a Artifact, bundleFS fs.FS, kbRoots map[string]string) ([]ArtifactFile, error) {
	files, err := ReadArtifactFilesCapped(a, bundleFS, kbRoots)
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		if f.Oversized > 0 {
			return nil, fmt.Errorf("provisioning: %s %s: file %s is %d bytes, over the %d bytes cap of an artifact file", a.Kind, a.Name, f.Path, f.Oversized, ArtifactMaxFileSize)
		}
	}
	return files, nil
}

// ReadArtifactFilesCapped is ReadArtifactFiles for a caller that answers for
// an over-cap file itself (the server's artifact_list and sync_pull): such a
// file comes back with Oversized set and no content, never read. A symlink in a
// KB artifact is not followed and does not appear.
func ReadArtifactFilesCapped(a Artifact, bundleFS fs.FS, kbRoots map[string]string) ([]ArtifactFile, error) {
	// Content already in memory: use it directly instead of trying to read it from
	// the filesystem. Two cases: (a) kind "instructions" (D56) — the content is
	// GENERATED by BuildManifest, doesn't exist as a file on the KB, so there's
	// nothing to read for source/kind below; (b) an artifact already decoded by
	// sync_pull on the remote client side (no filesystem shared with the server).
	if len(a.Files) > 0 {
		return a.Files, nil
	}
	if IsPluginSource(a.Source) {
		// A plugin's bytes come from its approved tree, in memory, never from
		// a KB: without them, "plugin:x" must not be looked up in KBRoots.
		return nil, fmt.Errorf("provisioning: %s %s: plugin artifact without content", a.Kind, a.Name)
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

	tracked, err := KBTrackedFilter(kbRoot)
	if err != nil {
		return nil, err
	}

	// Nothing is read through a link: not the file, not a directory above it.
	var artifactRel string
	switch a.Kind {
	case "agent":
		artifactRel = "agents/" + a.Name + ".md"
	case "mcp":
		artifactRel = "mcp/" + a.Name + ".json"
	case "hook":
		artifactRel = "hooks/" + a.Name
	default:
		artifactRel = "skills/" + a.Name
	}
	link, linkErr := FirstKBLink(kbRoot, artifactRel)
	if linkErr != nil {
		return nil, fmt.Errorf("provisioning: %s %s: %w", a.Kind, a.Name, linkErr)
	}
	if link != "" {
		return nil, fmt.Errorf("provisioning: %s %s: %w", a.Kind, a.Name, &KBSymlinkError{Rel: link})
	}

	switch a.Kind {
	case "agent":
		if !tracked.Has("agents/" + a.Name + ".md") {
			return nil, fmt.Errorf("provisioning: agent %s is not tracked by git in KB %q", a.Name, kbName)
		}
		agentPath := filepath.Join(kbRoot, "agents", a.Name+".md")
		data, size, err := ReadFileCapped(agentPath)
		if err != nil {
			return nil, fmt.Errorf("provisioning: read agent %s: %w", agentPath, err)
		}
		return []ArtifactFile{{Path: AgentHashPath(a.Name), Content: data, Oversized: size}}, nil
	case "mcp":
		if !tracked.Has("mcp/" + a.Name + ".json") {
			return nil, fmt.Errorf("provisioning: mcp %s is not tracked by git in KB %q", a.Name, kbName)
		}
		mcpPath := filepath.Join(kbRoot, "mcp", a.Name+".json")
		data, size, err := ReadFileCapped(mcpPath)
		if err != nil {
			return nil, fmt.Errorf("provisioning: read mcp %s: %w", mcpPath, err)
		}
		return []ArtifactFile{{Path: a.Name + ".json", Content: data, Oversized: size}}, nil
	case "hook":
		files, err := ReadDirFiles(filepath.Join(kbRoot, "hooks", a.Name), tracked.Under("hooks/"+a.Name))
		if err != nil {
			return nil, err
		}
		for i := range files {
			files[i].Executable = EffectiveExecutable("hook", files[i].Path, files[i].Executable)
		}
		return files, nil
	default: // "skill"
		return ReadDirFiles(filepath.Join(kbRoot, "skills", a.Name), tracked.Under("skills/"+a.Name))
	}
}

// ReadDirFiles walks srcDir and returns every file inside it as an
// ArtifactFile, with Path relative to srcDir (slash-separated). Factored out of
// ReadArtifactFiles because skill and hook share the same
// "directory with one or more files" schema.
func ReadDirFiles(srcDir string, include func(rel string) bool) ([]ArtifactFile, error) {
	var files []ArtifactFile
	var total int64
	count := 0
	err := filepath.WalkDir(srcDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			// A link is never read. One the include filter leaves out is not part
			// of the artifact, so it is no reason to refuse it (contract/kblinks.go).
			rel, relErr := filepath.Rel(srcDir, path)
			if relErr != nil {
				return relErr
			}
			if path == srcDir || include == nil || include(filepath.ToSlash(rel)) {
				return &KBSymlinkError{Rel: filepath.ToSlash(rel)}
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(srcDir, path)
		if relErr != nil {
			return relErr
		}
		if include != nil && !include(filepath.ToSlash(rel)) {
			return nil
		}
		// d.Info is an Lstat: a symlink is not followed, and a file over the
		// cap is reported, never read.
		fileInfo, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		if !fileInfo.Mode().IsRegular() {
			return nil
		}
		exec := execbit.IsExecutable(fileInfo.Mode())
		relSlash := filepath.ToSlash(rel)
		if fileInfo.Size() > ArtifactMaxFileSize {
			files = append(files, ArtifactFile{Path: relSlash, Executable: exec, Oversized: fileInfo.Size()})
			return nil
		}
		// A running budget on the whole directory: past it nothing more is
		// read and the file that crossed it is reported oversized, which
		// excludes the artifact like one big file does.
		count++
		if count > ArtifactMaxFiles || total+fileInfo.Size() > ArtifactMaxTotalSize {
			files = append(files, ArtifactFile{Path: relSlash, Executable: exec, Oversized: max(fileInfo.Size(), 1)})
			return fs.SkipAll
		}
		data, size, readErr := readOpenedCapped(path, fileInfo)
		if errors.Is(readErr, ErrNotRegular) {
			return nil // swapped for a link after the Lstat
		}
		if readErr != nil {
			return readErr
		}
		if size > 0 {
			files = append(files, ArtifactFile{Path: relSlash, Executable: exec, Oversized: size})
			return nil
		}
		total += int64(len(data))
		files = append(files, ArtifactFile{Path: relSlash, Content: data, Executable: exec})
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

// AgentWidened reports that installing this agent on provider drops a `tools`
// allow-list. Claude Code receives the source verbatim, so it enforces it, and a
// client the author gave a `providers:` entry (D291) is not widened either.
func AgentWidened(a Artifact, provider configurator.Provider) bool {
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

// validateClientsOf is the error of an agent source's `clients` key, nil when
// the key is absent, valid or the frontmatter cannot be read.
func validateClientsOf(content []byte) error {
	fmRaw, _, hasFM := okf.SplitFrontmatter(string(content))
	if !hasFM {
		return nil
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		return nil
	}
	return ValidateAgentClients(fm)
}

// AgentSkipped reports that an agent is not projected to provider: either it
// opted out of being widened (strict_tools) and provider cannot enforce its
// restriction, or its `clients` allow-list does not name provider (a plugin
// provider is matched by its base id). EVERY agent-skip site (sync, status, the
// resolver, the unplaced sentence, the hygiene check) must call this one
// predicate: a second one drifts between sync and status and shows permanent drift.
func AgentSkipped(a Artifact, provider configurator.Provider) bool {
	return AgentSkipReason(a, provider) != ""
}

// AgentSkipReason is why AgentSkipped holds: "clients" when the allow-list
// excludes provider, "strict_tools" when the agent refuses to be widened there,
// "" otherwise.
func AgentSkipReason(a Artifact, provider configurator.Provider) string {
	if a.Kind == "agent" && len(a.Clients) > 0 {
		base := string(NativeProvider(provider))
		listed := false
		for _, c := range a.Clients {
			if c == base {
				listed = true
				break
			}
		}
		if !listed {
			return "clients"
		}
	}
	if AgentWidened(a, provider) && a.Restriction.Strict {
		return "strict_tools"
	}
	return ""
}
