package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/agents"
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
}

type pulledArtifactJSON struct {
	Kind        string           `json:"kind"`
	Name        string           `json:"name"`
	Source      string           `json:"source"`
	Version     string           `json:"version,omitempty"`
	ContentHash string           `json:"content_hash"`
	Signed      bool             `json:"signed"`
	Files       []pulledFileJSON `json:"files"`
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

// kbTargets returns the list of KB names to query for cfg: cfg.KBs verbatim, or a
// single "" entry (the server's default single-KB endpoint, see
// MultiKBServer.Handler in internal/mcpserver/httpserver.go) when cfg.KBs is empty.
func kbTargets(cfg *clientconfig.Config) []string {
	if len(cfg.KBs) == 0 {
		return []string{""}
	}
	return cfg.KBs
}

// fetchMergedManifest connects to cfg.ServerURL and calls sync_pull once per KB
// target (cfg.KBs, or the default single-KB endpoint when empty), decoding each
// artifact's in-memory file contents (base64) and merging everything into a single
// provisioning.Manifest via provisioning.MergeArtifacts — the same precedence rule
// (KB source wins over bundle) BuildManifest applies server-side for one KB.
func fetchMergedManifest(cfg *clientconfig.Config) (provisioning.Manifest, error) {
	token := resolveToken(cfg)
	var all []provisioning.Artifact

	for _, kbName := range kbTargets(cfg) {
		c := client.New(cfg.ServerURL, token).WithKB(kbName)
		raw, err := c.Call("sync_pull", map[string]any{})
		if err != nil {
			if kbName == "" {
				return provisioning.Manifest{}, fmt.Errorf("sync_pull: %w", err)
			}
			return provisioning.Manifest{}, fmt.Errorf("sync_pull (kb=%s): %w", kbName, err)
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
				files[i] = provisioning.ArtifactFile{Path: pf.Path, Content: data}
			}
			all = append(all, provisioning.Artifact{
				Kind: pa.Kind, Name: pa.Name, Source: pa.Source, Version: pa.Version,
				ContentHash: pa.ContentHash, Signed: pa.Signed, Files: files,
			})
		}
	}

	return provisioning.MergeArtifacts(all), nil
}

// upgradeTrustedManifest returns a copy of m with every kb:-sourced artifact
// upgraded to Signed:true when trust is true; m unchanged (same slice) when
// trust is false. This is the single place the trust decision (persistent
// cfg.Trust from .cartographer.yaml, or a one-time --auto-trust flag) is
// applied to a manifest — used both by materializeForProviders, before
// provisioning.Apply, and by callers that only need an honest diff/status
// (cmdStatus, the TUI dashboard) without materializing anything. sync_pull
// never accepts an auto_trust argument (unlike the local sync_apply MCP
// tool): the trust decision is entirely client-side (see docs/sync.md
// §Sicurezza — gate di firma).
//
// Kind "mcp" (D69, WP5) is excluded from this blanket upgrade: a third-party
// MCP server is an endpoint that receives the agent's data, a stricter policy
// than the other kinds — it always needs its own approval at first appearance
// and at every hash change, even with AutoTrust/cfg.Trust on.
func upgradeTrustedManifest(m provisioning.Manifest, trust bool) provisioning.Manifest {
	if !trust {
		return m
	}
	artifacts := make([]provisioning.Artifact, len(m.Artifacts))
	copy(artifacts, m.Artifacts)
	for i := range artifacts {
		if artifacts[i].Kind != "mcp" && strings.HasPrefix(artifacts[i].Source, "kb:") {
			artifacts[i].Signed = true
		}
	}
	return provisioning.Manifest{Revision: m.Revision, Artifacts: artifacts}
}

