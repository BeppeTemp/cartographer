package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/agents"
	"github.com/BeppeTemp/cartographer/internal/artifactsig"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// pulledFileJSON/pulledArtifactJSON/pulledManifestJSON mirror the sync_pull tool's
// response shape (internal/mcpserver/tools_sync.go), decoded client-side.
type pulledFileJSON struct {
	Path       string `json:"path"`
	ContentB64 string `json:"content_b64"`
	Executable bool   `json:"executable"`
}

type pulledArtifactJSON struct {
	Kind        string                 `json:"kind"`
	Name        string                 `json:"name"`
	Source      string                 `json:"source"`
	Version     string                 `json:"version,omitempty"`
	ContentHash string                 `json:"content_hash"`
	Signed      bool                   `json:"signed"`
	BuiltIn     bool                   `json:"built_in,omitempty"`
	Signature   *artifactsig.Signature `json:"signature,omitempty"`
	Files       []pulledFileJSON       `json:"files"`
}

type pulledManifestJSON struct {
	Revision  string               `json:"revision"`
	Artifacts []pulledArtifactJSON `json:"artifacts"`
}

// lockFilePath returns the path to the v2 multi-provider lockfile inside targetDir.
func lockFilePath(targetDir string) string {
	return filepath.Join(targetDir, provisioning.LockFileName)
}

// resolveToken returns the bearer token for cfg, read from cfg.TokenEnv when
// cfg.Auth is true; empty otherwise (no Authorization header is sent).
func resolveToken(cfg *clientconfig.Config) string {
	if !cfg.Auth || cfg.TokenEnv == "" {
		return ""
	}
	return os.Getenv(cfg.TokenEnv)
}

// fetchMergedManifest connects to cfg.ServerURL, discovers the live per-KB
// tool-name prefixes from /health (D120: resolveKBTargets), and calls
// sync_pull — qualified with each target's advertised prefix — once per KB
// target (cfg.KBs, or the server's default single-KB endpoint when empty),
// decoding each artifact's in-memory file contents (base64) and merging
// everything into a single provisioning.Manifest via
// provisioning.MergeArtifacts — the same precedence rule (KB source wins over
// bundle) BuildManifest applies server-side for one KB.
func fetchMergedManifest(cfg *clientconfig.Config) (provisioning.Manifest, error) {
	token := resolveToken(cfg)
	health, err := client.New(cfg.ServerURL, token).Health(probeTimeout)
	if err != nil {
		return provisioning.Manifest{}, fmt.Errorf("health: %w", err)
	}
	targets, err := resolveKBTargets(health, cfg.KBs)
	if err != nil {
		return provisioning.Manifest{}, err
	}

	var all []provisioning.Artifact
	seen := make(map[string]provisioning.Artifact)

	for _, target := range targets {
		c := client.New(cfg.ServerURL, token).WithKB(target.Name)
		raw, err := callTool(c, target, "sync_pull", map[string]any{})
		if err != nil {
			if target.Name == "" {
				return provisioning.Manifest{}, fmt.Errorf("sync_pull: %w", err)
			}
			return provisioning.Manifest{}, fmt.Errorf("sync_pull (kb=%s): %w", target.Name, err)
		}

		var pm pulledManifestJSON
		if err := json.Unmarshal(raw, &pm); err != nil {
			return provisioning.Manifest{}, fmt.Errorf("sync_pull: decode response: %w", err)
		}
		for _, pa := range pm.Artifacts {
			files := make([]provisioning.ArtifactFile, len(pa.Files))
			for i, pf := range pa.Files {
				data, err := base64.StdEncoding.DecodeString(pf.ContentB64)
				if err != nil {
					return provisioning.Manifest{}, fmt.Errorf("sync_pull: decode file %s/%s/%s: %w", pa.Kind, pa.Name, pf.Path, err)
				}
				files[i] = provisioning.ArtifactFile{Path: pf.Path, Content: data, Executable: pf.Executable}
			}
			if got := provisioning.ContentHashFiles(files); got != pa.ContentHash {
				return provisioning.Manifest{}, fmt.Errorf("sync_pull: content hash mismatch for %s/%s", pa.Kind, pa.Name)
			}
			a := provisioning.Artifact{
				Kind: pa.Kind, Name: pa.Name, Source: pa.Source, Version: pa.Version,
				ContentHash: pa.ContentHash, BuiltIn: pa.BuiltIn, Signature: pa.Signature, Files: files,
			}
			key := a.Kind + "\x00" + a.Name + "\x00" + a.Source
			if previous, exists := seen[key]; exists && !sameSignature(previous.Signature, a.Signature) {
				return provisioning.Manifest{}, fmt.Errorf("sync_pull: conflicting signatures for %s/%s", a.Kind, a.Name)
			}
			seen[key] = a
			all = append(all, a)
		}
	}

	pins, err := pinnedPublicKeys(cfg)
	if err != nil {
		return provisioning.Manifest{}, err
	}
	return provisioning.VerifiedManifest(provisioning.MergeArtifacts(all), pins)
}

