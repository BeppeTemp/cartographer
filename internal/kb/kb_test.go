package kb

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

func tempKB(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "wiki-kb-test-*")
	if err != nil {
		t.Fatalf("tempKB: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// --- Init ---

func TestInit_CreaScheletro(t *testing.T) {
	dir := tempKB(t)
	_, err := Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, rel := range []string{"data/index.md", "data/log.md"} {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); os.IsNotExist(err) {
			t.Errorf("Init: missing %s", rel)
		}
	}
}

func TestInit_CreaDirAgentsHooks(t *testing.T) {
	dir := tempKB(t)
	_, err := Init(dir)
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, rel := range []string{"skills", "services", "agents", "hooks"} {
		p := filepath.Join(dir, rel)
		fi, err := os.Stat(p)
		if err != nil {
			t.Errorf("Init: missing dir %s: %v", rel, err)
			continue
		}
		if !fi.IsDir() {
			t.Errorf("Init: %s should be a directory", rel)
		}
	}
}

func TestInit_NoAgentsMdNoGitignore(t *testing.T) {
	dir := tempKB(t)
	if _, err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	for _, rel := range []string{"AGENTS.md", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); !os.IsNotExist(err) {
			t.Errorf("Init: %s should not be generated (got err=%v)", rel, err)
		}
	}
}

func TestInit_InfoExcludeHasCartographerEntry(t *testing.T) {
	dir := tempKB(t)
	if _, err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude"))
	if err != nil {
		t.Fatalf("read .git/info/exclude: %v", err)
	}
	if !strings.Contains(string(data), ".cartographer/") {
		t.Errorf(".git/info/exclude missing .cartographer/ entry: %q", string(data))
	}
}

func TestInit_Idempotente(t *testing.T) {
	dir := tempKB(t)
	if _, err := Init(dir); err != nil {
		t.Fatalf("first Init: %v", err)
	}
	if _, err := Init(dir); err != nil {
		t.Fatalf("second Init: %v", err)
	}
}

func TestOpen_KBValida(t *testing.T) {
	dir := tempKB(t)
	if _, err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	kb, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if kb.Root == "" {
		t.Fatal("Open: Root empty")
	}
}

func TestOpen_KBNonValida(t *testing.T) {
	dir := tempKB(t)
	_, err := Open(dir)
	if err == nil {
		t.Fatal("Open on directory without index.md: expected error")
	}
}

func TestOpen_MigratesMissingInfoExclude(t *testing.T) {
	dir := tempKB(t)
	if _, err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	excludePath := filepath.Join(dir, ".git", "info", "exclude")
	// Simulate a pre-D62 KB: no .cartographer/ entry yet.
	if err := os.WriteFile(excludePath, []byte(""), 0o644); err != nil {
		t.Fatalf("reset exclude: %v", err)
	}
	if _, err := Open(dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("read exclude: %v", err)
	}
	if !strings.Contains(string(data), ".cartographer/") {
		t.Fatalf("Open did not self-migrate .git/info/exclude: %q", string(data))
	}
}

func TestOpen_InfoExcludeIdempotent(t *testing.T) {
	dir := tempKB(t)
	if _, err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	excludePath := filepath.Join(dir, ".git", "info", "exclude")
	before, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("read exclude: %v", err)
	}
	if _, err := Open(dir); err != nil {
		t.Fatalf("second Open: %v", err)
	}
	after, err := os.ReadFile(excludePath)
	if err != nil {
		t.Fatalf("read exclude after second Open: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("ensureInfoExclude not idempotent: before=%q after=%q", before, after)
	}
}

