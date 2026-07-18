package okf

import (
	"strings"
	"testing"
)

// --- ContentHash ---

func TestContentHash_Determinismo(t *testing.T) {
	content := "---\ntype: Runbook\ntitle: Test\n---\n# Corpo\nContenuto."
	h1 := ContentHash(content)
	h2 := ContentHash(content)
	if h1 != h2 {
		t.Fatalf("ContentHash not deterministic: %s != %s", h1, h2)
	}
}

func TestContentHash_CRLFequalsLF(t *testing.T) {
	lf := "---\ntype: Runbook\n---\n# Corpo\nContenuto."
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")
	if ContentHash(lf) != ContentHash(crlf) {
		t.Fatal("CRLF and LF must produce the same hash")
	}
}

func TestContentHash_TrailingWhitespace(t *testing.T) {
	base := "---\ntype: Runbook\n---\n# Corpo\nContenuto."
	withSpaces := "---\ntype: Runbook   \n---\n# Corpo  \nContenuto.   "
	if ContentHash(base) != ContentHash(withSpaces) {
		t.Fatal("trailing whitespace must be ignored in hash")
	}
}

func TestContentHash_TimestampEscluso(t *testing.T) {
	a := "---\ntype: Runbook\ntimestamp: 2026-01-01T00:00:00Z\n---\n# Corpo\nContenuto."
	b := "---\ntype: Runbook\ntimestamp: 2026-06-25T10:00:00Z\n---\n# Corpo\nContenuto."
	if ContentHash(a) != ContentHash(b) {
		t.Fatal("different timestamps must produce the same hash")
	}
}

func TestContentHash_TrailingNewlines(t *testing.T) {
	a := "---\ntype: Runbook\n---\n# Corpo\nContenuto."
	b := "---\ntype: Runbook\n---\n# Corpo\nContenuto.\n\n\n"
	if ContentHash(a) != ContentHash(b) {
		t.Fatal("trailing empty lines must be ignored")
	}
}

func TestContentHash_ContentoDiverso(t *testing.T) {
	a := "---\ntype: Runbook\n---\n# Corpo\nContenuto A."
	b := "---\ntype: Runbook\n---\n# Corpo\nContenuto B."
	if ContentHash(a) == ContentHash(b) {
		t.Fatal("different contents must have different hashes")
	}
}

func TestContentHash_OrdinamentoCanonicoChiavi(t *testing.T) {
	// Same frontmatter with keys in different order must produce the same hash.
	a := "---\ntype: Runbook\ntitle: Test\n---\n# Corpo\nContenuto."
	b := "---\ntitle: Test\ntype: Runbook\n---\n# Corpo\nContenuto."
	if ContentHash(a) != ContentHash(b) {
		t.Fatal("frontmatter with different key order must produce the same hash")
	}
}

func TestContentHash_TimestampEsclusoConOrdinamentoCanico(t *testing.T) {
	// Timestamp excluded even when keys are out of order.
	a := "---\ntitle: Test\ntimestamp: 2026-01-01T00:00:00Z\ntype: Runbook\n---\n# Corpo\nContenuto."
	b := "---\ntype: Runbook\ntitle: Test\ntimestamp: 2026-06-25T10:00:00Z\n---\n# Corpo\nContenuto."
	if ContentHash(a) != ContentHash(b) {
		t.Fatal("different timestamps and key order must produce the same hash")
	}
}

// --- SectionHashes ---

func TestSectionHashes_FullMatchContentHash(t *testing.T) {
	content := "---\ntype: Runbook\n---\n# Sezione Uno\nTesto uno.\n## Sotto\nSotto."
	hashes := SectionHashes(content)
	full, ok := hashes["_full"]
	if !ok {
		t.Fatal("SectionHashes: _full key missing")
	}
	if full != ContentHash(content) {
		t.Fatalf("SectionHashes[_full] = %s, ContentHash = %s", full, ContentHash(content))
	}
}