func sameSignature(a, b *artifactsig.Signature) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Algorithm == b.Algorithm && a.KeyID == b.KeyID && a.EnvelopeVersion == b.EnvelopeVersion && a.Value == b.Value
}

func pinnedPublicKeys(cfg *clientconfig.Config) (map[string][]ed25519.PublicKey, error) {
	pins := make(map[string][]ed25519.PublicKey)
	for kbName, encoded := range cfg.SigningKeys {
		for _, value := range encoded {
			key, err := artifactsig.ParsePublicKey(value)
			if err != nil {
				return nil, fmt.Errorf("invalid signing key pin for KB %q: %w", kbName, err)
			}
			pins[kbName] = append(pins[kbName], key)
		}
	}
	return pins, nil
}

// materializeForProviders applies manifest m for each provider in providers,
// persisting a single v2 LockFile at <targetDir>/.cartographer-sync.lock.json (one
// Lock entry per provider), stamped with serverVersion — the version of the
// server this state was materialized against (D142). An empty serverVersion
// means the server could not be asked, and leaves the recorded value
// unchanged: an offline sync must not erase what the client knew. The lockfile
// always lives in targetDir, but each
// provider materializes under its OWN base dir (D141: every provider but
// hermes shares targetDir; hermes writes under $HERMES_HOME), recorded in that
// provider's Lock so prune and verification later find the same files. autoTrust is explicit authorization for eligible
// unsigned KB artifacts, passed to ApplyOptions.AutoTrust; it never changes an
// artifact's Signed verification result. searchRoots/paths come from the loaded
// clientconfig.Config (cfg.SearchRoots/cfg.Paths) and drive placeholder expansion
// (D75 WP3) — this is the one place cmd/cartographer turns
// ApplyOptions.ExpandPlaceholders on; internal/mcpserver never does.
func materializeForProviders(m provisioning.Manifest, providers []string, targetDir, serverVersion string, autoTrust, dryRun, noHeal bool, searchRoots []string, paths map[string]string, approvalHashes ...map[string]string) (map[string]provisioning.AppliedResult, error) {
	lockPath := lockFilePath(targetDir)
	lockFile, err := provisioning.ReadLockFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("read lockfile: %w", err)
	}

	results := make(map[string]provisioning.AppliedResult, len(providers))
	var approvedMCP map[string]string
	if len(approvalHashes) > 0 {
		approvedMCP = approvalHashes[0]
	}
	// Validate every authorized local command for every destination before any
	// provider file or lockfile can change. Provider is carried into errors so
	// a failed multi-provider sync identifies the configuration it protected.
	baseDirs := make(map[string]string, len(providers))
	for _, p := range providers {
		baseDir, err := provisioning.BaseDirFor(configurator.Provider(p), targetDir)
		if err != nil {
			return nil, err
		}
		baseDirs[p] = baseDir
		if err := provisioning.PreflightStdioMCP(m, provisioning.ApplyOptions{Provider: configurator.Provider(p), BaseDir: baseDir, AutoTrust: autoTrust, ApprovedMCP: approvedMCP}); err != nil {
			return nil, err
		}
	}
	for _, p := range providers {
		previous := lockFile.ForProvider(p)
		opts := provisioning.ApplyOptions{
			Provider:           configurator.Provider(p),
			BaseDir:            baseDirs[p],
			DryRun:             dryRun,
			NoHeal:             noHeal,
			AutoTrust:          autoTrust,
			ApprovedMCP:        approvedMCP,
			Lock:               previous,
			SkipLockWrite:      true,
			ExpandPlaceholders: true,
			SearchRoots:        searchRoots,
			Paths:              paths,
		}
		// Apply only the artifacts the provider knows how to materialize:
		// unsupported kinds (e.g. hook outside Claude Code, or agent outside
		// Claude Code/OpenCode — D55) are neither drift nor pending, they
		// simply don't concern it.
		applied, err := provisioning.Apply(provisioning.FilterForProvider(m, configurator.Provider(p)), opts)
		if err != nil {
			return nil, fmt.Errorf("apply %s: %w", p, err)
		}
		// Record the base dir only when it is not the lockfile's own
		// directory: every existing lockfile keeps its current meaning and no
		// migration runs (D141).
		if baseDirs[p] != targetDir {
			applied.NewLock.BaseDir = baseDirs[p]
		}
		// An unknown live version preserves the recorded one rather than
		// blanking it (D142).
		applied.NewLock.ServerVersion = previous.ServerVersion
		if serverVersion != "" {
			applied.NewLock.ServerVersion = serverVersion
		}
		lockFile.SetProvider(p, applied.NewLock)
		results[p] = applied
	}

	if !dryRun {
		if err := provisioning.WriteLockFile(lockPath, lockFile); err != nil {
			return nil, fmt.Errorf("write lockfile: %w", err)
		}
	}
	return results, nil
}

