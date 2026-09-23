package mcpserver

import (
	"net/http"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// The Atlas UI's artifact routes (D238): what a KB ships to agent clients,
// for a human to read. Artifacts are whole-KB resources (policy.go), so both
// routes answer 404 to a principal that cannot see the whole KB, the same
// non-disclosure rule as uiKBVisible. Nothing is decrypted: a descriptor's
// secret references are shown as written, as artifact_read returns them.

var uiArtifactKinds = map[string]bool{
	"skill": true, "agent": true, "hook": true, "mcp": true, "instructions": true, "template": true,
}

type uiArtifactClient struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type uiArtifactFile struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	Size       int    `json:"size"`
	Executable bool   `json:"executable"`
	// Detail route only: the text, or why it is not sent.
	Content   *string `json:"content,omitempty"`
	Binary    bool    `json:"binary,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
}

type uiArtifact struct {
	Kind        string             `json:"kind"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	ContentHash string             `json:"content_hash,omitempty"`
	Signed      *bool              `json:"signed,omitempty"`
	Clients     []uiArtifactClient `json:"clients"`
	Files       []uiArtifactFile   `json:"files"`
}

func uiArtifactFrom(a kbArtifact, withContent bool) uiArtifact {
	out := uiArtifact{
		Kind:        a.Kind,
		Name:        a.Name,
		Description: a.description(),
		Clients:     []uiArtifactClient{},
		Files:       []uiArtifactFile{},
	}
	if a.Manifest != nil {
		signed := a.Manifest.Signed
		out.ContentHash = a.Manifest.ContentHash
		out.Signed = &signed
	}
	// The curated instructions.md has no manifest entry of its own here, but
	// it does reach clients: sync folds it into the generated instructions
	// block (D61). Templates never leave the KB, and the matrix has no row
	// for them.
	names := map[configurator.Provider]string{}
	for _, d := range configurator.Providers() {
		names[d.Provider] = d.DisplayName
	}
	for _, p := range provisioning.Destinations(a.Kind, a.Name) {
		out.Clients = append(out.Clients, uiArtifactClient{ID: string(p), Name: names[p]})
	}
	for _, f := range a.Files {
		file := uiArtifactFile{Path: f.Path, SHA256: sha256Hex(f.Content), Size: len(f.Content), Executable: f.Executable}
		if withContent {
			switch {
			case !utf8.Valid(f.Content):
				file.Binary = true
			case len(f.Content) > artifactMaxFileSize:
				file.Truncated = true
			default:
				text := string(f.Content)
				file.Content = &text
			}
		}
		out.Files = append(out.Files, file)
	}
	return out
}

func (m *MultiKBServer) uiArtifacts(w http.ResponseWriter, r *http.Request, srv *Server) {
	if !WholeVisible(r.Context(), srv.kbRef, false) {
		writeUINotFound(w)
		return
	}
	catalog, err := listKBArtifacts(srv.kbRef, srv.kbArtifacts.allowlist, srv.kbArtifacts.signer)
	if err != nil {
		writeUIInternal(w, "artifacts", err)
		return
	}
	list := make([]uiArtifact, 0, len(catalog.Artifacts))
	counts := map[string]int{}
	for _, a := range catalog.Artifacts {
		list = append(list, uiArtifactFrom(a, false))
		counts[a.Kind]++
	}
	issues := catalog.Issues
	if issues == nil {
		issues = []string{}
	}
	writeUIJSON(w, http.StatusOK, map[string]interface{}{"artifacts": list, "counts": counts, "issues": issues})
}

func (m *MultiKBServer) uiArtifact(w http.ResponseWriter, r *http.Request, srv *Server) {
	if !WholeVisible(r.Context(), srv.kbRef, false) {
		writeUINotFound(w)
		return
	}
	q := r.URL.Query()
	kind, name := q.Get("kind"), q.Get("name")
	if kind == "" {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "kind is required", "kind")
		return
	}
	if !uiArtifactKinds[kind] {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "unknown artifact kind", "kind")
		return
	}
	if name == "" {
		writeUIError(w, http.StatusBadRequest, uiCodeInvalidRequest, "name is required", "name")
		return
	}
	catalog, err := listKBArtifacts(srv.kbRef, srv.kbArtifacts.allowlist, srv.kbArtifacts.signer)
	if err != nil {
		writeUIInternal(w, "artifact", err)
		return
	}
	for _, a := range catalog.Artifacts {
		if a.Kind == kind && a.Name == name {
			writeUIJSON(w, http.StatusOK, uiArtifactFrom(a, true))
			return
		}
	}
	writeUINotFound(w)
}

// uiArtifactsVisible is the `artifacts` flag of GET /kbs: whether the
// principal may open the Artifacts panel for this KB at all.
func uiArtifactsVisible(ctx requestContext, k *kb.KB) bool {
	return WholeVisible(ctx, k, false)
}
