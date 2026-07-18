package mcpserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// flushPendingPush is called at the start of sync-sensitive tool handlers
// (D76/WP4) so they don't race an in-flight async push scheduled by a
// preceding write. Best-effort: a timeout/failure is logged and does not
// block the handler — consistent with push errors being non-fatal
// elsewhere (gitwrap.go). No-op if no push is pending.
func flushPendingPush(k *kb.KB, toolName string) {
	if err := k.FlushPush(pushFlushTimeout); err != nil {
		fmt.Fprintf(os.Stderr, "cartographer: flush pending push (%s): %v\n", toolName, err)
	}
}

// --- sync_check ---

// toolSyncCheck returns the current revision of the provisioning manifest (bundle + KB).
// Read-only: writes nothing, safe on a remote server too.
// The client may pass its own lockfile revision; the response includes in_sync=true/false.
func toolSyncCheck(k *kb.KB, bundleFS fs.FS) Tool {
	return Tool{
		Name:     "sync_check",
		ReadOnly: true,
		Description: "Checks client ↔ server alignment for provisioning skills and artifacts. " +
			"Returns the current manifest revision and, if the client's lockfile applied_revision is provided, " +
			"reports whether the client is in-sync or in drift. Read-only.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"applied_revision": {
					"type": "string",
					"description": "Revision of the last lockfile applied by the client (optional). If provided, the response includes in_sync=true/false."
				}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			flushPendingPush(k, "sync_check")
			var params struct {
				AppliedRevision string `json:"applied_revision"`
			}
			json.Unmarshal(args, &params)

			// Use the basename of k.Root as the KB name in the manifest.
			kbName := filepath.Base(k.Root)
			m, err := provisioning.BuildManifest(bundleFS, map[string]string{kbName: k.Root}, false)
			if err != nil {
				return errorResult(fmt.Sprintf("sync_check: build manifest: %v", err)), nil
			}

			inSync := params.AppliedRevision == "" || params.AppliedRevision == m.Revision

			type artifactJSON struct {
				Kind    string `json:"kind"`
				Name    string `json:"name"`
				Source  string `json:"source"`
				Version string `json:"version,omitempty"`
				Signed  bool   `json:"signed"`
			}
			arts := make([]artifactJSON, len(m.Artifacts))
			for i, a := range m.Artifacts {
				arts[i] = artifactJSON{
					Kind:    a.Kind,
					Name:    a.Name,
					Source:  a.Source,
					Version: a.Version,
					Signed:  a.Signed,
				}
			}

			// Include the count of open git conflicts so the SessionStart hook can
			// surface them and direct the agent to kb-conflict-resolve.
			openConflicts := 0
			if cs, cerr := k.ListConflicts(); cerr == nil {
				openConflicts = len(cs)
			}

			result := map[string]interface{}{
				"revision":       m.Revision,
				"in_sync":        inSync,
				"artifacts":      arts,
				"open_conflicts": openConflicts,
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- sync_apply ---

// toolSyncApply materializes the provisioning artifacts into the client's base_dir.
// Intended for local deployment (stdio): server and client share the filesystem.
// For remote deployment use `cartographer sync`/`cartographer connect` (via sync_pull) from the client machine.
func toolSyncApply(k *kb.KB, bundleFS fs.FS) Tool {
	return Tool{
		Name: "sync_apply",
		Description: "Materializes the provisioning skills into the given base_dir, updates the lockfile and prunes " +
			"stale artifacts. Intended for local deployment (stdio) where server and client share the FS. " +
			"Only artifacts with signed=true are written; the others go into needs_approval. " +
			"Supports dry_run=true to show the diff without writing.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["base_dir"],
			"properties": {
				"base_dir": {
					"type": "string",
					"description": "Client base directory where artifacts are materialized (e.g. workspace root)"
				},
				"dry_run": {
					"type": "boolean",
					"description": "If true, computes the diff but writes nothing (default false)"
				},
				"auto_trust": {
					"type": "boolean",
					"description": "If true, KB skills are considered trusted too (opt-in workspace policy). Default false."
				}
			}
		}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			flushPendingPush(k, "sync_apply")
			var params struct {
				BaseDir   string `json:"base_dir"`
				DryRun    bool   `json:"dry_run"`
				AutoTrust bool   `json:"auto_trust"`
			}
			if err := json.Unmarshal(args, &params); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			if params.BaseDir == "" {
				return errorResult("'base_dir' is required"), nil
			}

			kbName := filepath.Base(k.Root)
			kbRoots := map[string]string{kbName: k.Root}

			m, err := provisioning.BuildManifest(bundleFS, kbRoots, params.AutoTrust)
			if err != nil {
				return errorResult(fmt.Sprintf("sync_apply: build manifest: %v", err)), nil
			}

			lockPath := filepath.Join(params.BaseDir, provisioning.LockFileName)
			lock, err := provisioning.ReadLock(lockPath)
			if err != nil {
				return errorResult(fmt.Sprintf("sync_apply: read lock: %v", err)), nil
			}

			applyOpts := provisioning.ApplyOptions{
				BundleFS:  bundleFS,
				KBRoots:   kbRoots,
				Provider:  configurator.ProviderClaudeCode,
				BaseDir:   params.BaseDir,
				DryRun:    params.DryRun,
				AutoTrust: params.AutoTrust,
				Lock:      lock,
			}

			applied, err := provisioning.Apply(m, applyOpts)
			if err != nil {
				return errorResult(fmt.Sprintf("sync_apply: %v", err)), nil
			}

			type mfJSON struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
				Path string `json:"path"`
			}
			type artJSON struct {
				Kind   string `json:"kind"`
				Name   string `json:"name"`
				Source string `json:"source"`
			}

			toMFJSON := func(files []provisioning.ManagedFile) []mfJSON {
				r := make([]mfJSON, len(files))
				for i, f := range files {
					r[i] = mfJSON{Kind: f.Kind, Name: f.Name, Path: f.Path}
				}
				return r
			}
			toArtJSON := func(arts []provisioning.Artifact) []artJSON {
				r := make([]artJSON, len(arts))
				for i, a := range arts {
					r[i] = artJSON{Kind: a.Kind, Name: a.Name, Source: a.Source}
				}
				return r
			}

			result := map[string]interface{}{
				"revision":       m.Revision,
				"new_revision":   applied.NewLock.AppliedRevision,
				"written":        toMFJSON(applied.Written),
				"pruned":         toMFJSON(applied.Pruned),
				"needs_approval": toArtJSON(applied.NeedsApproval),
				"dry_run":        params.DryRun,
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// --- sync_pull ---

// pulledFileJSON is a single artifact file serialized for transport: path relative
// to the artifact's own directory, content base64-encoded (skills are small text
// files; base64 keeps the JSON-RPC payload simple and transport-safe).
type pulledFileJSON struct {
	Path       string `json:"path"`
	ContentB64 string `json:"content_b64"`
}

// pulledArtifactJSON is an Artifact plus its file contents, as returned by sync_pull.
type pulledArtifactJSON struct {
	Kind        string           `json:"kind"`
	Name        string           `json:"name"`
	Source      string           `json:"source"`
	Version     string           `json:"version,omitempty"`
	ContentHash string           `json:"content_hash"`
	Signed      bool             `json:"signed"`
	Files       []pulledFileJSON `json:"files"`
}

// toolSyncPull returns the provisioning manifest (bundle + KB) with each
// artifact's file contents embedded (base64). Meant for a remote HTTP client
// that does not share the filesystem with the server: the client materializes
// locally without reading bundle/KB directly. Read-only, no arguments required.
func toolSyncPull(k *kb.KB, bundleFS fs.FS) Tool {
	return Tool{
		Name:     "sync_pull",
		ReadOnly: true,
		Description: "Returns the provisioning manifest (bundle and KB skills) with file contents " +
			"embedded in base64, ready for client-side materialization over HTTP (no shared filesystem " +
			"with the server required). Read-only. Used by `cartographer connect`/`cartographer sync` on the client side.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {}}`),
		Handler: func(args json.RawMessage) (ToolResult, error) {
			flushPendingPush(k, "sync_pull")
			kbName := filepath.Base(k.Root)
			kbRoots := map[string]string{kbName: k.Root}

			m, err := provisioning.BuildManifest(bundleFS, kbRoots, false)
			if err != nil {
				return errorResult(fmt.Sprintf("sync_pull: build manifest: %v", err)), nil
			}

			arts := make([]pulledArtifactJSON, 0, len(m.Artifacts))
			for _, a := range m.Artifacts {
				files, err := provisioning.ReadArtifactFiles(a, bundleFS, kbRoots)
				if err != nil {
					return errorResult(fmt.Sprintf("sync_pull: read files of %s/%s: %v", a.Kind, a.Name, err)), nil
				}
				fj := make([]pulledFileJSON, len(files))
				for i, f := range files {
					fj[i] = pulledFileJSON{Path: f.Path, ContentB64: base64.StdEncoding.EncodeToString(f.Content)}
				}
				arts = append(arts, pulledArtifactJSON{
					Kind:        a.Kind,
					Name:        a.Name,
					Source:      a.Source,
					Version:     a.Version,
					ContentHash: a.ContentHash,
					Signed:      a.Signed,
					Files:       fj,
				})
			}

			result := map[string]interface{}{
				"revision":  m.Revision,
				"artifacts": arts,
			}
			out, _ := json.MarshalIndent(result, "", "  ")
			return textResult(string(out)), nil
		},
	}
}