// ensureBootstrapForProviders ensures the cartographer-bootstrap hook (D60,
// provisioning.EnsureBootstrapHook) is materialized and registered for every
// provider in providers, merging its ManagedFile entries into the v2 lockfile and
// persisting it (unless dryRun). Called by both `connect` and `sync`, independent
// of whether the server manifest could be fetched — the bootstrap hook is purely
// local, and it's exactly what lets a session self-heal via `cartographer sync`
// once the server becomes reachable, so it must be ensured even when connect's own
// manifest fetch is deferred (server down at connect time).
func ensureBootstrapForProviders(providers []string, targetDir string, dryRun bool) error {
	lockPath := lockFilePath(targetDir)
	lockFile, err := provisioning.ReadLockFile(lockPath)
	if err != nil {
		return fmt.Errorf("read lockfile: %w", err)
	}

	for _, p := range providers {
		lock := lockFile.ForProvider(p)
		newLock, err := provisioning.EnsureBootstrapHook(targetDir, configurator.Provider(p), lock, dryRun)
		if err != nil {
			return fmt.Errorf("ensure bootstrap hook (%s): %w", p, err)
		}
		lockFile.SetProvider(p, newLock)
	}

	if !dryRun {
		if err := provisioning.WriteLockFile(lockPath, lockFile); err != nil {
			return fmt.Errorf("write lockfile: %w", err)
		}
	}
	return nil
}

