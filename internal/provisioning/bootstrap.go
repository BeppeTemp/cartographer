// bootstrap.go (D60) — the "cartographer-bootstrap" hook: a synthetic artifact,
// generated entirely by the client (not by the server/KB manifest), that at
// session start silently runs `cartographer sync --auto-trust` so the LLM agent
// always starts aligned with the server, without the user having to run `sync`
// by hand. See D60, docs/sync.md §Layer 1.
//
// EnsureBootstrapHook reuses exactly the D57 (Claude settings.json), D58 (Codex
// config.toml) and D59 (OpenCode plugin) registration primitives used by Apply
// for KB hooks — same hook.json schema + script in a dedicated directory, same
// idempotence. The only difference is that here the directory is not populated
// by reading files from the KB (copyArtifactFiles) but by writing the two
// generated files directly (bootstrapHookJSON, bootstrapScriptContent).

package provisioning

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/execbit"
)

// BootstrapHookName is the reserved hook name for the client-generated bootstrap
// hook (D60). A KB that defines a hook with this exact name collides with it:
// Apply ignores that KB artifact with a warning (see the reserved-name check in
// Apply) rather than letting it shadow the files EnsureBootstrapHook manages.
const BootstrapHookName = "cartographer-bootstrap"

// bootstrapScriptName and bootstrapScriptContent are platform-dependent, in
// bootstrapscript_unix.go and bootstrapscript_windows.go: a POSIX shell script
// is not executable on Windows in any sense the operating system recognises.

// bootstrapHookJSON renders the hook.json content the bootstrap hook's directory
// is materialized with — same shape KB hooks use (readHookSpec/hookSpec), so
// registerHookSettings/registerCodexHook/registerOpenCodePlugin work on it
// completely unmodified. SessionStart has no matcher semantics in any of the three
// engines, so Matcher is left empty.
func bootstrapHookJSON() []byte {
	data, err := json.Marshal(hookSpec{Event: "SessionStart", Command: "./" + bootstrapScriptName})
	if err != nil {
		// hookSpec is a plain struct of strings: Marshal cannot fail on it.
		panic(err)
	}
	return data
}

// bootstrapContentHash is a fixed content hash for the bootstrap hook's
// ManagedFile entries. It is never compared by ComputeDiff (the reserved name is
// excluded from diffing entirely, see ComputeDiff/Apply) — it only exists so the
// ManagedFile has a well-formed, non-empty ContentHash like every other kind.
var bootstrapContentHash = contentHashBytes(append(bootstrapHookJSON(), []byte(bootstrapScriptContent)...))

// EnsureBootstrapHook materializes and registers the cartographer-bootstrap hook
// (D60) for provider under baseDir, and returns lock with its ManagedFile entries
// refreshed (any previous bootstrap entries replaced — idempotent, safe to call on
// every `connect`/`sync`). Independent of any server manifest: this is what lets
// the hook install even when the server is unreachable at connect time, and what
// lets a later session self-heal once it comes back.
//
// A provider that cannot run a hook at session start is a no-op here, lock
// returned unchanged: hermes and crush have no hook mechanism at all
// (destDir("hook", _, p) == ""), antigravity has one but no session-start
// event (noSessionStartEvent). They sync on the scheduled trigger instead
// (D140). Kiro gets the hook, which fires only in some of its sessions
// (SessionHookLimit, D300).
//
// dryRun performs no I/O and simulates the resulting paths (mirrors Apply's own
// DryRun contract) — used by `connect --dry-run`/`sync --dry-run`.
//
// The returned Lock is meant to be persisted by the caller (merged into the v2
// LockFile) — from then on PruneManaged/`cartographer disconnect` remove the
// bootstrap hook's files and provider-native registration exactly like any
// KB-provided hook, no new removal code needed (see ComputeDiff/Apply for how the
// reserved name is protected from being flagged as a server-driven orphan in the
// meantime).
func EnsureBootstrapHook(baseDir string, provider configurator.Provider, lock Lock, dryRun bool) (Lock, error) {
	if !SupportsSessionHook(provider) {
		return lock, nil
	}
	return ensureSyntheticHook(baseDir, provider, lock, bootstrapHook, dryRun)
}