func TestSectionHashes_SezioniPresenti(t *testing.T) {
	content := "---\ntype: Runbook\n---\n# Sezione Uno\nTesto uno.\n# Sezione Due\nTesto due."
	hashes := SectionHashes(content)

	if _, ok := hashes["Sezione Uno"]; !ok {
		t.Fatal("SectionHashes: section 'Sezione Uno' missing")
	}
	if _, ok := hashes["Sezione Due"]; !ok {
		t.Fatal("SectionHashes: section 'Sezione Due' missing")
	}
	if hashes["Sezione Uno"] == hashes["Sezione Due"] {
		t.Fatal("sections with different content must have different hashes")
	}
}

func TestSectionHashes_H2TerminaAlProssimoH1(t *testing.T) {
	content := "# Uno\nTesto uno.\n## Sotto\nContenuto sotto.\n# Due\nTesto due."
	hashes := SectionHashes(content)

	for _, name := range []string{"Uno", "Sotto", "Due"} {
		if _, ok := hashes[name]; !ok {
			t.Fatalf("SectionHashes: section %q missing", name)
		}
	}
	if hashes["Uno"] == hashes["Due"] {
		t.Fatal("different sections must not have the same hash")
	}
}

func TestSectionHashes_SenzaSezioni(t *testing.T) {
	content := "---\ntype: Runbook\n---\nSolo testo senza heading."
	hashes := SectionHashes(content)

	if len(hashes) != 1 {
		t.Fatalf("expected only _full key, found %d keys", len(hashes))
	}
	if _, ok := hashes["_full"]; !ok {
		t.Fatal("SectionHashes: _full key missing")
	}
}

// --- PathToID / IDToPath ---

func TestPathToID_RoundTrip(t *testing.T) {
	cases := []string{
		"manutenzione/rotazione-certificati/runbook.md",
		"servizi/keycloak.md",
		"index.md",
	}
	for _, p := range cases {
		id, err := PathToID(p)
		if err != nil {
			t.Fatalf("PathToID(%q): %v", p, err)
		}
		got := IDToPath(id)
		if got != p {
			t.Fatalf("round-trip failed: PathToID(%q) -> IDToPath -> %q", p, got)
		}
	}
}

func TestPathToID_ErrInvalidPath(t *testing.T) {
	invalid := []string{
		"../escape",
		"/absolute",
		"Maiuscola/file.md",
	}
	for _, p := range invalid {
		_, err := PathToID(p)
		if err == nil {
			t.Fatalf("PathToID(%q): expected error, got nil", p)
		}
	}
}

// --- ExtractSection ---

func TestExtractSection_HeadingTrovato(t *testing.T) {
	body := "# Schema\nContenuto schema.\n## Sottoheading\nSotto.\n# Citations\nFonti."
	got, ok := ExtractSection(body, "# Schema")
	if !ok {
		t.Fatal("ExtractSection: heading not found")
	}
	if !strings.Contains(got, "Contenuto schema.") {
		t.Fatalf("ExtractSection: unexpected content: %q", got)
	}
	if strings.Contains(got, "Fonti.") {
		t.Fatal("ExtractSection: included content beyond the next same-level heading")
	}
}

func TestExtractSection_HeadingAssente(t *testing.T) {
	body := "# Schema\nContenuto."
	_, ok := ExtractSection(body, "# NonEsiste")
	if ok {
		t.Fatal("ExtractSection: expected not found")
	}
}

func TestExtractSection_SottoheadingIncluso(t *testing.T) {
	body := "# Schema\nTesto.\n## Dettagli\nDettaglio.\n# Altro\nFine."
	got, ok := ExtractSection(body, "# Schema")
	if !ok {
		t.Fatal("ExtractSection: heading not found")
	}
	if !strings.Contains(got, "Dettagli") {
		t.Fatal("ExtractSection: must include subheading")
	}
}