// materializeForProviders applies manifest m for each provider in providers,
// persisting a single v2 LockFile at <targetDir>/.cartographer-sync.lock.json (one
// Lock entry per provider). autoTrust upgrades kb:-sourced artifacts to Signed:true
// before materialization (see upgradeTrustedManifest) — callers pass
// cfg.Trust || --auto-trust so the persisted per-server decision and the
// one-time flag both apply. searchRoots/paths come from the loaded
// clientconfig.Config (cfg.SearchRoots/cfg.Paths) and drive placeholder
// expansion (D75 WP3) — this is the one place cmd/cartographer turns
// ApplyOptions.ExpandPlaceholders on; internal/mcpserver never does.
func materializeForProviders(m provisioning.Manifest, providers []string, targetDir string, autoTrust, dryRun bool, searchRoots []string, paths map[string]string) (map[string]provisioning.AppliedResult, error) {
	mm := upgradeTrustedManifest(m, autoTrust)

	lockPath := lockFilePath(targetDir)
	lockFile, err := provisioning.ReadLockFile(lockPath)
	if err != nil {
		return nil, fmt.Errorf("read lockfile: %w", err)
	}

	results := make(map[string]provisioning.AppliedResult, len(providers))
	for _, p := range providers {
		opts := provisioning.ApplyOptions{
			Provider:           configurator.Provider(p),
			BaseDir:            targetDir,
			DryRun:             dryRun,
			Lock:               lockFile.ForProvider(p),
			SkipLockWrite:      true,
			ExpandPlaceholders: true,
			SearchRoots:        searchRoots,
			Paths:              paths,
		}
		// Apply only the artifacts the provider knows how to materialize:
		// unsupported kinds (e.g. hook outside Claude Code, or agent outside
		// Claude Code/OpenCode — D55) are neither drift nor pending, they
		// simply don't concern it.
		applied, err := provisioning.Apply(provisioning.FilterForProvider(mm, configurator.Provider(p)), opts)
		if err != nil {
			return nil, fmt.Errorf("apply %s: %w", p, err)
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
func printApplySummary(dir string, results map[string]provisioning.AppliedResult) {
	needsApproval := false
	for _, p := range sortedKeys(results) {
		r := results[p]
		for _, w := range r.Written {
			fmt.Printf("[%s] wrote %s\n", p, w.Path)
			if hookRegistrationManagedFile(p, w) {
				printHookRegistered(p, dir, w)
			}
		}
		for _, pr := range r.Pruned {
			fmt.Printf("[%s] pruned %s\n", p, pr.Path)
		}
		for _, na := range r.NeedsApproval {
			needsApproval = true
			fmt.Printf("[%s] needs_approval: %s/%s [%s]\n", p, na.Kind, na.Name, na.Source)
		}
		for _, ua := range r.Unsupported {
			fmt.Printf("[%s] unsupported: %s/%s [%s] (kind has no destination for this provider)\n", p, ua.Kind, ua.Name, ua.Source)
		}
		for _, w := range r.Warnings {
			fmt.Printf("[%s] warning: %s\n", p, w)
		}
	}
	if needsApproval {
		fmt.Printf("to approve the unsigned artifacts run: %s\n", autoTrustCommand())
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
	switch configurator.Provider(provider) {
	case configurator.ProviderClaudeCode, configurator.ProviderCodex:
		return filepath.Base(w.Path) == "hook.json"
	case configurator.ProviderOpenCode:
		return filepath.Base(w.Path) == "cartographer-"+w.Name+".js"
	default:
		return false
	}
}

// printHookRegistered prints the one-line confirmation that a materialized
// hook was also registered in the provider's own file: settings.json (D57),
// config.toml (D58), or its own generated plugin file (D59 — the plugin *is*
// the registration, so its own path is printed instead of a separate shared
// file).
func printHookRegistered(provider, dir string, w provisioning.ManagedFile) {
	var registeredIn string
	switch configurator.Provider(provider) {
	case configurator.ProviderClaudeCode:
		registeredIn = filepath.Join(dir, ".claude", "settings.json")
	case configurator.ProviderCodex:
		registeredIn = filepath.Join(dir, ".codex", "config.toml")
	case configurator.ProviderOpenCode:
		registeredIn = filepath.Join(dir, w.Path)
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

// resolveTargetProviders resolves the `connect`/`sync` positional target argument:
//   - "" or "all" → every agent detected on this machine (internal/agents.Detect)
//   - an explicit provider name → that provider, regardless of detection (the
//     user's explicit choice overrides detection)
func resolveTargetProviders(target string) ([]string, error) {
	if target == "" || target == "all" {
		var out []string
		for _, a := range agents.Detect() {
			if a.Installed {
				out = append(out, string(a.Provider))
			}
		}
		return out, nil
	}
	switch configurator.Provider(target) {
	case configurator.ProviderClaudeCode, configurator.ProviderOpenCode, configurator.ProviderCodex, configurator.ProviderKiro:
		return []string{target}, nil
	default:
		return nil, fmt.Errorf("unknown provider %q (want claude|opencode|codex|kiro|all)", target)
	}
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