// syntheticHook is a hook generated entirely by the client (bootstrap, D60;
// write-findings, D353): its two files are constants, not read from a KB.
// ensureSyntheticHook is the one code path that materializes them, so both go
// through the same no-follow writers, registration and lock bookkeeping.
type syntheticHook struct {
	name          string
	hookJSON      func() []byte
	scriptName    string
	scriptContent string
	contentHash   string
	// warningBlocks makes a non-fatal registration warning fatal when the
	// provider's mechanism says so (warningBlocksBootstrap): true for the
	// bootstrap hook only, whose event is always mapped.
	warningBlocks bool
}

var bootstrapHook = syntheticHook{
	name:          BootstrapHookName,
	hookJSON:      bootstrapHookJSON,
	scriptName:    bootstrapScriptName,
	scriptContent: bootstrapScriptContent,
	contentHash:   bootstrapContentHash,
	warningBlocks: true,
}

func ensureSyntheticHook(baseDir string, provider configurator.Provider, lock Lock, h syntheticHook, dryRun bool) (Lock, error) {
	destRel := destDir("hook", h.name, provider)

	var relPaths []string
	if dryRun {
		relPaths = []string{
			filepath.Join(destRel, "hook.json"),
			filepath.Join(destRel, h.scriptName),
		}
		if pluginRel := HookPluginRelPath(provider, h.name); pluginRel != "" {
			relPaths = append(relPaths, pluginRel)
		}
	} else {
		fullDestDir := filepath.Join(baseDir, destRel)
		if err := mkdirAllNoFollow(baseDir, fullDestDir, 0o755); err != nil {
			return Lock{}, fmt.Errorf("provisioning: mkdir %s: %w", fullDestDir, err)
		}
		hookJSONPath := filepath.Join(fullDestDir, "hook.json")
		if err := writeFileNoFollow(hookJSONPath, h.hookJSON(), 0o644); err != nil {
			return Lock{}, fmt.Errorf("provisioning: write %s: %w", hookJSONPath, err)
		}
		scriptPath := filepath.Join(fullDestDir, h.scriptName)
		if err := writeFileNoFollow(scriptPath, []byte(h.scriptContent), 0o755); err != nil {
			return Lock{}, fmt.Errorf("provisioning: write %s: %w", scriptPath, err)
		}
		// WriteFile applies the mode only on creation: a pre-existing
		// script (e.g. written 0600 by an earlier version) would stay
		// non-executable → Permission denied on every run.
		//
		// Guarded on the one place that knows whether this filesystem has an
		// execute bit (internal/execbit). Where it has none, os.Chmod only
		// toggles the read-only attribute and returns nil: keeping the call
		// would be a line that claims to grant something it cannot, and the
		// error it could return would be about a permission that does not exist.
		if execbit.Supported {
			if err := os.Chmod(scriptPath, 0o755); err != nil {
				return Lock{}, fmt.Errorf("provisioning: chmod %s: %w", scriptPath, err)
			}
		}
		relPaths = []string{
			filepath.Join(destRel, "hook.json"),
			filepath.Join(destRel, h.scriptName),
		}

		// Register in the provider's native mechanism, reusing exactly the
		// D57/D58/D59 primitives — the very same code Apply uses for KB
		// hooks, with the hook.json just written above acting as the bridge.
		if mechanism, ok := hookMechanisms[provider]; ok {
			pluginRel, warning, err := mechanism.register(baseDir, h.name, fullDestDir)
			if err != nil {
				return Lock{}, err
			}
			if warning != "" && h.warningBlocks && mechanism.warningBlocksBootstrap {
				return Lock{}, fmt.Errorf("provisioning: %s hook: %s", h.name, warning)
			}
			if pluginRel != "" {
				relPaths = append(relPaths, pluginRel)
			}
		}
	}

	// The hash of what is on disk (D138), so on-disk verification (D139) and
	// `cartographer doctor` (D143) cover the hook like any other managed
	// artifact instead of reporting it as unverifiable forever. It is
	// computed from the same constants written above, never read back, and a
	// dry run records nothing: there is nothing on disk to describe.
	materializedHash := ""
	if !dryRun {
		materializedHash = hashArtifactFiles([]ArtifactFile{
			{Path: "hook.json", Content: h.hookJSON()},
			{Path: h.scriptName, Content: []byte(h.scriptContent), Executable: true},
		})
	}

	managed := make([]ManagedFile, 0, len(relPaths))
	for _, rp := range relPaths {
		managed = append(managed, ManagedFile{
			Kind:             "hook",
			Name:             h.name,
			Path:             rp,
			ContentHash:      h.contentHash,
			MaterializedHash: materializedHash,
			// The one file hashed as executable above. Recorded like any other
			// artifact's, so a client whose filesystem has no execute bit can
			// still reproduce this hash (ManagedFile.ExecutablePaths).
			ExecutablePaths: []string{h.scriptName},
		})
	}

	newManaged := make([]ManagedFile, 0, len(lock.Managed)+len(managed))
	for _, mf := range lock.Managed {
		if mf.Kind == "hook" && mf.Name == h.name {
			continue
		}
		newManaged = append(newManaged, mf)
	}
	lock.Managed = append(newManaged, managed...)
	return lock, nil
}

