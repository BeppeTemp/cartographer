package mcpserver

import (
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
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
	// Concepts the artifact references explicitly (uiapi_artifactrefs.go).
	Concepts []string `json:"concepts"`
	// Findings are the artifact lint findings on this artifact's files
	// (D316), so its health shows beside it.
	Findings []uiArtifactFinding `json:"findings,omitempty"`
}

type uiArtifactFinding struct {
	Path     string `json:"path"`
	Check    string `json:"check"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// uiArtifactFindingChecks are the lint checks the Artifacts panel reports
// (D316): the ones about what the KB ships, not about its concepts.
var uiArtifactFindingChecks = map[string]bool{
	"skill_invalid": true, "skill_warning": true, "legacy_tool_name": true,
	"skill_broken_ref": true, "skill_git_command": true, "hook_invalid": true,
	"junk_file": true, "junk_asset": true, "missing_instructions": true,
	"sops_format_mismatch": true, "sops_missing_file": true, "cross_kb_path": true,
	"skill_missing_perimeter": true,
}

// artifactFindings returns the KB's artifact findings, from the whole-KB lint
// cache when the server has one. A finding on a concept (a sops pipeline in a
// concept body) is not an artifact's.
func artifactFindings(srv *Server) ([]lint.Finding, error) {
	var all []lint.Finding
	var err error
	if srv.conformance != nil {
		all, err = srv.conformance.lintFindings(srv.kbRef)
	} else {
		all, err = lint.Run(srv.kbRef, "", false)
	}
	if err != nil {
		return nil, err
	}
	var out []lint.Finding
	for _, f := range all {
		// sops_* also fire on concept bodies: only the artifact ones count.
		if uiArtifactFindingChecks[f.Check] && (f.Artifact || !strings.HasPrefix(f.Check, "sops_")) {
			out = append(out, f)
		}
	}
	return out, nil
}

// attachFindings gives an artifact the findings on one of its files, or on
// its directory (a skill reported as a whole).
func attachFindings(a *uiArtifact, findings []lint.Finding) {
	for _, f := range findings {
		if f.Path == "" {
			continue
		}
		for _, file := range a.Files {
			if f.Path == file.Path || strings.HasPrefix(file.Path, f.Path+"/") {
				a.Findings = append(a.Findings, uiArtifactFinding{Path: f.Path, Check: f.Check, Severity: f.Severity, Message: f.Message})
				break
			}
		}
	}
}

func uiArtifactFrom(a kbArtifact, withContent bool, exists map[okf.ConceptID]struct{}) uiArtifact {
	out := uiArtifact{
		Kind:        a.Kind,
		Name:        a.Name,
		Description: a.description(),
		Clients:     []uiArtifactClient{},
		Files:       []uiArtifactFile{},
		Concepts:    artifactConceptRefs(a, exists),
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
	exists, err := conceptIDSet(srv.kbRef)
	if err != nil {
		writeUIInternal(w, "artifacts: concepts", err)
		return
	}
	findings, err := artifactFindings(srv)
	if err != nil {
		writeUIInternal(w, "artifacts: lint", err)
		return
	}
	list := make([]uiArtifact, 0, len(catalog.Artifacts))
	counts := map[string]int{}
	for _, a := range catalog.Artifacts {
		ua := uiArtifactFrom(a, false, exists)
		attachFindings(&ua, findings)
		list = append(list, ua)
		counts[a.Kind]++
	}
	issues := catalog.Issues
	if issues == nil {
		issues = []string{}
	}
	// Every artifact finding is counted, including the ones no listed
	// artifact owns (a skill left out, a junk file, no instructions.md).
	findingCounts, severityCounts := map[string]int{}, map[string]int{}
	for _, f := range findings {
		findingCounts[f.Check]++
		severityCounts[f.Severity]++
	}
	writeUIJSON(w, http.StatusOK, map[string]interface{}{
		"artifacts": list, "counts": counts, "issues": issues,
		"finding_counts": findingCounts, "finding_severities": severityCounts,
	})
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
	exists, err := conceptIDSet(srv.kbRef)
	if err != nil {
		writeUIInternal(w, "artifact: concepts", err)
		return
	}
	for _, a := range catalog.Artifacts {
		if a.Kind == kind && a.Name == name {
			ua := uiArtifactFrom(a, true, exists)
			findings, err := artifactFindings(srv)
			if err != nil {
				writeUIInternal(w, "artifact: lint", err)
				return
			}
			attachFindings(&ua, findings)
			writeUIJSON(w, http.StatusOK, ua)
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
