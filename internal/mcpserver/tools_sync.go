package mcpserver

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/BeppeTemp/cartographer/internal/artifactsig"
	"github.com/BeppeTemp/cartographer/internal/feedback"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// toolSyncStatus returns the authoritative local commit/push replication state.
func toolSyncStatus(k *kb.KB) Tool {
	return Tool{Name: "sync_status", ReadOnly: true,
		Description: "Returns local git commit and remote replication status, including pending or failed pushes. Read-only.",
		InputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			s := k.GitStatusSnapshot()
			mode := "synchronous"
			if k.SyncOutDebounce > 0 {
				mode = "debounced"
			}
			return jsonResult(map[string]any{"state": s.State, "last_error": statusError(ctx, s), "last_attempt_at": s.LastAttemptAt, "head_sha": s.HeadSHA, "unpushed_commits": s.UnpushedCommits, "behind_commits": s.BehindCommits, "last_push_at": s.LastPushAt, "last_sync_in_at": s.LastSyncInAt, "attempts": s.Attempts, "branch": s.Branch, "remote_default_branch": s.RemoteDefaultBranch, "branch_source": s.BranchSource, "push_mode": mode}), nil
		},
	}
}

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
func toolSyncCheck(k *kb.KB, bundleFS fs.FS, routedMount bool, signer ed25519.PrivateKey) Tool {
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
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			flushPendingPush(k, "sync_check")
			var params struct {
				AppliedRevision string `json:"applied_revision"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &params); err != nil {
					return errorResult("invalid params: " + err.Error()), nil
				}
			}

			// Use the basename of k.Root as the KB name in the manifest.
			kbName := filepath.Base(k.Root)
			m, err := contract.BuildManifest(bundleFS, map[string]string{kbName: k.Root}, buildOptions(kbName, routedMount, signer))
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
				BuiltIn bool   `json:"built_in,omitempty"`
			}
			arts := make([]artifactJSON, len(m.Artifacts))
			for i, a := range m.Artifacts {
				arts[i] = artifactJSON{
					Kind:    a.Kind,
					Name:    a.Name,
					Source:  a.Source,
					Version: a.Version,
					Signed:  a.Signed,
					BuiltIn: a.BuiltIn,
				}
			}

			// Include the count of open git conflicts so the SessionStart hook can
			// surface them and direct the agent to cartographer-kb-conflict-resolve.
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
			return jsonResult(result), nil
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
	Executable bool   `json:"executable"`
}

// pulledArtifactJSON is an Artifact plus its file contents, as returned by sync_pull.
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

// toolSyncPull returns the provisioning manifest (bundle + KB) with each
// artifact's file contents embedded (base64). Meant for a remote HTTP client
// that does not share the filesystem with the server: the client materializes
// locally without reading bundle/KB directly. Read-only, no arguments required.
func toolSyncPull(k *kb.KB, bundleFS fs.FS, routedMount bool, signer ed25519.PrivateKey, fb func() feedback.Destination) Tool {
	return Tool{
		Name:     "sync_pull",
		ReadOnly: true,
		Description: "Returns the provisioning manifest (bundle and KB skills) with file contents " +
			"embedded in base64, ready for client-side materialization over HTTP (no shared filesystem " +
			"with the server required). Read-only. Used by `cartographer connect`/`cartographer sync` on the client side.",
		InputSchema: json.RawMessage(`{"type": "object", "properties": {"include_cli_skill": {"type": "boolean", "description": "Also list the bundled cartographer-cli skill. Sent by every client that filters it per provider (D1700); a client that never sends it never receives it."}}}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			flushPendingPush(k, "sync_pull")
			var params struct {
				IncludeCLISkill bool `json:"include_cli_skill"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &params); err != nil {
					return errorResult("invalid params: " + err.Error()), nil
				}
			}
			kbName := filepath.Base(k.Root)
			kbRoots := map[string]string{kbName: k.Root}

			bo := buildOptions(kbName, routedMount, signer)
			bo.IncludeCLISkill = params.IncludeCLISkill
			m, err := contract.BuildManifest(bundleFS, kbRoots, bo)
			if err != nil {
				return errorResult(fmt.Sprintf("sync_pull: build manifest: %v", err)), nil
			}

			arts := make([]pulledArtifactJSON, 0, len(m.Artifacts))
			var artifactKeys []string
			var excluded []string
			var embedded int
			for _, a := range m.Artifacts {
				files, err := contract.ReadArtifactFilesCapped(a, bundleFS, kbRoots)
				if errors.Is(err, contract.ErrNotRegular) {
					excluded = append(excluded, fmt.Sprintf("%s %q excluded from sync_pull: not a regular file", a.Kind, a.Name))
					continue
				}
				if err != nil {
					return errorResult(fmt.Sprintf("sync_pull: read files of %s/%s: %v", a.Kind, a.Name, relPathErr(err, k.Root))), nil
				}
				// An artifact with a file over the cap is left out whole, not
				// shipped without that file: a client must never materialize
				// half a skill. The response as a whole is bounded too.
				if why := pullExclusion(files, embedded); why != "" {
					excluded = append(excluded, fmt.Sprintf("%s %q excluded from sync_pull: %s", a.Kind, a.Name, why))
					continue
				}
				for _, f := range files {
					embedded += len(f.Content)
				}
				fj := make([]pulledFileJSON, len(files))
				for i, f := range files {
					fj[i] = pulledFileJSON{Path: f.Path, ContentB64: base64.StdEncoding.EncodeToString(f.Content), Executable: f.Executable}
					// This KB's own artifacts only: the bundle is
					// Cartographer's, and its keys are not this KB's.
					if a.Source == "kb:"+kbName {
						artifactKeys = append(artifactKeys, okf.Placeholders(string(f.Content))...)
					}
				}
				arts = append(arts, pulledArtifactJSON{
					Kind:        a.Kind,
					Name:        a.Name,
					Source:      a.Source,
					Version:     a.Version,
					ContentHash: a.ContentHash,
					Signed:      a.Signed,
					BuiltIn:     a.BuiltIn,
					Signature:   a.Signature,
					Files:       fj,
				})
			}

			result := map[string]interface{}{
				"revision":  m.Revision,
				"artifacts": arts,
			}
			placeholders, err := pulledPlaceholders(ctx, k, artifactKeys)
			if err != nil {
				return errorResult(fmt.Sprintf("sync_pull: placeholders: %v", err)), nil
			}
			if len(placeholders) > 0 {
				result["placeholders"] = placeholders
			}
			registry, registryIssues := pulledPathRegistry(k)
			if registry != nil {
				result["path_registry"] = registry
			}
			if issues := append(append(append([]string(nil), m.Issues...), registryIssues...), excluded...); len(issues) > 0 {
				result["issues"] = issues
			}
			// Absent means github: a client or server that predates the setting
			// agrees (the managed block only needs the mode).
			if fb != nil {
				if mode := fb().Mode(); mode != feedback.ModeGitHub {
					result["feedback"] = map[string]interface{}{"destination": string(mode)}
				}
			}
			return jsonResult(result), nil
		},
	}
}

// syncPullMaxBytes bounds the file bytes one sync_pull embeds (base64 adds a
// third): 8 MiB is about 50 times the whole bundled skill set (148 KiB, the
// largest skill 40 KiB), and 32 files at the per-file cap.
const syncPullMaxBytes = 8 << 20

// pullExclusion says why an artifact cannot go into the response, or "".
func pullExclusion(files []contract.ArtifactFile, embedded int) string {
	total := 0
	for _, f := range files {
		if f.Oversized > 0 {
			return fmt.Sprintf("%s is %d bytes, over the %d bytes cap of an artifact file (or the artifact is past %d files or %d bytes in all)", f.Path, f.Oversized, artifactMaxFileSize, contract.ArtifactMaxFiles, contract.ArtifactMaxTotalSize)
		}
		total += len(f.Content)
	}
	if embedded+total > syncPullMaxBytes {
		return fmt.Sprintf("the response would pass %d bytes of file content", syncPullMaxBytes)
	}
	return ""
}

// pulledPlaceholders lists the {{repo:…}}/{{path:…}} keys this KB cites, as
// sorted, unique "kind:key" strings (D262): the ones in the bodies of the
// concepts this caller may see, plus the ones in this KB's own artifacts, so a
// key used only in a skill is listed too. The server still expands nothing
// (D75) — it only tells the client which keys to resolve.
//
// The concept half reads the stat-validated graph cache (D241) through
// visibleGraph, never a walk per pull, and never lists a key only a hidden
// concept cites: a narrowed principal learns nothing about a concept it cannot
// read, not even the name of a repository it mentions. It sits outside
// revision and outside every signature, like Manifest.Issues: a concept
// starting to cite a key is derived data, not a catalogue change.
func pulledPlaceholders(ctx requestContext, k *kb.KB, artifactKeys []string) ([]string, error) {
	lg, err := visibleGraph(ctx, k)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	add := func(ids []string) {
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	for _, f := range lg.Facets {
		add(f.Placeholders)
	}
	add(artifactKeys)
	sort.Strings(out)
	return out, nil
}

// pulledPathRegistry is the KB's declared placeholder vocabulary for the
// sync_pull response (D263): nil when the KB has no paths.yaml, otherwise the
// parsed registry without its malformed entries, whose reasons are returned
// for the response's issue list. Like placeholders it is outside revision and
// every signature — a declaration is data a client resolves with, not an
// artifact it installs. A registry that cannot be read at all (a symlink, not
// YAML) is an issue, not a failed pull: resolution must never block a sync.
func pulledPathRegistry(k *kb.KB) (*kb.PathRegistry, []string) {
	st, err := k.ReadPathRegistry()
	if err != nil {
		return nil, []string{err.Error()}
	}
	if !st.Present {
		return nil, nil
	}
	var issues []string
	for _, m := range st.Malformed {
		issues = append(issues, m.String()+" (left out of path_registry)")
	}
	if st.Unparseable {
		return nil, issues
	}
	return &st.Registry, issues
}

// buildOptions assembles the manifest build options for the single KB this
// server instance serves. routedMount is whether the server routes its KBs
// through one endpoint (D187): it reaches the generated instructions block so
// the imprinting names the arguments the agent must pass (D144).
func buildOptions(kbName string, routedMount bool, signer ed25519.PrivateKey) contract.BuildOptions {
	opts := contract.BuildOptions{
		RoutedMount: routedMount,
	}
	if len(signer) != 0 {
		opts.Signers = map[string]ed25519.PrivateKey{kbName: signer}
	}
	return opts
}