func TestEnsureInfoExclude_NoGitNoOp(t *testing.T) {
	dir := tempKB(t)
	// No .git/ at all (Init not called): must be a silent no-op.
	if err := ensureInfoExclude(dir, ".cartographer/"); err != nil {
		t.Fatalf("ensureInfoExclude on non-git dir: expected nil error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("ensureInfoExclude must not create .git: %v", err)
	}
}

func TestEnsureInfoExclude_GitfileWorktreeNoOp(t *testing.T) {
	dir := tempKB(t)
	// Simulate a worktree: .git is a file ("gitdir: ..."), not a directory.
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/x\n"), 0o644); err != nil {
		t.Fatalf("write .git file: %v", err)
	}
	if err := ensureInfoExclude(dir, ".cartographer/"); err != nil {
		t.Fatalf("ensureInfoExclude on gitfile worktree: expected nil error, got %v", err)
	}
	// .git remains the plain gitfile: no info/ subdirectory was created inside it.
	if fi, err := os.Stat(filepath.Join(dir, ".git")); err != nil || fi.IsDir() {
		t.Fatalf("ensureInfoExclude must not touch a gitfile worktree: stat=%v isDir=%v", err, err == nil && fi.IsDir())
	}
}

// --- ResolvePath ---

func TestResolvePath_EscapeRifiutato(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)
	_, err := kb.ResolvePath("../escape", false)
	if err == nil {
		t.Fatal("ResolvePath: expected error for ../escape")
	}
}

func TestResolvePath_AssolutoRifiutato(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)
	_, err := kb.ResolvePath("/etc/passwd", false)
	if err == nil {
		t.Fatal("ResolvePath: expected error for absolute path")
	}
}

func TestResolvePath_ServicesAnchoredAtRoot(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)
	// services/ is a sibling of data/ but must be resolvable for Service concepts.
	abs, err := kb.ResolvePath("services/keycloak.md", false)
	if err != nil {
		t.Fatalf("ResolvePath services/: %v", err)
	}
	want := filepath.Join(kb.Root, "services", "keycloak.md")
	if abs != want {
		t.Fatalf("ResolvePath services/: got %q, want %q", abs, want)
	}
}

// --- ReadConcept ---

func TestReadConcept_Hash(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	content := "---\ntype: Runbook\ntitle: Test\n---\n# Corpo\nContenuto."
	p := filepath.Join(kb.DataRoot(), "test-concept.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	data, err := kb.ReadConcept(okf.ConceptID("test-concept"))
	if err != nil {
		t.Fatalf("ReadConcept: %v", err)
	}
	if data.ContentHash == "" {
		t.Fatal("ReadConcept: ContentHash empty")
	}
	expected := okf.ContentHash(content)
	if data.ContentHash != expected {
		t.Fatalf("hash mismatch: %s != %s", data.ContentHash, expected)
	}
}

// --- ListArchives / ListExpanded ---

func TestListArchives(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	// Create two archives and a hidden dir (must be excluded).
	for _, d := range []string{"manutenzione", "servizi", ".nascosta"} {
		os.MkdirAll(filepath.Join(kb.DataRoot(), d), 0o755)
	}

	archives, err := kb.ListArchives()
	if err != nil {
		t.Fatalf("ListArchives: %v", err)
	}
	found := map[string]bool{}
	for _, a := range archives {
		found[a] = true
	}
	if !found["manutenzione"] || !found["servizi"] {
		t.Fatalf("ListArchives: expected archives not found: %v", archives)
	}
	if found[".nascosta"] {
		t.Fatal("ListArchives: hidden directory must not be included")
	}
	if found["raw"] {
		t.Fatal("ListArchives: raw/ must not be included")
	}
}

func TestListExpanded(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	os.MkdirAll(filepath.Join(kb.DataRoot(), "manutenzione", "rotazione-cert"), 0o755)
	os.MkdirAll(filepath.Join(kb.DataRoot(), "manutenzione", "patching"), 0o755)

	dossiers, err := kb.ListExpanded("manutenzione")
	if err != nil {
		t.Fatalf("ListExpanded: %v", err)
	}
	if len(dossiers) != 2 {
		t.Fatalf("ListExpanded: expected 2 dossiers, found %d: %v", len(dossiers), dossiers)
	}
}

func TestConceptCount_ArchivioFlat(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	os.MkdirAll(filepath.Join(kb.DataRoot(), "entities"), 0o755)
	os.WriteFile(filepath.Join(kb.DataRoot(), "entities", "concept-a.md"), []byte("---\ntype: Nota\n---\n"), 0o644)
	os.WriteFile(filepath.Join(kb.DataRoot(), "entities", "concept-b.md"), []byte("---\ntype: Nota\n---\n"), 0o644)
	os.WriteFile(filepath.Join(kb.DataRoot(), "entities", "index.md"), []byte("# index"), 0o644)
	os.WriteFile(filepath.Join(kb.DataRoot(), "entities", "log.md"), []byte("# log"), 0o644)

	count, err := kb.ConceptCount("entities")
	if err != nil {
		t.Fatalf("ConceptCount: %v", err)
	}
	if count != 2 {
		t.Fatalf("ConceptCount: expected 2 concepts (reserved files excluded), found %d", count)
	}
}

func TestConceptCount_ArchivioInesistente(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	if _, err := kb.ConceptCount("inesistente"); !errors.Is(err, okf.ErrNotFound) {
		t.Fatalf("ConceptCount: expected ErrNotFound, got %v", err)
	}
}

// --- WriteFileAtomic ---

func TestWriteFileAtomic(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	if err := kb.WriteFileAtomic("nuovo-concept.md", []byte("contenuto")); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(kb.DataRoot(), "nuovo-concept.md"))
	if err != nil {
		t.Fatalf("verify WriteFileAtomic: %v", err)
	}
	if string(data) != "contenuto" {
		t.Fatalf("unexpected content: %q", string(data))
	}
}

