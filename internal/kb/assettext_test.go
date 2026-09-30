package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// D277: which assets are searchable text, and what the signature covers.
func TestIndexableAssetText_Rules(t *testing.T) {
	k := expandedAssetKB(t)
	write := func(rel string, data []byte) {
		t.Helper()
		if _, err := k.WriteAsset("map/owner", rel, data, "", nil); err != nil {
			t.Fatalf("WriteAsset %s: %v", rel, err)
		}
	}
	write("b/inventory.csv", []byte("host,mac\ngw1,aa:bb\n"))
	write("a/notes.txt", []byte("first note"))
	write("scan.png", []byte("pngdata"))           // extension not indexable
	write("blob.json", []byte("{\"x\":\"\xff\"}")) // not UTF-8
	write("nul.log", []byte("a\x00b"))             // NUL byte
	write("big.txt", []byte(strings.Repeat("a", AssetIndexMaxBytes)+"TAILMARKER"))

	text, sig, err := k.IndexableAssetText("map/owner")
	if err != nil {
		t.Fatal(err)
	}
	// Path order, "<path>\n<text>\n\n".
	if !strings.HasPrefix(text, "a/notes.txt\nfirst note\n\nb/inventory.csv\nhost,mac\ngw1,aa:bb\n\n") {
		t.Fatalf("unexpected text prefix: %.120q", text)
	}
	for _, absent := range []string{"pngdata", "scan.png", "blob.json", "nul.log", "TAILMARKER"} {
		if strings.Contains(text, absent) {
			t.Fatalf("%q must not be indexed", absent)
		}
	}
	if !strings.Contains(text, "big.txt\n") {
		t.Fatal("truncated asset must still be indexed")
	}
	sigs, err := k.AssetSignatures()
	if err != nil || sigs["map/owner"] != sig || sig == "" {
		t.Fatalf("AssetSignatures = %v (%v), want %q", sigs, err, sig)
	}
}

func TestIndexableAssetText_OversizedAndHidden(t *testing.T) {
	k := expandedAssetKB(t)
	dir := filepath.Join(k.DataRoot(), "map", "owner")
	if err := os.MkdirAll(filepath.Join(dir, ".hidden"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".hidden", "x.txt"), []byte("hiddenword"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A file above the cap reaches the KB through git, not asset_write.
	f, err := os.Create(filepath.Join(dir, "huge.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(AssetMaxFileSize + 1); err != nil {
		t.Fatal(err)
	}
	f.Close()
	text, sig, err := k.IndexableAssetText("map/owner")
	if err != nil || text != "" || sig != "" {
		t.Fatalf("text=%q sig=%q err=%v, want nothing indexable", text, sig, err)
	}
	if sigs, _ := k.AssetSignatures(); len(sigs) != 0 {
		t.Fatalf("AssetSignatures = %v, want none", sigs)
	}
}

func TestAssetSignatures_TracksChangesAndRacyWindow(t *testing.T) {
	k := expandedAssetKB(t)
	// A non-expanded concept and a flat one own no assets.
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: Flat")
	if _, err := k.WriteConcept("map/flat", fm, "body\n", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := k.WriteAsset("map/owner", "a.csv", []byte("x,y\n"), "", nil); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(k.DataRoot(), "map", "owner", "a.csv")
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(abs, old, old); err != nil {
		t.Fatal(err)
	}
	s1, err := k.AssetSignatures()
	if err != nil || len(s1) != 1 || s1["map/owner"] == "" {
		t.Fatalf("s1 = %v, %v", s1, err)
	}
	if strings.HasSuffix(s1["map/owner"], "~") {
		t.Fatal("an old file must not be racy")
	}
	s2, _ := k.AssetSignatures()
	if s2["map/owner"] != s1["map/owner"] {
		t.Fatal("signature is not stable for an unchanged file")
	}
	// Same size, new mtime: the signature changes.
	newer := old.Add(time.Minute)
	if err := os.Chtimes(abs, newer, newer); err != nil {
		t.Fatal(err)
	}
	s3, _ := k.AssetSignatures()
	if s3["map/owner"] == s1["map/owner"] {
		t.Fatal("mtime change not reflected in the signature")
	}
	// A file modified just now is inside the racy window: marked, so it never
	// equals the next signature.
	now := time.Now()
	if err := os.Chtimes(abs, now, now); err != nil {
		t.Fatal(err)
	}
	s4, _ := k.AssetSignatures()
	if !strings.HasSuffix(s4["map/owner"], "~") {
		t.Fatalf("recent file not marked racy: %q", s4["map/owner"])
	}
}
