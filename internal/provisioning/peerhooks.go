// peerhooks.go (D341) — the two client-generated hooks behind agent-to-agent
// messaging: at session start the session registers with the server and learns
// its own peer id, and at every stop the messages waiting for it are handed to
// the agent before it goes idle. Like the bootstrap hook (D60) they are
// synthetic artifacts the server manifest never contains, materialized and
// registered with the same per-provider primitives, and installed only on
// `cartographer peer enable`.

package provisioning

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/execbit"
)

// The reserved peer hook names. A KB hook with one of them is ignored, exactly
// like one named BootstrapHookName.
const (
	PeerStartHookName = "cartographer-peers-start"
	PeerStopHookName  = "cartographer-peers-stop"
)

// isClientHook reports whether name belongs to a hook the client generates on
// its own rather than receiving from a KB: those are carried through every
// sync untouched and never written from a manifest.
func isClientHook(name string) bool {
	return name == BootstrapHookName || name == PeerStartHookName || name == PeerStopHookName
}

// peerHookEvents are the two hooks and the event each registers on. The
// script passes the provider to `cartographer peer hook`, so the same command
// knows how to read the event and how to answer it.
var peerHookEvents = []struct{ name, event string }{
	{PeerStartHookName, "SessionStart"},
	{PeerStopHookName, "Stop"},
}

// SupportsPeerHooks reports whether provider gets the peer hooks. Claude Code,
// Codex and Kiro hand a hook its session id on stdin and let a Stop hook keep
// the agent going; OpenCode's hook bridge runs a command with neither, so its
// sessions are found and served by the relay instead (D341).
func SupportsPeerHooks(provider configurator.Provider) bool {
	switch provider {
	case configurator.ProviderClaudeCode, configurator.ProviderCodex, configurator.ProviderKiro:
		return SupportsSessionHook(provider)
	}
	return false
}

// EnsurePeerHooks materializes and registers both peer hooks for provider
// under baseDir and returns lock with their ManagedFile entries replaced. A
// provider without support is a no-op.
func EnsurePeerHooks(baseDir string, provider configurator.Provider, lock Lock) (Lock, error) {
	if !SupportsPeerHooks(provider) {
		return lock, nil
	}
	mechanism, ok := hookMechanisms[provider]
	if !ok {
		return lock, nil
	}
	var managed []ManagedFile
	for _, h := range peerHookEvents {
		script := peerHookScript(string(provider), h.event)
		hookJSON := hookJSONFor(h.event, peerHookScriptName)
		destRel := destDir("hook", h.name, provider)
		fullDestDir := filepath.Join(baseDir, destRel)
		if err := mkdirAllNoFollow(baseDir, fullDestDir, 0o755); err != nil {
			return Lock{}, fmt.Errorf("provisioning: mkdir %s: %w", fullDestDir, err)
		}
		if err := writeFileNoFollow(filepath.Join(fullDestDir, "hook.json"), hookJSON, 0o644); err != nil {
			return Lock{}, fmt.Errorf("provisioning: write %s hook.json: %w", h.name, err)
		}
		scriptPath := filepath.Join(fullDestDir, peerHookScriptName)
		if err := writeFileNoFollow(scriptPath, []byte(script), 0o755); err != nil {
			return Lock{}, fmt.Errorf("provisioning: write %s: %w", scriptPath, err)
		}
		if execbit.Supported {
			if err := os.Chmod(scriptPath, 0o755); err != nil {
				return Lock{}, fmt.Errorf("provisioning: chmod %s: %w", scriptPath, err)
			}
		}
		relPaths := []string{filepath.Join(destRel, "hook.json"), filepath.Join(destRel, peerHookScriptName)}
		pluginRel, warning, err := mechanism.register(baseDir, h.name, fullDestDir)
		if err != nil {
			return Lock{}, err
		}
		if warning != "" && mechanism.warningBlocksBootstrap {
			return Lock{}, fmt.Errorf("provisioning: peer hook %s: %s", h.name, warning)
		}
		if pluginRel != "" {
			relPaths = append(relPaths, pluginRel)
		}
		files := []ArtifactFile{
			{Path: "hook.json", Content: hookJSON},
			{Path: peerHookScriptName, Content: []byte(script), Executable: true},
		}
		hash := hashArtifactFiles(files)
		for _, rp := range relPaths {
			managed = append(managed, ManagedFile{
				Kind:             "hook",
				Name:             h.name,
				Path:             rp,
				ContentHash:      contentHashBytes(append(hookJSON, script...)),
				MaterializedHash: hash,
				ExecutablePaths:  []string{peerHookScriptName},
			})
		}
	}
	kept := make([]ManagedFile, 0, len(lock.Managed)+len(managed))
	for _, mf := range lock.Managed {
		if mf.Kind == "hook" && (mf.Name == PeerStartHookName || mf.Name == PeerStopHookName) {
			continue
		}
		kept = append(kept, mf)
	}
	lock.Managed = append(kept, managed...)
	return lock, nil
}

// RemovePeerHooks prunes both peer hooks — files and provider registration —
// and returns lock without them.
func RemovePeerHooks(baseDir string, lock Lock) (Lock, error) {
	var mine, kept []ManagedFile
	for _, mf := range lock.Managed {
		if mf.Kind == "hook" && (mf.Name == PeerStartHookName || mf.Name == PeerStopHookName) {
			mine = append(mine, mf)
			continue
		}
		kept = append(kept, mf)
	}
	if len(mine) == 0 {
		return lock, nil
	}
	if _, err := PruneManaged(mine, baseDir, false); err != nil {
		return Lock{}, err
	}
	lock.Managed = kept
	return lock, nil
}

// hookJSONFor renders the hook.json a client-generated hook is materialized
// with, in the shape every registration primitive already reads (hookSpec).
func hookJSONFor(event, script string) []byte {
	data, err := json.Marshal(hookSpec{Event: event, Command: "./" + script})
	if err != nil {
		// hookSpec is a plain struct of strings: Marshal cannot fail on it.
		panic(err)
	}
	return data
}