// --- AppendLog ---

func TestAppendLog_NewestOnTop(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

	if err := kb.AppendLog("prima voce", t1); err != nil {
		t.Fatalf("AppendLog 1: %v", err)
	}
	if err := kb.AppendLog("seconda voce", t2); err != nil {
		t.Fatalf("AppendLog 2: %v", err)
	}

	content, err := kb.ReadRaw("log.md")
	if err != nil {
		t.Fatalf("ReadRaw log.md: %v", err)
	}

	idxSeconda := indexOf(content, "seconda voce")
	idxPrima := indexOf(content, "prima voce")
	if idxSeconda == -1 || idxPrima == -1 {
		t.Fatalf("entries not found in log: %q", content)
	}
	if idxSeconda > idxPrima {
		t.Fatal("AppendLog: second entry must appear first (newest-on-top)")
	}
}

// --- LogTail ---

func TestLogTail_PathFiltersRootPrefixedEntries(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)

	if err := kb.AppendLog("voce root senza path", t1); err != nil {
		t.Fatalf("AppendLog 1: %v", err)
	}
	if err := kb.AppendLog("[manutenzione] voce per manutenzione", t2); err != nil {
		t.Fatalf("AppendLog 2: %v", err)
	}
	if err := kb.AppendLog("[manutenzione/certificati] voce annidata", t3); err != nil {
		t.Fatalf("AppendLog 3: %v", err)
	}

	got, err := kb.LogTail("manutenzione", 10)
	if err != nil {
		t.Fatalf("LogTail: %v", err)
	}
	if !strings.Contains(got, "[manutenzione] voce per manutenzione") {
		t.Errorf("LogTail(%q): missing own-prefix entry: %q", "manutenzione", got)
	}
	if strings.Contains(got, "voce root senza path") {
		t.Errorf("LogTail(%q): unprefixed root entry leaked in: %q", "manutenzione", got)
	}
	if strings.Contains(got, "voce annidata") {
		t.Errorf("LogTail(%q): entry from a different (nested) path leaked in: %q", "manutenzione", got)
	}
}

func TestLogTail_PathWithoutEntriesIsEmpty(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	if err := kb.AppendLog("voce root", time.Now()); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}

	got, err := kb.LogTail("nessuna-voce-qui", 10)
	if err != nil {
		t.Fatalf("LogTail: %v", err)
	}
	if got != "" {
		t.Errorf("LogTail on a path with no entries: expected empty string, got %q", got)
	}
}