// --- ListHeadings ---

func TestListHeadings_LivelliETitoli(t *testing.T) {
	body := "# Schema\nTesto.\n## Dettagli\nDettaglio.\n# Altro\nFine."
	got := ListHeadings(body)
	if len(got) != 3 {
		t.Fatalf("ListHeadings: expected 3 headings, got %d: %+v", len(got), got)
	}
	if got[0].Level != 1 || got[0].Title != "Schema" {
		t.Errorf("ListHeadings[0]: got %+v", got[0])
	}
	if got[1].Level != 2 || got[1].Title != "Dettagli" {
		t.Errorf("ListHeadings[1]: got %+v", got[1])
	}
	if got[2].Level != 1 || got[2].Title != "Altro" {
		t.Errorf("ListHeadings[2]: got %+v", got[2])
	}
}

func TestListHeadings_Dimensioni(t *testing.T) {
	body := "# Schema\nTesto.\n## Dettagli\nDettaglio.\n# Altro\nFine."
	got := ListHeadings(body)

	// The "# Schema" section stops before the next same-level heading
	// ("# Altro"), including the "## Dettagli" sub-heading.
	schemaSection, ok := ExtractSection(body, "# Schema")
	if !ok {
		t.Fatal("ExtractSection: setup failed")
	}
	if got[0].Bytes != len(schemaSection) {
		t.Errorf("ListHeadings[0].Bytes: got %d, want %d (consistent with ExtractSection)", got[0].Bytes, len(schemaSection))
	}

	dettagliSection, ok := ExtractSection(body, "## Dettagli")
	if !ok {
		t.Fatal("ExtractSection: setup failed")
	}
	if got[1].Bytes != len(dettagliSection) {
		t.Errorf("ListHeadings[1].Bytes: got %d, want %d", got[1].Bytes, len(dettagliSection))
	}

	altroSection, ok := ExtractSection(body, "# Altro")
	if !ok {
		t.Fatal("ExtractSection: setup failed")
	}
	if got[2].Bytes != len(altroSection) {
		t.Errorf("ListHeadings[2].Bytes: got %d, want %d", got[2].Bytes, len(altroSection))
	}
}

func TestListHeadings_BodySenzaHeading(t *testing.T) {
	got := ListHeadings("Solo testo, nessun heading.\nSeconda riga.")
	if len(got) != 0 {
		t.Fatalf("ListHeadings: expected 0 headings, got %d: %+v", len(got), got)
	}
}

// --- SplitFrontmatter ---

func TestSplitFrontmatter_ConFrontmatter(t *testing.T) {
	content := "---\ntype: Runbook\ntitle: Test\n---\n# Corpo\nContenuto."
	fm, body, ok := SplitFrontmatter(content)
	if !ok {
		t.Fatal("SplitFrontmatter: expected hasFrontmatter=true")
	}
	if !strings.Contains(fm, "type: Runbook") {
		t.Fatalf("unexpected frontmatter: %q", fm)
	}
	if !strings.Contains(body, "# Corpo") {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestSplitFrontmatter_SenzaFrontmatter(t *testing.T) {
	content := "# Solo corpo\nTesto."
	_, body, ok := SplitFrontmatter(content)
	if ok {
		t.Fatal("SplitFrontmatter: expected hasFrontmatter=false")
	}
	if body != content {
		t.Fatalf("body must be the original content: %q", body)
	}
}

// --- IsReserved ---

func TestIsReserved(t *testing.T) {
	if !IsReserved("index.md") {
		t.Fatal("index.md must be reserved")
	}
	if !IsReserved("log.md") {
		t.Fatal("log.md must be reserved")
	}
	if !IsReserved("_archive.md") {
		t.Fatal("_archive.md must be reserved")
	}
	if IsReserved("concept.md") {
		t.Fatal("concept.md must not be reserved")
	}
}
