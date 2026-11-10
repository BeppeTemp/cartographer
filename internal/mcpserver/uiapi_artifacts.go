package mcpserver

import (
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
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
	// Mode (D1656) is what this client does with the artifact: native,
	// degraded (an agent installed as a skill), skipped (Reason says why) or
	// unverified. Empty on a kind row, which has no artifact to judge, except
	// "degraded" for the agents of a client without subagents.
	Mode   string `json:"mode,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// uiKindInfo is one kind of artifact a KB can hold, as provisioning.Kinds
// describes it (D864): the Atlas lists every kind, with the path its files go
// in and the page that documents it, even when the KB has none.
type uiKindInfo struct {
	Kind   string `json:"kind"`
	Title  string `json:"title"`
	KBPath string `json:"kb_path"`
	// Doc is a repository path with an anchor ("docs/sync.md#agents-and-hooks");
	// the UI resolves it against the repository's blob URL, not this server.
	Doc string `json:"doc"`
	// KBOnly: no client receives it (templates, secrets).
	KBOnly bool `json:"kb_only"`
}

func uiKindInfoFrom(k contract.KindInfo) uiKindInfo {
	return uiKindInfo{Kind: k.Kind, Title: k.Title, KBPath: k.KBPath, Doc: k.Doc, KBOnly: k.KBOnly}
}

// uiArtifactKind is a row of the artifacts list's "kinds": the descriptor, how
// many artifacts of the kind the KB ships, and the clients that receive one.
type uiArtifactKind struct {
	uiKindInfo
	Count   int                `json:"count"`
	Clients []uiArtifactClient `json:"clients"`
}

// uiArtifactKindRows lists, in reading order, every kind of the artifacts panel
// with its count. Secrets are left out: they have their own tab and answer
// from /secrets.
func uiArtifactKindRows(counts map[string]int) []uiArtifactKind {
	rows := []uiArtifactKind{}
	for _, k := range contract.Kinds() {
		if k.Kind == "secret" {
			continue
		}
		rows = append(rows, uiArtifactKind{
			uiKindInfo: uiKindInfoFrom(k),
			Count:      counts[k.Kind],
			Clients:    uiClientsFor(k.Kind, "", nil),
		})
	}
	return rows
}

// uiClientsFor names the clients that receive an artifact of this kind, in
// registry order, with the display names the Atlas shows. An empty name asks
// for the kind as a whole. modes are the client modes of the artifact
// (clientModesFor), nil for a kind row or an artifact the resolver does not
// judge (the curated instructions): those list the providers whose destination
// cell is supported, as provisioning.Destinations does, plus the clients that
// get agents as skills.
func uiClientsFor(kind, name string, modes []contract.ClientMode) []uiArtifactClient {
	names := map[configurator.Provider]string{}
	for _, d := range configurator.Providers() {
		names[d.Provider] = d.DisplayName
	}
	if modes == nil {
		if name == "" {
			modes = contract.KindClientModes(kind)
		} else {
			for _, p := range contract.Destinations(kind, name) {
				modes = append(modes, contract.ClientMode{Provider: p})
			}
		}
	}
	out := []uiArtifactClient{}
	for _, m := range modes {
		out = append(out, uiArtifactClient{ID: string(m.Provider), Name: names[m.Provider], Mode: m.Mode, Reason: m.Reason})
	}
	return out
}

// modeJudged: the artifact takes part in clientModesFor.
func modeJudged(a kbArtifact) bool {
	return a.Manifest != nil && (a.Kind == "skill" || a.Kind == "agent" || a.Kind == "hook" || a.Kind == "mcp")
}

// clientModesFor judges every artifact the KB ships, once per request, with the
// resolver a sync runs (provisioning.ClientModes): the panel cannot say
// something a sync would not do. Only artifacts with a manifest entry take
// part; a nil map means none is judged.
func clientModesFor(catalog kbArtifactCatalog) map[string][]contract.ClientMode {
	var arts []contract.Artifact
	for _, a := range catalog.Artifacts {
		if !modeJudged(a) {
			continue
		}
		pa := *a.Manifest
		// The catalog lists paths in the KB's layout ("skills/<name>/SKILL.md");
		// the resolver reads them relative to the artifact, as a sync's manifest
		// carries them: with the prefix it finds no `requires` and no body.
		prefix := artifactFilePrefix(a.Kind, a.Name)
		pa.Files = nil
		for _, f := range a.Files {
			f.Path = strings.TrimPrefix(f.Path, prefix)
			pa.Files = append(pa.Files, f)
		}
		arts = append(arts, pa)
	}
	if len(arts) == 0 {
		return nil
	}
	return contract.ClientModes(arts, contract.ProjectOptions{})
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
	// Usage (D326): when a client last activated the artifact, from the
	// reports in .cartographer/usage.json. All three are null for an artifact
	// nothing reported (and for kinds the scanner does not follow).
	LastUsed         *string `json:"last_used"`
	LastUsedProvider *string `json:"last_used_provider"`
	LastUsedDaysAgo  *int    `json:"last_used_days_ago"`
	// LastUsedCatalogOnly is true when the only signal is a client loading
	// the catalogue the artifact was listed in (Codex): available, not used.
	LastUsedCatalogOnly bool `json:"last_used_catalog_only,omitempty"`
}

type uiArtifactFinding struct {
	Path    string `json:"path"`
	Check   string `json:"check"`
	Message string `json:"message"`
}

// artifactFindings returns the KB's artifact findings (the registry's Panel
// checks, D316: what the KB ships, not its concepts), from the whole-KB lint
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
		if lint.ArtifactPanelCheck(f.Check) && (f.Artifact || !strings.HasPrefix(f.Check, "sops_")) {
			out = append(out, f)
		}
	}
	return out, nil
}

// artifactUsage reads the KB's usage reports once per request. A store that
// cannot be read is no data: the panel still answers.
func artifactUsage(k *kb.KB) map[string]kb.UsageSummary {
	entries, err := k.LoadUsage()
	if err != nil || len(entries) == 0 {
		return nil
	}
	return kb.SummarizeUsage(entries)
}

// attachUsage fills the Last used fields of a skill or agent.
func attachUsage(a *uiArtifact, usage map[string]kb.UsageSummary, now time.Time) {
	u, ok := usage[kb.UsageKey(a.Kind, a.Name)]
	if !ok || u.LastUsed.IsZero() {
		return
	}
	at := u.LastUsed.UTC().Format(time.RFC3339)
	days := int(now.Sub(u.LastUsed).Hours() / 24)
	if days < 0 {
		days = 0
	}
	provider := u.Provider
	a.LastUsed, a.LastUsedProvider, a.LastUsedDaysAgo = &at, &provider, &days
	a.LastUsedCatalogOnly = u.Count == 0
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
				a.Findings = append(a.Findings, uiArtifactFinding{Path: f.Path, Check: f.Check, Message: f.Message})
				break
			}
		}
	}
}

func uiArtifactFrom(a kbArtifact, withContent bool, exists map[okf.ConceptID]struct{}, modes map[string][]contract.ClientMode) uiArtifact {
	out := uiArtifact{
		Kind:        a.Kind,
		Name:        a.Name,
		Description: a.description(),
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
	var judged []contract.ClientMode
	if modes != nil && modeJudged(a) {
		// Judged and received by nobody is an empty list, not the matrix.
		judged = append([]contract.ClientMode{}, modes[a.Kind+"/"+a.Name]...)
	}
	out.Clients = uiClientsFor(a.Kind, a.Name, judged)
	for _, f := range a.Files {
		file := uiArtifactFile{Path: f.Path, SHA256: sha256Hex(f.Content), Size: len(f.Content), Executable: f.Executable}
		if f.Oversized > 0 {
			file.SHA256, file.Size = "", int(f.Oversized)
		}
		if withContent {
			switch {
			case f.Oversized > 0:
				file.Truncated = true
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
	catalog, err := listKBArtifacts(srv.kbRef, srv.kbArtifacts.signer)
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
	usage, now := artifactUsage(srv.kbRef), time.Now()
	modes := clientModesFor(catalog)
	for _, a := range catalog.Artifacts {
		ua := uiArtifactFrom(a, false, exists, modes)
		attachFindings(&ua, findings)
		attachUsage(&ua, usage, now)
		list = append(list, ua)
		counts[a.Kind]++
	}
	issues := catalog.Issues
	if issues == nil {
		issues = []string{}
	}
	// Every artifact finding is counted, including the ones no listed
	// artifact owns (a skill left out, a junk file, no instructions.md).
	findingCounts := lint.CountsByCheck(findings)
	writeUIJSON(w, http.StatusOK, map[string]interface{}{
		"artifacts": list, "counts": counts, "kinds": uiArtifactKindRows(counts), "issues": issues,
		"finding_counts": findingCounts,
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
	catalog, err := listKBArtifacts(srv.kbRef, srv.kbArtifacts.signer)
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
			ua := uiArtifactFrom(a, true, exists, clientModesFor(catalog))
			findings, err := artifactFindings(srv)
			if err != nil {
				writeUIInternal(w, "artifact: lint", err)
				return
			}
			attachFindings(&ua, findings)
			attachUsage(&ua, artifactUsage(srv.kbRef), time.Now())
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