// hookMechanism describes how a provider registers a materialized hook in its
// own configuration (D137): claude patches settings.json (D57), codex its
// hooks.json (D230), opencode generates a plugin JS
// file that *is* the registration (D59), kiro its own standalone hook file
// (D300). A provider absent from hookMechanisms has no hook mechanism at all,
// and its "hook" cell in destinationMatrix is unsupported, so nothing reaches
// here for it.
type hookMechanism struct {
	// settingsFile is the provider-native file the registration is written
	// into, relative to the base dir. Empty when the registration is a
	// generated artifact of its own (opencode).
	settingsFile []string
	// pluginPath derives that generated artifact's path, relative to the base
	// dir. Nil when the provider has none.
	pluginPath func(name string) string
	// noSessionStartEvent marks a provider whose hook engine has no event
	// firing once at session start. It registers KB hooks normally, but the
	// bootstrap hook (D60) cannot exist for it: its trigger is the scheduled
	// timer instead (D140). True for antigravity, whose five events
	// (PreToolUse, PostToolUse, PreInvocation, PostInvocation, Stop) all fire
	// per tool call or per model invocation — mapping SessionStart onto one of
	// them would run `sync` on every turn, which is a different behaviour
	// wearing the same name. https://antigravity.google/docs/hooks/
	noSessionStartEvent bool
	// warningBlocksBootstrap makes a non-fatal warning fatal for the
	// bootstrap hook specifically. True only for opencode: its warning means
	// the hook's event has no OpenCode equivalent, and SessionStart is always
	// mapped (openCodeHookEvents) — a warning there would mean the mapping
	// regressed, and silently leaving the bootstrap hook unregistered is
	// worse than failing. Codex's warning is a D99 repair notice, informational.
	warningBlocksBootstrap bool
	// sessionHookLimit names the only sessions the bootstrap hook fires in,
	// when the client has session modes that run no hooks at all; empty when
	// it fires in every session. A provider with a limit still has its
	// bootstrap hook installed, and the scheduled timer is still advised for
	// it (SessionHookLimit), because a sync that runs only in some sessions
	// is not a trigger an operator can rely on (D300).
	sessionHookLimit string
	// writeFindingsHook marks a provider whose PostToolUse feedback channel
	// (exit 2 + stderr reaches the agent) was verified, so the write-findings
	// hook (D353) is installed for it. Declared, never inferred: a client is
	// added by a probe of its payload, matcher and feedback semantics.
	writeFindingsHook bool
	// register performs the registration, returning the relative path of any
	// generated artifact (so it is tracked as a managed file) plus any
	// non-fatal warning for the caller to surface.
	register func(baseDir, name, fullDestDir string) (relPath, warning string, err error)
}

