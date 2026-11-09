package kb

import (
	"strings"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// SourceIngestStatuses is the vocabulary of a Source's ingest_status.
var SourceIngestStatuses = []string{"pending", "ingested", "skipped"}

// SourceShapes is the vocabulary of a Source's optional source_shape (D1201);
// absent reads as "document". It sits beside the free-string source_kind, which
// says what the thing is. "system" is a live read-only system observed through
// snapshots.
var SourceShapes = []string{"document", "collection", "feed", "system"}

// SourceTemplate is the text of templates/source.md, the template
// source_register writes into a ledger journal that has none (D1072). It is
// composed from the server's own shape of a Source, so a change to the
// vocabulary changes the template. timestamp is optional because
// source_register omits it when the caller gives none; the body names no H2 and
// sections are open, because the distillation is free.
func SourceTemplate() string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("type: " + SourceType + "\n")
	b.WriteString("title: \"{{title}}\"\n")
	b.WriteString(TemplateMetaDescription + ": One primary source the knowledge base has absorbed, with its ingest status\n")
	b.WriteString(TemplateMetaRequiredFields + ": [source_kind, ingest_status]\n")
	b.WriteString(TemplateMetaOptionalFields + ": [locator, sha256, timestamp, ingested_at, source_shape, connector, recipe, feed_cursor, feed_checked_at, feed_interval, feed_include, feed_error]\n")
	b.WriteString(TemplateMetaFieldValues + "ingest_status: [" + strings.Join(SourceIngestStatuses, ", ") + "]\n")
	b.WriteString(TemplateMetaFieldValues + "source_shape: [" + strings.Join(SourceShapes, ", ") + "]\n")
	b.WriteString(TemplateMetaOpenSections + ": true\n")
	b.WriteString("---\n# {{title}}\n")
	return b.String()
}

// ProvenanceEntries returns the trimmed, non-empty entries of a concept's
// provenance frontmatter. A bare string is read as a one-element list. An entry
// that is the ID of an existing Source is a source citation (D278); the caller
// decides that, this only parses.
func ProvenanceEntries(fm *okf.Frontmatter) []string {
	if fm == nil {
		return nil
	}
	v, ok := fm.Get("provenance")
	if !ok {
		return nil
	}
	var raw []string
	switch t := v.(type) {
	case string:
		raw = []string{t}
	case []string:
		raw = t
	}
	var out []string
	for _, e := range raw {
		if e = strings.TrimSpace(e); e != "" {
			out = append(out, e)
		}
	}
	return out
}

// SourceCitations returns, for every concept in content, the provenance entries
// it lists, keyed by the cited ID: cited[sourceID] = citing concept IDs. Whether
// a key is a Source is for the caller to decide.
func SourceCitations(all map[okf.ConceptID]string) map[string][]string {
	out := map[string][]string{}
	for id, content := range all {
		fmRaw, _, _ := okf.SplitFrontmatter(content)
		fm, err := okf.ParseFrontmatter(fmRaw)
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, e := range ProvenanceEntries(fm) {
			if !seen[e] && e != string(id) {
				seen[e] = true
				out[e] = append(out[e], string(id))
			}
		}
	}
	return out
}
