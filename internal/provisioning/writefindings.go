// writefindings.go (D353) — the "cartographer-write-findings" hook: a second
// synthetic, client-generated hook (same family as the D60 bootstrap hook) that
// turns the `findings` of a write response into feedback the agent must act on.
// The hook only shells out to `cartographer hook write-findings`, which holds
// the logic (cmd/cartographer/hook.go): parsing JSON in sh/cmd would need jq.
//
// It goes through ensureSyntheticHook, the same no-follow writers and the same
// registration primitives as the bootstrap hook; its name is in
// reservedHookNames, so a KB hook of the same name is ignored and the hook is
// never an orphan in ComputeDiff.

package provisioning

import (
	"encoding/json"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// WriteFindingsHookName is the reserved hook name of the client-generated
// write-findings hook (D353).
const WriteFindingsHookName = "cartographer-write-findings"

// WriteFindingsTools are the MCP tools whose response carries `findings`
// (D312). A test in internal/mcpserver checks this list against the handlers
// that assign them, so a new write tool cannot be forgotten.
var WriteFindingsTools = []string{
	"concept_write", "concept_new", "concept_patch", "concept_batch",
	"concept_move", "concept_archive", "supersede", "index_patch",
}

// writeFindingsMatcher is the Claude Code PostToolUse matcher (a regex): the
// middle segment is the configured server_name, so it is a wildcard.
func writeFindingsMatcher() string {
	return "mcp__.*__(" + strings.Join(WriteFindingsTools, "|") + ")$"
}

func writeFindingsHookJSON() []byte {
	data, err := json.Marshal(hookSpec{
		Event:   "PostToolUse",
		Matcher: writeFindingsMatcher(),
		Command: "./" + writeFindingsScriptName,
	})
	if err != nil {
		// hookSpec is a plain struct of strings: Marshal cannot fail on it.
		panic(err)
	}
	return data
}

var writeFindingsContentHash = contentHashBytes(append(writeFindingsHookJSON(), []byte(writeFindingsScriptContent)...))

var writeFindingsSynthetic = syntheticHook{
	name:          WriteFindingsHookName,
	hookJSON:      writeFindingsHookJSON,
	scriptName:    writeFindingsScriptName,
	scriptContent: writeFindingsScriptContent,
	contentHash:   writeFindingsContentHash,
}

// SupportsWriteFindingsHook reports whether the write-findings hook (D353) is
// installed for provider: only clients whose PostToolUse payload and feedback
// channel were verified (claude).
func SupportsWriteFindingsHook(provider configurator.Provider) bool {
	return hookMechanisms[provider].writeFindingsHook
}

// reservedHookNames are the hook names the client generates itself: a KB hook
// carrying one is ignored with a warning (Apply), and a managed entry carrying
// one is never driven by the server manifest (ComputeDiff, Apply).
var reservedHookNames = map[string]bool{
	BootstrapHookName:     true,
	WriteFindingsHookName: true,
}

func isReservedHook(kind, name string) bool {
	return kind == "hook" && reservedHookNames[name]
}

// EnsureWriteFindingsHook materializes and registers the write-findings hook
// for provider and returns lock with its ManagedFile entries refreshed
// (idempotent). A provider without the verified feedback channel is a no-op.
// enabled=false (`write_findings_hook: false` in .cartographer.yaml) removes a
// previously installed hook through PruneManaged, the path `disconnect` uses;
// idempotent when absent. dryRun performs no I/O.
func EnsureWriteFindingsHook(baseDir string, provider configurator.Provider, lock Lock, enabled, dryRun bool) (Lock, error) {
	if !SupportsWriteFindingsHook(provider) {
		return lock, nil
	}
	if enabled {
		return ensureSyntheticHook(baseDir, provider, lock, writeFindingsSynthetic, dryRun)
	}
	var drop, keep []ManagedFile
	for _, mf := range lock.Managed {
		if mf.Kind == "hook" && mf.Name == WriteFindingsHookName {
			drop = append(drop, mf)
		} else {
			keep = append(keep, mf)
		}
	}
	if len(drop) == 0 {
		return lock, nil
	}
	if _, err := PruneManaged(drop, baseDir, dryRun); err != nil {
		return Lock{}, err
	}
	if !dryRun {
		lock.Managed = keep
	}
	return lock, nil
}