var hookMechanisms = map[configurator.Provider]hookMechanism{
	configurator.ProviderClaudeCode: {
		settingsFile:      []string{".claude", "settings.json"},
		writeFindingsHook: true,
		register: func(baseDir, name, fullDestDir string) (string, string, error) {
			warning, err := registerHookSettings(baseDir, name, fullDestDir)
			if err != nil {
				return "", "", fmt.Errorf("provisioning: register hook %s in settings.json: %w", name, err)
			}
			return "", warning, nil
		},
	},
	configurator.ProviderCodex: {
		settingsFile: []string{".codex", "hooks.json"},
		register: func(baseDir, name, fullDestDir string) (string, string, error) {
			warning, err := registerCodexHook(baseDir, name, fullDestDir)
			if err != nil {
				return "", "", fmt.Errorf("provisioning: register hook %s in hooks.json: %w", name, err)
			}
			return "", warning, nil
		},
	},
	configurator.ProviderOpenCode: {
		pluginPath:             openCodePluginRelPath,
		warningBlocksBootstrap: true,
		register: func(baseDir, name, fullDestDir string) (string, string, error) {
			// Unlike claude/codex (patching an existing shared file), here the
			// registration produces a NEW dedicated file (the generated
			// plugin, D59) — the caller appends it to the managed files so it
			// is pruned like any other, not left as a silent side effect.
			//
			// openCodePluginRelPath prefixes with "cartographer-"
			// unconditionally (same helper every KB hook uses) — for
			// BootstrapHookName ("cartographer-bootstrap") that yields a
			// double-prefixed file name. Documented, deliberate (D60):
			// reusing the shared helper verbatim keeps registration and
			// removal (removeOpenCodePlugin, driven by PruneManaged/mf.Name)
			// on the exact same path-derivation logic.
			pluginRel, warning, err := registerOpenCodePlugin(baseDir, name, fullDestDir)
			if err != nil {
				return "", "", fmt.Errorf("provisioning: register hook %s as opencode plugin: %w", name, err)
			}
			return pluginRel, warning, nil
		},
	},
	configurator.ProviderAntigravity: {
		settingsFile:        []string{".gemini", "config", "hooks.json"},
		noSessionStartEvent: true,
		register: func(baseDir, name, fullDestDir string) (string, string, error) {
			warning, err := registerAntigravityHook(baseDir, name, fullDestDir)
			if err != nil {
				return "", "", fmt.Errorf("provisioning: register hook %s in Antigravity hooks.json: %w", name, err)
			}
			return "", warning, nil
		},
	},
	configurator.ProviderKiro: {
		settingsFile: kiroHooksRelPath,
		// Probed on 2.26.1 and 2.27.0: the standalone hook files load only
		// in the V3 TUI. Plain `kiro-cli chat` (whose interactive UI is the
		// new TUI since 2.27.0, over the v2 engine) and every
		// --no-interactive run never build the hook cache.
		sessionHookLimit: "kiro-cli chat --v3 --tui",
		register: func(baseDir, name, fullDestDir string) (string, string, error) {
			warning, err := registerKiroHook(baseDir, name, fullDestDir)
			if err != nil {
				return "", "", fmt.Errorf("provisioning: register hook %s in Kiro cartographer.json: %w", name, err)
			}
			return "", warning, nil
		},
	},
}

// SupportsSessionHook reports whether the bootstrap hook (D60) can run at
// session start for this provider: it needs both a destination for the hook's
// files and a native registration mechanism. A provider without one syncs
// only on demand, or on the scheduled trigger (D140,
// `cartographer service sync-timer install`).
func SupportsSessionHook(provider configurator.Provider) bool {
	if destDir("hook", BootstrapHookName, provider) == "" {
		return false
	}
	m, ok := hookMechanisms[provider]
	return ok && !m.noSessionStartEvent
}

// SessionHookLimit names the only sessions in which provider's bootstrap hook
// fires, or "" when it fires in every session (or the provider has none — see
// SupportsSessionHook). A non-empty limit means the hook is installed and the
// scheduled timer is still needed for every other session (D300).
func SessionHookLimit(provider configurator.Provider) string {
	if !SupportsSessionHook(provider) {
		return ""
	}
	return hookMechanisms[provider].sessionHookLimit
}

// HookRegistrationFile returns the provider-native file a hook registration is
// written into, relative to the client base dir, or "" when the provider
// registers through a generated artifact instead (or has no hook mechanism).
// Exported for the client's own output: `cartographer sync` reports where a
// hook was registered.
func HookRegistrationFile(provider configurator.Provider) string {
	m, ok := hookMechanisms[provider]
	if !ok || len(m.settingsFile) == 0 {
		return ""
	}
	return filepath.Join(m.settingsFile...)
}

// HookPluginRelPath returns the path, relative to the client base dir, of the
// dedicated registration artifact this provider generates for hook name — or
// "" when it registers into a shared settings file instead.
func HookPluginRelPath(provider configurator.Provider, name string) string {
	m, ok := hookMechanisms[provider]
	if !ok || m.pluginPath == nil {
		return ""
	}
	return m.pluginPath(name)
}