// printApplySummary prints a one-line-per-file summary of a materialization pass.
// dir is the base-dir the artifacts were materialized into — used to print the
// resolved settings.json path in printHookRegistered (D57).
//
// dryRun is not cosmetic: Apply fills AppliedResult with what it *would* write
// when it changes nothing, so reporting those lines in the past tense describes
// a pass that never happened — precisely when the reader asked to see the plan
// before committing to it (D147).
func printApplySummary(dir string, results map[string]provisioning.AppliedResult, dryRun bool) {
	needsApproval := false
	needsMCPApproval := false
	// "wrote" / "would write", chosen once so every line of the pass agrees.
	verb := func(done, planned string) string {
		if dryRun {
			return planned
		}
		return done
	}
	for _, p := range sortedKeys(results) {
		r := results[p]
		// One line per file written, not per artifact: the instructions block is
		// one physical write shared by every mounted KB, so reporting it once per
		// KB printed three identical lines for one file, which reads like a loop
		// (D154). Only the instructions kind is collapsed — elsewhere two
		// artifacts never share a path, and a blanket dedup would hide a real bug.
		reportedInstructions := map[string]bool{}
		for _, w := range r.Written {
			if w.Kind == "instructions" {
				if reportedInstructions[w.Path] {
					continue
				}
				reportedInstructions[w.Path] = true
			}
			fmt.Printf("[%s] %s %s\n", p, verb("wrote", "would write"), w.Path)
			// The registration line reports a side effect on a shared file
			// that a dry run does not perform either.
			if !dryRun && hookRegistrationManagedFile(p, w) {
				printHookRegistered(p, dir, w)
			}
		}
		for _, pr := range r.Pruned {
			fmt.Printf("[%s] %s %s\n", p, verb("pruned", "would prune"), pr.Path)
		}
		// A heal means a local change was discarded (D139): say so on its own
		// line instead of folding it into the ordinary writes above.
		healedArtifacts := map[string]bool{}
		for _, h := range r.Healed {
			key := h.Kind + "/" + h.Name
			if healedArtifacts[key] {
				continue
			}
			healedArtifacts[key] = true
			fmt.Printf("[%s] %s %s from the server (local changes discarded)\n", p, verb("restored", "would restore"), key)
		}
		for _, d := range r.Divergent {
			fmt.Printf("[%s] diverged locally (%s): %s/%s at %s — `cartographer sync` restores it\n", p, d.Reason, d.Kind, d.Name, d.Path)
		}
		for _, na := range r.NeedsApproval {
			if na.Kind == "mcp" {
				needsMCPApproval = true
			} else {
				needsApproval = true
			}
			fmt.Printf("[%s] needs_approval: %s/%s [%s]\n", p, na.Kind, na.Name, na.Source)
		}
		for _, ua := range r.Unsupported {
			fmt.Printf("[%s] unsupported: %s/%s [%s] (kind has no destination for this provider)\n", p, ua.Kind, ua.Name, ua.Source)
		}
		// A refusal is not one warning among others: the artifact is not
		// installed, and only the operator can clear the condition (D148).
		for _, rf := range r.Refused {
			fmt.Printf("[%s] %s: %s/%s [%s] — its destination is a symlink, so the write would land outside the client\n",
				p, verb("refused", "would refuse"), rf.Kind, rf.Name, rf.Source)
		}
		for _, w := range r.Warnings {
			fmt.Printf("[%s] warning: %s\n", p, w)
		}
	}
	if needsApproval {
		fmt.Printf("to approve the unsigned artifacts run: %s\n", autoTrustCommand())
	}
	if needsMCPApproval {
		fmt.Println("MCP artifacts require a point approval: cartographer approve mcp <name> --kb <kb>, then cartographer sync")
	}
}

// autoTrustCommand returns the exact command line the user must run to approve
// unsigned KB-sourced artifacts, so every needs-approval message can print it
// verbatim instead of a vague "use --auto-trust" hint.
func autoTrustCommand() string {
	return "cartographer sync --auto-trust"
}

// hookRegistrationManagedFile reports whether w is the ManagedFile whose
// presence in AppliedResult.Written signals "this Apply (re)ran the hook's
// provider-native registration step" — the trigger differs by provider because
// claude/codex patch an existing shared file (settings.json/config.toml) as a
// side effect of materializing hook.json, so hook.json itself is the (always
// present) trigger for them; opencode instead generates its own dedicated
// registration artifact (the plugin wrapper, D59) as a separate ManagedFile,
// which is the trigger there — and is absent when the hook's event has no
// OpenCode equivalent (see registerOpenCodePlugin), correctly suppressing the
// message in that case.
func hookRegistrationManagedFile(provider string, w provisioning.ManagedFile) bool {
	if w.Kind != "hook" {
		return false
	}
	p := configurator.Provider(provider)
	if plugin := provisioning.HookPluginRelPath(p, w.Name); plugin != "" {
		return filepath.Base(w.Path) == filepath.Base(plugin)
	}
	if provisioning.HookRegistrationFile(p) != "" {
		return filepath.Base(w.Path) == "hook.json"
	}
	return false
}

// printHookRegistered prints the one-line confirmation that a materialized
// hook was also registered in the provider's own file: settings.json (D57),
// config.toml (D58), or its own generated plugin file (D59 — the plugin *is*
// the registration, so its own path is printed instead of a separate shared
// file).
func printHookRegistered(provider, dir string, w provisioning.ManagedFile) {
	p := configurator.Provider(provider)
	var registeredIn string
	switch {
	case provisioning.HookPluginRelPath(p, w.Name) != "":
		// The generated plugin *is* the registration (D59): print its own path.
		registeredIn = filepath.Join(dir, w.Path)
	case provisioning.HookRegistrationFile(p) != "":
		registeredIn = filepath.Join(dir, provisioning.HookRegistrationFile(p))
	default:
		return
	}
	fmt.Printf("[%s] hook %q registered in %s\n", provider, w.Name, registeredIn)
}