func TestLogTail_PerDirectoryLogPlusRootPrefixed(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	if err := kb.AppendLog("[manutenzione] voce root", time.Now()); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}

	// A pre-existing per-directory log.md (not written by AppendLog, but
	// LogTail must still surface it) — entries here come before the root ones.
	manutenzioneDir := filepath.Join(kb.DataRoot(), "manutenzione")
	if err := os.MkdirAll(manutenzioneDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	dirLog := "# Log\n\n## 2026-01-01T00:00:00Z\n\nvoce solo per-directory\n\n"
	if err := os.WriteFile(filepath.Join(manutenzioneDir, "log.md"), []byte(dirLog), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got, err := kb.LogTail("manutenzione", 10)
	if err != nil {
		t.Fatalf("LogTail: %v", err)
	}
	idxDir := indexOf(got, "voce solo per-directory")
	idxRoot := indexOf(got, "voce root")
	if idxDir == -1 || idxRoot == -1 {
		t.Fatalf("LogTail: expected both per-directory and root-prefixed entries, got %q", got)
	}
	if idxDir > idxRoot {
		t.Fatal("LogTail: per-directory entries must come before root-filtered entries")
	}
}

func indexOf(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// --- WriteConcept ---

func TestWriteConcept_NuovoConcept(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	fm, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Test")
	hash, err := kb.WriteConcept(okf.ConceptID("mio-concept"), fm, "# Corpo\n\nContenuto.\n", "")
	if err != nil {
		t.Fatalf("WriteConcept: %v", err)
	}
	if hash == "" {
		t.Fatal("WriteConcept: hash empty")
	}

	content, err := kb.ReadRaw("mio-concept.md")
	if err != nil {
		t.Fatalf("ReadRaw after WriteConcept: %v", err)
	}
	expected := okf.ContentHash(content)
	if hash != expected {
		t.Fatalf("hash mismatch: %s != %s", hash, expected)
	}
	if !strings.Contains(content, "type: Runbook") {
		t.Fatalf("content does not contain type: Runbook: %q", content)
	}
}

func TestWriteConcept_AggiornamentoIfMatchCorretto(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	fm1, _ := okf.ParseFrontmatter("type: Runbook\ntitle: V1")
	hash1, err := kb.WriteConcept(okf.ConceptID("concept"), fm1, "# V1\n", "")
	if err != nil {
		t.Fatalf("first write: %v", err)
	}

	fm2, _ := okf.ParseFrontmatter("type: Runbook\ntitle: V2")
	hash2, err := kb.WriteConcept(okf.ConceptID("concept"), fm2, "# V2\n", hash1)
	if err != nil {
		t.Fatalf("update with correct ifMatch: %v", err)
	}
	if hash2 == hash1 {
		t.Fatal("hash must change after content update")
	}
}

func TestWriteConcept_StaleWrite(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	fm, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Test")
	if _, err := kb.WriteConcept(okf.ConceptID("concept"), fm, "# V1\n", ""); err != nil {
		t.Fatalf("first write: %v", err)
	}

	fm2, _ := okf.ParseFrontmatter("type: Runbook\ntitle: V2")
	_, err := kb.WriteConcept(okf.ConceptID("concept"), fm2, "# V2\n", "hash-sbagliato")
	if !errors.Is(err, okf.ErrStaleWrite) {
		t.Fatalf("expected ErrStaleWrite, got: %v", err)
	}
}

func TestWriteConcept_RifiutaFileRiservato(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	fm, _ := okf.ParseFrontmatter("type: Index\ntitle: Test")
	_, err := kb.WriteConcept(okf.ConceptID("index"), fm, "# Index\n", "")
	if err == nil {
		t.Fatal("expected error for reserved file index.md")
	}
}

func TestWriteConcept_RifiutaTypeVuoto(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	fm, _ := okf.ParseFrontmatter("title: Test")
	_, err := kb.WriteConcept(okf.ConceptID("concept"), fm, "# Test\n", "")
	if !errors.Is(err, okf.ErrInvalidConcept) {
		t.Fatalf("expected ErrInvalidConcept, got: %v", err)
	}
}

// --- WriteConcept: dossier stub + depth guard (D72 WP4) ---

func TestWriteConcept_StubDossierIndexSuDossierImplicito(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	if err := kb.CreateMap("entities", "Entities", "map", nil, ""); err != nil {
		t.Fatalf("CreateMap: %v", err)
	}

	fm, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Foo")
	if _, err := kb.WriteConcept(okf.ConceptID("entities/smart-home/foo"), fm, "# Foo\n", ""); err != nil {
		t.Fatalf("WriteConcept: %v", err)
	}

	content, err := kb.ReadRaw("entities/smart-home/index.md")
	if err != nil {
		t.Fatalf("ReadRaw stub index.md: %v", err)
	}
	if !strings.Contains(content, "type: Index") {
		t.Errorf("stub index.md missing 'type: Index': %q", content)
	}
	if !strings.Contains(content, "title: Smart Home") {
		t.Errorf("stub index.md title not derived from kebab name: %q", content)
	}
}

func TestWriteConcept_NonSovrascriveIndexDossierEsistente(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	if err := kb.CreateMap("entities", "Entities", "map", nil, ""); err != nil {
		t.Fatalf("CreateMap: %v", err)
	}
	// Pre-create the dossier directory with a custom index.md (no CreateDossier
	// anymore, D77 WP1 — plain filesystem setup, same as other tests in this file).
	os.MkdirAll(filepath.Join(kb.DataRoot(), "entities", "smart-home"), 0o755)
	os.WriteFile(filepath.Join(kb.DataRoot(), "entities", "smart-home", "index.md"),
		[]byte("---\ntype: Index\ntitle: Titolo Personalizzato\n---\n# Titolo Personalizzato\n"), 0o644)

	fm, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Foo")
	if _, err := kb.WriteConcept(okf.ConceptID("entities/smart-home/foo"), fm, "# Foo\n", ""); err != nil {
		t.Fatalf("WriteConcept: %v", err)
	}

	content, err := kb.ReadRaw("entities/smart-home/index.md")
	if err != nil {
		t.Fatalf("ReadRaw index.md: %v", err)
	}
	if !strings.Contains(content, "Titolo Personalizzato") {
		t.Errorf("existing dossier index.md was overwritten: %q", content)
	}
}

func TestWriteConcept_ErroreProfonditaMassima(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	if err := kb.CreateMap("entities", "Entities", "map", nil, ""); err != nil {
		t.Fatalf("CreateMap: %v", err)
	}

	fm, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Foo")
	_, err := kb.WriteConcept(okf.ConceptID("entities/smart-home/sub/foo"), fm, "# Foo\n", "")
	if !errors.Is(err, okf.ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath for 4-segment ConceptID, got: %v", err)
	}
	if !strings.Contains(err.Error(), "map/concept/child") {
		t.Errorf("expected error message to cite the map/concept/child limit, got: %v", err)
	}

	if _, statErr := os.Stat(filepath.Join(kb.DataRoot(), "entities", "smart-home", "sub", "foo.md")); !os.IsNotExist(statErr) {
		t.Error("rejected write should not have created any file")
	}
}

// --- ExpandConcept (D77 WP2) ---

func TestExpandConcept_Success(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: Foo")
	if _, err := kb.WriteConcept(okf.ConceptID("entities/foo"), fm, "# Foo\n", ""); err != nil {
		t.Fatalf("WriteConcept: %v", err)
	}

	if err := kb.ExpandConcept(okf.ConceptID("entities/foo")); err != nil {
		t.Fatalf("ExpandConcept: %v", err)
	}

	if _, err := os.Stat(filepath.Join(kb.DataRoot(), "entities", "foo.md")); !os.IsNotExist(err) {
		t.Error("ExpandConcept: entities/foo.md should no longer exist")
	}
	content, err := kb.ReadRaw("entities/foo/index.md")
	if err != nil {
		t.Fatalf("ReadRaw entities/foo/index.md: %v", err)
	}
	if !strings.Contains(content, "title: Foo") {
		t.Errorf("ExpandConcept: content not preserved: %q", content)
	}

	// The ID is unchanged: ReadConcept still resolves it, now to the
	// expanded form.
	data, err := kb.ReadConcept(okf.ConceptID("entities/foo"))
	if err != nil {
		t.Fatalf("ReadConcept after expand: %v", err)
	}
	if !strings.Contains(data.Content, "title: Foo") {
		t.Errorf("ReadConcept after expand: unexpected content: %q", data.Content)
	}

	// WriteConcept on the same id updates the expanded index.md in place.
	fm2, _ := okf.ParseFrontmatter("type: Note\ntitle: Foo Updated")
	if _, err := kb.WriteConcept(okf.ConceptID("entities/foo"), fm2, "# Foo\n", data.ContentHash); err != nil {
		t.Fatalf("WriteConcept after expand: %v", err)
	}
	updated, err := kb.ReadRaw("entities/foo/index.md")
	if err != nil {
		t.Fatalf("ReadRaw after update: %v", err)
	}
	if !strings.Contains(updated, "title: Foo Updated") {
		t.Errorf("WriteConcept after expand: update not applied: %q", updated)
	}

	// A satellite concept can now be written under the expanded directory.
	fm3, _ := okf.ParseFrontmatter("type: Note\ntitle: Child")
	if _, err := kb.WriteConcept(okf.ConceptID("entities/foo/child"), fm3, "# Child\n", ""); err != nil {
		t.Fatalf("WriteConcept satellite: %v", err)
	}
	if _, err := os.Stat(filepath.Join(kb.DataRoot(), "entities", "foo", "child.md")); err != nil {
		t.Errorf("satellite concept not written: %v", err)
	}
}

func TestExpandConcept_NotFound_Error(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	err := kb.ExpandConcept(okf.ConceptID("entities/nonexistent"))
	if !errors.Is(err, okf.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestExpandConcept_GiaEspanso_Errore(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: Foo")
	if _, err := kb.WriteConcept(okf.ConceptID("entities/foo"), fm, "# Foo\n", ""); err != nil {
		t.Fatalf("WriteConcept: %v", err)
	}
	if err := kb.ExpandConcept(okf.ConceptID("entities/foo")); err != nil {
		t.Fatalf("first ExpandConcept: %v", err)
	}

	err := kb.ExpandConcept(okf.ConceptID("entities/foo"))
	if err == nil || !strings.Contains(err.Error(), "already_expanded") {
		t.Fatalf("expected already_expanded error, got: %v", err)
	}
}

func TestExpandConcept_Figlio_ErroreProfondita(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: Child")
	if _, err := kb.WriteConcept(okf.ConceptID("entities/foo/child"), fm, "# Child\n", ""); err != nil {
		t.Fatalf("WriteConcept: %v", err)
	}

	err := kb.ExpandConcept(okf.ConceptID("entities/foo/child"))
	if !errors.Is(err, okf.ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath for a 3-segment id, got: %v", err)
	}
}

// --- DeleteConcept ---

func TestDeleteConcept_Rimuove(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	fm, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Test")
	if _, err := kb.WriteConcept(okf.ConceptID("mio-concept"), fm, "# Corpo\n", ""); err != nil {
		t.Fatalf("WriteConcept: %v", err)
	}

	if err := kb.DeleteConcept(okf.ConceptID("mio-concept")); err != nil {
		t.Fatalf("DeleteConcept: %v", err)
	}

	if _, err := kb.ReadConcept(okf.ConceptID("mio-concept")); !errors.Is(err, okf.ErrNotFound) {
		t.Fatalf("expected ErrNotFound after DeleteConcept, got: %v", err)
	}
}

func TestDeleteConcept_RifiutaFileRiservato(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	err := kb.DeleteConcept(okf.ConceptID("index"))
	if !errors.Is(err, okf.ErrInvalidConcept) {
		t.Fatalf("expected ErrInvalidConcept for reserved file index.md, got: %v", err)
	}
}

func TestDeleteConcept_NonEsistente(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	err := kb.DeleteConcept(okf.ConceptID("non-esiste"))
	if !errors.Is(err, okf.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestDeleteConcept_RifiutaIDVuoto(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	err := kb.DeleteConcept(okf.ConceptID(""))
	if !errors.Is(err, okf.ErrInvalidConcept) {
		t.Fatalf("expected ErrInvalidConcept for empty ConceptID, got: %v", err)
	}
}

// --- CreateMap ---

func TestCreateMap_CreaStruttura(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	err := kb.CreateMap("manutenzione", "Manutenzione", "map", []string{"Runbook", "Postmortem"}, "strict")
	if err != nil {
		t.Fatalf("CreateMap: %v", err)
	}

	// Verify presence of the three structural files.
	for _, rel := range []string{
		"manutenzione/_map.md",
		"manutenzione/index.md",
		"manutenzione/log.md",
	} {
		if _, err := os.Stat(filepath.Join(kb.DataRoot(), rel)); os.IsNotExist(err) {
			t.Errorf("CreateMap: missing %s", rel)
		}
	}

	// Verify _map.md contents.
	content, err := kb.ReadRaw("manutenzione/_map.md")
	if err != nil {
		t.Fatalf("ReadRaw _map.md: %v", err)
	}
	if !strings.Contains(content, "type: Map") {
		t.Errorf("_map.md does not contain 'type: Map': %q", content)
	}
	if !strings.Contains(content, "kind: map") {
		t.Errorf("_map.md does not contain 'kind: map': %q", content)
	}
	if !strings.Contains(content, "ontology_mode: strict") {
		t.Errorf("_map.md does not contain 'ontology_mode: strict': %q", content)
	}
}

func TestCreateMap_KindJournal(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	if err := kb.CreateMap("incidents", "Incidents", "journal", nil, ""); err != nil {
		t.Fatalf("CreateMap: %v", err)
	}
	content, err := kb.ReadRaw("incidents/_map.md")
	if err != nil {
		t.Fatalf("ReadRaw _map.md: %v", err)
	}
	if !strings.Contains(content, "kind: journal") {
		t.Errorf("_map.md does not contain 'kind: journal': %q", content)
	}
}

func TestCreateMap_KindVuoto_DefaultMap(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	if err := kb.CreateMap("manutenzione", "Manutenzione", "", nil, ""); err != nil {
		t.Fatalf("CreateMap: %v", err)
	}
	content, err := kb.ReadRaw("manutenzione/_map.md")
	if err != nil {
		t.Fatalf("ReadRaw _map.md: %v", err)
	}
	if !strings.Contains(content, "kind: map") {
		t.Errorf("_map.md: empty kind must default to 'map': %q", content)
	}
}

func TestCreateMap_KindInvalido_Errore(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	err := kb.CreateMap("manutenzione", "Manutenzione", "dossier", nil, "")
	if err == nil {
		t.Fatal("CreateMap: expected error for invalid kind")
	}
}

func TestCreateMap_ErroreSeEsiste(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	if err := kb.CreateMap("manutenzione", "Manutenzione", "map", nil, ""); err != nil {
		t.Fatalf("first CreateMap: %v", err)
	}
	err := kb.CreateMap("manutenzione", "Manutenzione 2", "map", nil, "")
	if err == nil {
		t.Fatal("CreateMap: expected error for already existing map")
	}
}

// --- ReadArchiveMeta: read-compat sul descriptor legacy (D77 WP1) ---

func TestReadArchiveMeta_LegacyArchiveDescriptor(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	// KB legacy: descriptor _archive.md, nessun campo kind.
	os.MkdirAll(filepath.Join(kb.DataRoot(), "entities"), 0o755)
	os.WriteFile(filepath.Join(kb.DataRoot(), "entities", "_archive.md"),
		[]byte("---\ntype: Archive\ntitle: Entities\narchive_type: ops\nontology_mode: flexible\n---\n# Entities\n"), 0o644)

	meta, err := kb.ReadArchiveMeta("entities")
	if err != nil {
		t.Fatalf("ReadArchiveMeta: %v", err)
	}
	kind, ok := meta.Get("kind")
	if !ok || kind != "map" {
		t.Errorf("ReadArchiveMeta: legacy _archive.md must be treated as kind=map, got: %v (ok=%v)", kind, ok)
	}
}

func TestReadArchiveMeta_PrefersMapOverLegacyArchive(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	os.MkdirAll(filepath.Join(kb.DataRoot(), "entities"), 0o755)
	os.WriteFile(filepath.Join(kb.DataRoot(), "entities", "_archive.md"),
		[]byte("---\ntype: Archive\ntitle: Old\n---\n"), 0o644)
	os.WriteFile(filepath.Join(kb.DataRoot(), "entities", "_map.md"),
		[]byte("---\ntype: Map\ntitle: New\nkind: journal\n---\n"), 0o644)

	meta, err := kb.ReadArchiveMeta("entities")
	if err != nil {
		t.Fatalf("ReadArchiveMeta: %v", err)
	}
	title, _ := meta.Get("title")
	if title != "New" {
		t.Errorf("ReadArchiveMeta: expected _map.md to take precedence over _archive.md, got title=%v", title)
	}
}

// --- Validate ---

func TestValidate_KBValida(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	if err := kb.CreateMap("manutenzione", "Manutenzione", "map", []string{"Runbook"}, "flexible"); err != nil {
		t.Fatalf("CreateMap: %v", err)
	}
	fm, _ := okf.ParseFrontmatter("type: Runbook\ntitle: Test")
	if _, err := kb.WriteConcept(okf.ConceptID("manutenzione/test-concept"), fm, "# Test\n", ""); err != nil {
		t.Fatalf("WriteConcept: %v", err)
	}

	errs, err := kb.Validate("")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("Validate: expected 0 errors on valid KB, found %d: %v", len(errs), errs)
	}
}

func TestValidate_RilevaCampoTypeVuoto(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	// Manually write a concept without the type field.
	p := filepath.Join(kb.DataRoot(), "concept-senza-type.md")
	if err := os.WriteFile(p, []byte("---\ntitle: Test\n---\n# Test\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	errs, err := kb.Validate("")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	found := false
	for _, e := range errs {
		if e.Path == "concept-senza-type.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Validate: expected error for concept-senza-type.md, found: %v", errs)
	}
}

func TestValidate_RilevaFrontmatterNonParsabile(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	// Concept without --- delimiters (plain text, no frontmatter).
	p := filepath.Join(kb.DataRoot(), "concept-no-fm.md")
	if err := os.WriteFile(p, []byte("Solo testo senza frontmatter"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	errs, err := kb.Validate("")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	found := false
	for _, e := range errs {
		if e.Path == "concept-no-fm.md" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Validate: expected error for concept-no-fm.md, found: %v", errs)
	}
}

func TestValidate_StrictOntologiaTypeNonAmmesso(t *testing.T) {
	dir := tempKB(t)
	kb, _ := Init(dir)

	if err := kb.CreateMap("manutenzione", "Manutenzione", "map", []string{"Runbook"}, "strict"); err != nil {
		t.Fatalf("CreateMap: %v", err)
	}

	// Concept with a type not allowed in the strict archive.
	p := filepath.Join(kb.DataRoot(), "manutenzione", "postmortem-non-ammesso.md")
	if err := os.WriteFile(p, []byte("---\ntype: Postmortem\ntitle: Test\n---\n# Test\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	errs, err := kb.Validate("manutenzione")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Message, "not allowed") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Validate: expected strict ontology error, found: %v", errs)
	}
}
