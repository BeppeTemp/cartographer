package mcpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/lint"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// The source ledger (D278): a Source is a concept of the conventional type
// "Source" recording one primary source the KB has absorbed. The server never
// fetches, copies or transforms a source; it only keeps the bookkeeping.

const (
	defaultSourceMap   = "sources"
	defaultSourceLimit = 50
	maxSourceLimit     = 500
	maxSourceSlugLen   = 60
)

var (
	sha256Re     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)
)

func validSourceStatus(s string) bool {
	for _, v := range kb.SourceIngestStatuses {
		if s == v {
			return true
		}
	}
	return false
}

// sourceSlug derives a kebab-case slug of at most maxSourceSlugLen characters.
func sourceSlug(title string) string {
	s := strings.Trim(slugNonAlnum.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if len(s) > maxSourceSlugLen {
		s = strings.Trim(s[:maxSourceSlugLen], "-")
	}
	if s == "" {
		s = "source"
	}
	return s
}

// parseSourceTimestamp accepts an RFC3339 date-time or a plain date.
func parseSourceTimestamp(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Parse("2006-01-02", s)
}

func fmString(fm *okf.Frontmatter, key string) string {
	v, _ := fm.Get(key)
	s, _ := v.(string)
	return s
}

func toolSourceRegister(k *kb.KB) Tool {
	return Tool{
		Name:        "source_register",
		Description: "Records a primary source in the ledger BEFORE ingesting it: a Source concept in a journal (default 'sources', created with map_create kind journal). Dedupes by sha256, else locator: a duplicate returns {id, duplicate: true, ingest_status} and writes nothing. Never fetches the source; body is your distillation. Cite its ID in provenance, then set ingest_status via concept_patch.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"required": ["title", "source_kind"],
			"properties": {
				"title": {"type": "string"},
				"source_kind": {"type": "string"},
				"locator": {"type": "string", "description": "URL or {{path:<key>}}, never a raw path"},
				"sha256": {"type": "string", "description": "Lowercase hex SHA-256 of the original bytes"},
				"timestamp": {"type": "string"},
				"body": {"type": "string", "description": "Distillation: facts, decisions, open questions"},
				"map": {"type": "string", "description": "Ledger journal; default 'sources'"},
				"ingest_status": {"type": "string", "enum": ["pending", "ingested", "skipped"]}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var p struct {
				Title        string `json:"title"`
				SourceKind   string `json:"source_kind"`
				Locator      string `json:"locator"`
				SHA256       string `json:"sha256"`
				Timestamp    string `json:"timestamp"`
				Body         string `json:"body"`
				Map          string `json:"map"`
				IngestStatus string `json:"ingest_status"`
			}
			if err := json.Unmarshal(args, &p); err != nil {
				return errorResult("invalid params: " + err.Error()), nil
			}
			p.Title, p.SourceKind = strings.TrimSpace(p.Title), strings.TrimSpace(p.SourceKind)
			p.Locator = strings.TrimSpace(p.Locator)
			if p.Title == "" || p.SourceKind == "" {
				return errorResult("source_register: 'title' and 'source_kind' are required"), nil
			}
			if p.Map == "" {
				p.Map = defaultSourceMap
			}
			if p.IngestStatus == "" {
				p.IngestStatus = "pending"
			}
			if !validSourceStatus(p.IngestStatus) {
				return errorResult(fmt.Sprintf("source_register: ingest_status %q must be one of %s", p.IngestStatus, strings.Join(kb.SourceIngestStatuses, ", "))), nil
			}
			if p.SHA256 != "" && !sha256Re.MatchString(p.SHA256) {
				return errorResult("source_register: sha256 must be 64 lowercase hex characters"), nil
			}
			if bad := lint.FirstMachinePath(p.Locator); bad != "" {
				return errorResult(fmt.Sprintf("source_register: locator %q is a client-local path — use a URL or a {{path:<key>}} placeholder (D75)", bad)), nil
			}
			when := time.Now().UTC()
			if p.Timestamp != "" {
				t, err := parseSourceTimestamp(p.Timestamp)
				if err != nil {
					return errorResult(fmt.Sprintf("source_register: timestamp %q is neither an RFC3339 date-time nor a YYYY-MM-DD date", p.Timestamp)), nil
				}
				when = t.UTC()
			}

			meta, err := k.ReadArchiveMeta(p.Map)
			hint := fmt.Sprintf("source_register: map %q does not exist or is not readable: create it first with map_create(name: %q, kind: \"journal\")", p.Map, p.Map)
			if err != nil {
				return errorResult(hint), nil
			}
			if kind := fmString(meta, "kind"); kind != "journal" {
				return errorResult(fmt.Sprintf("source_register: %q is a %s, not a journal: the ledger lives in a journal (map_create(name: %q, kind: \"journal\"))", p.Map, kind, defaultSourceMap)), nil
			}

			// Dedup: sha256 is authoritative when both sides have it, otherwise
			// an exact locator match.
			var dupID, dupStatus string
			err = k.WalkConcepts(func(id okf.ConceptID, content string) error {
				if dupID != "" {
					return nil
				}
				fmRaw, _, _ := okf.SplitFrontmatter(content)
				fm, perr := okf.ParseFrontmatter(fmRaw)
				if perr != nil || fm.Type() != kb.SourceType {
					return nil
				}
				sha, loc := fmString(fm, "sha256"), fmString(fm, "locator")
				match := false
				if p.SHA256 != "" && sha != "" {
					match = sha == p.SHA256
				} else if p.Locator != "" && loc != "" {
					match = loc == p.Locator
				}
				if match {
					dupID = string(id)
					dupStatus = fmString(fm, "ingest_status")
				}
				return nil
			})
			if err != nil {
				return errorResult(fmt.Sprintf("source_register: walk: %v", err)), nil
			}
			if dupID != "" {
				out, _ := json.Marshal(map[string]interface{}{"id": dupID, "duplicate": true, "ingest_status": dupStatus})
				return textResult(string(out)), nil
			}

			base := p.Map + "/" + when.Format("2006-01-02") + "-" + sourceSlug(p.Title)
			id := base
			for n := 2; ; n++ {
				if _, rerr := k.ReadConcept(okf.ConceptID(id)); rerr != nil {
					if errors.Is(rerr, okf.ErrNotFound) {
						break
					}
					return errorResult(fmt.Sprintf("source_register: %v", rerr)), nil
				}
				id = fmt.Sprintf("%s-%d", base, n)
			}

			fm, _ := okf.ParseFrontmatter("type: " + kb.SourceType)
			fm.Set("title", p.Title)
			fm.Set("source_kind", p.SourceKind)
			if p.Locator != "" {
				fm.Set("locator", p.Locator)
			}
			if p.SHA256 != "" {
				fm.Set("sha256", p.SHA256)
			}
			if p.Timestamp != "" {
				fm.Set("timestamp", p.Timestamp)
			}
			fm.Set("ingest_status", p.IngestStatus)
			if p.IngestStatus == "ingested" {
				fm.Set("ingested_at", time.Now().UTC().Format(time.RFC3339))
			}
			body := p.Body
			if strings.TrimSpace(body) == "" {
				body = "# " + p.Title + "\n"
			}
			if _, err := k.WriteConcept(okf.ConceptID(id), fm, body, ""); err != nil {
				return errorResult(fmt.Sprintf("source_register %q: %v", id, err)), nil
			}
			_ = k.AppendLog("source_register: "+id, time.Now())
			out, _ := json.Marshal(map[string]interface{}{"id": id, "duplicate": false})
			return textResult(string(out)), nil
		},
	}
}

func toolSourceList(k *kb.KB) Tool {
	return Tool{
		Name:        "source_list",
		Description: "Source ledger (Source concepts), oldest first: {id, title, source_kind, ingest_status, timestamp?, cited_by (concepts citing it in provenance)}. status defaults to pending ('*' = all). Read-only.",
		ReadOnly:    true,
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"status": {"type": "string", "description": "pending (default), ingested, skipped or *"},
				"scope": {"type": "string"},
				"limit": {"type": "integer"}
			}
		}`),
		Handler: func(ctx requestContext, args json.RawMessage) (ToolResult, error) {
			var p struct {
				Status string `json:"status"`
				Scope  string `json:"scope"`
				Limit  int    `json:"limit"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &p); err != nil {
					return errorResult("invalid params: " + err.Error()), nil
				}
			}
			if p.Status == "" {
				p.Status = "pending"
			}
			if p.Status != "*" && !validSourceStatus(p.Status) {
				return errorResult(fmt.Sprintf("source_list: status %q must be one of %s or '*'", p.Status, strings.Join(kb.SourceIngestStatuses, ", "))), nil
			}
			if p.Limit <= 0 {
				p.Limit = defaultSourceLimit
			}
			if p.Limit > maxSourceLimit {
				p.Limit = maxSourceLimit
			}

			type entry struct {
				ID           string `json:"id"`
				Title        string `json:"title,omitempty"`
				SourceKind   string `json:"source_kind,omitempty"`
				IngestStatus string `json:"ingest_status"`
				Timestamp    string `json:"timestamp,omitempty"`
				CitedBy      int    `json:"cited_by"`
			}
			var entries []entry
			cited := map[string]int{}
			err := k.WalkConcepts(func(id okf.ConceptID, content string) error {
				fmRaw, _, _ := okf.SplitFrontmatter(content)
				fm, perr := okf.ParseFrontmatter(fmRaw)
				if perr != nil {
					return nil
				}
				sid := string(id)
				if refs := kb.ProvenanceEntries(fm); len(refs) > 0 && Visible(ctx, k, sid) {
					seen := map[string]bool{}
					for _, r := range refs {
						if r != sid && !seen[r] {
							seen[r] = true
							cited[r]++
						}
					}
				}
				if fm.Type() != kb.SourceType || !strings.HasPrefix(sid, p.Scope) || !Visible(ctx, k, sid) {
					return nil
				}
				st := fmString(fm, "ingest_status")
				if st == "" {
					st = "pending"
				}
				if p.Status != "*" && st != p.Status {
					return nil
				}
				entries = append(entries, entry{ID: sid, Title: fmString(fm, "title"), SourceKind: fmString(fm, "source_kind"),
					IngestStatus: st, Timestamp: fmString(fm, "timestamp")})
				return nil
			})
			if err != nil {
				return errorResult(fmt.Sprintf("source_list: walk: %v", err)), nil
			}
			sort.Slice(entries, func(i, j int) bool {
				if entries[i].Timestamp != entries[j].Timestamp {
					return entries[i].Timestamp < entries[j].Timestamp
				}
				return entries[i].ID < entries[j].ID
			})
			if len(entries) > p.Limit {
				entries = entries[:p.Limit]
			}
			for i := range entries {
				entries[i].CitedBy = cited[entries[i].ID]
			}
			if entries == nil {
				entries = []entry{}
			}
			out, _ := json.MarshalIndent(entries, "", "  ")
			return textResult(string(out)), nil
		},
	}
}