// sortedKeys returns the keys of a map[string]provisioning.AppliedResult sorted,
// so command output is deterministic across runs.
func sortedKeys(m map[string]provisioning.AppliedResult) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return keys
}

// resolveTargetProviders resolves connect's positional target and --agents CSV:
//   - no target or "all" → every detected agent;
//   - one explicit positional provider → that provider, regardless of detection;
//   - --agents → the selected validated provider subset.
//
// A positional target and --agents are deliberately mutually exclusive so a
// command cannot quietly ignore one of two conflicting selections.
func resolveTargetProviders(target, csv string) ([]string, error) {
	if csv != "" {
		if target != "" {
			return nil, fmt.Errorf("--agents cannot be used with positional provider %q", target)
		}
		return resolveProviderCSV(csv)
	}
	if target == "" || target == "all" {
		var out []string
		for _, a := range agents.Detect() {
			if a.Installed {
				out = append(out, string(a.Provider))
			}
		}
		return out, nil
	}
	return resolveProvider(target)
}

// resolveProviderCSV validates a non-empty comma-separated provider list while
// preserving its order. Repeating a provider is harmless but redundant, so it
// is represented only once in the resulting operation.
func resolveProviderCSV(csv string) ([]string, error) {
	var providers []string
	seen := make(map[string]bool)
	for _, part := range strings.Split(csv, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			return nil, fmt.Errorf("invalid --agents value %q (want comma-separated %s)", csv, providerNamesJoined())
		}
		provider, err := resolveProvider(name)
		if err != nil {
			return nil, err
		}
		if !seen[provider[0]] {
			providers = append(providers, provider[0])
			seen[provider[0]] = true
		}
	}
	return providers, nil
}

func resolveProvider(target string) ([]string, error) {
	if _, ok := configurator.Lookup(configurator.Provider(target)); ok {
		return []string{target}, nil
	}
	return nil, fmt.Errorf("unknown provider %q (want %s)", target, providerNamesJoined())
}

// providerNamesJoined renders the supported provider identifiers for a usage
// message, from the registry rather than a hand-kept list (D137/D141: adding a
// provider must not leave stale help text behind).
func providerNamesJoined() string {
	names := make([]string, 0, len(configurator.ProviderList()))
	for _, p := range configurator.ProviderList() {
		names = append(names, string(p))
	}
	return strings.Join(names, "|")
}

// splitPositional extracts a single leading positional argument (one not starting
// with "-") from args, returning it (or def if none) and the remaining arguments to
// hand to flag.FlagSet.Parse. flag.Parse stops at the first non-flag token, so a
// positional target given before the flags (as in `connect claude --server-url …`)
// must be pulled out first.
func splitPositional(args []string, def string) (string, []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}
	return def, args
}

// versionIsComparable rejects the two cases where a version difference means
// nothing: an unknown version (a lockfile from before D142, or a sync that
// could not reach /health) and a local "dev" build, which changes with every
// `go build`. Same rule the advisory client/server skew line already applies
// in `status`.
func versionIsComparable(v string) bool { return v != "" && v != "dev" }

// serverChangeNotice returns the one line `sync` prints when the server that
// answers now is not the one this client's provider state was materialized
// against (D142) — or "" when there is nothing to say. It reports; it never
// escalates: the ordinary sync runs either way, and rebuilding the
// configuration stays the user's explicit `cartographer reconnect`.
//
// Once per invocation, not once per provider: several providers recording
// different versions is one fact about the server, not three.
func serverChangeNotice(dir string, providers []string, liveVersion string) string {
	if !versionIsComparable(liveVersion) {
		return ""
	}
	lockFile, err := provisioning.ReadLockFile(lockFilePath(dir))
	if err != nil {
		return ""
	}
	var recorded []string
	seen := map[string]bool{}
	for _, p := range providers {
		v := lockFile.ForProvider(p).ServerVersion
		if !versionIsComparable(v) || v == liveVersion || seen[v] {
			continue
		}
		seen[v] = true
		recorded = append(recorded, v)
	}
	if len(recorded) == 0 {
		return ""
	}
	sort.Strings(recorded)
	return fmt.Sprintf("the server changed since this client was configured (was %s, now %s) — run `cartographer reconnect` to rebuild the client configuration",
		strings.Join(recorded, ", "), liveVersion)
}