// sourceCounts returns the kb_status `sources` block, or nil when the KB has no
// Source concept.
func sourceCounts(k *kb.KB) map[string]int {
	counts := map[string]int{"pending": 0, "ingested": 0, "skipped": 0}
	n := 0
	_ = k.WalkConcepts(func(_ okf.ConceptID, content string) error {
		fmRaw, _, _ := okf.SplitFrontmatter(content)
		fm, err := okf.ParseFrontmatter(fmRaw)
		if err != nil || fm.Type() != kb.SourceType {
			return nil
		}
		n++
		st := fmString(fm, "ingest_status")
		if st == "" {
			st = "pending"
		}
		if _, ok := counts[st]; ok {
			counts[st]++
		}
		return nil
	})
	if n == 0 {
		return nil
	}
	return counts
}

// deleteBlockers is what concept_delete treats as "still linked to id": the
// visible inbound linkers (D271) plus, when id is a Source, the visible
// concepts that cite it through provenance (D278).
func deleteBlockers(ctx requestContext, k *kb.KB, id string) ([]string, error) {
	linkers, err := visibleInboundLinkers(ctx, k, id)
	if err != nil {
		return nil, err
	}
	data, rerr := k.ReadConcept(okf.ConceptID(id))
	if rerr != nil {
		return linkers, nil
	}
	if fm, perr := okf.ParseFrontmatter(data.FrontmatterRaw); perr != nil || fm.Type() != kb.SourceType {
		return linkers, nil
	}
	have := map[string]bool{}
	for _, l := range linkers {
		have[l] = true
	}
	err = k.WalkConcepts(func(cid okf.ConceptID, content string) error {
		s := string(cid)
		if have[s] || s == id || strings.HasPrefix(s, id+"/") || !strings.Contains(content, id) {
			return nil
		}
		fmRaw, _, _ := okf.SplitFrontmatter(content)
		fm, perr := okf.ParseFrontmatter(fmRaw)
		if perr != nil {
			return nil
		}
		for _, e := range kb.ProvenanceEntries(fm) {
			if e == id && Visible(ctx, k, s) {
				have[s] = true
				linkers = append(linkers, s)
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(linkers)
	return linkers, nil
}
