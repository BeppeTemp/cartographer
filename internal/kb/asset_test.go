package kb

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

func expandedAssetKB(t *testing.T) *KB {
	t.Helper()
	k, err := Init(tempKB(t))
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: Asset owner")
	if _, err := k.WriteConcept("map/owner", fm, "# Owner\n", ""); err != nil {
		t.Fatalf("WriteConcept: %v", err)
	}
	if err := k.ExpandConcept("map/owner"); err != nil {
		t.Fatalf("ExpandConcept: %v", err)
	}
	return k
}

func TestAssetCRUDTextAndBinary(t *testing.T) {
	k := expandedAssetKB(t)
	entry, err := k.WriteAsset("map/owner", "reports/inventory.csv", []byte("host,cve\na,CVE-1\n"), "", nil)
	if err != nil {
		t.Fatalf("WriteAsset text: %v", err)
	}
	if entry.Executable || entry.Size == 0 {
		t.Fatalf("unexpected entry: %+v", entry)
	}
	binary := []byte{0xff, 0x00, 0x80}
	if _, err := k.WriteAsset("map/owner", "evidence/screen.png", binary, "", nil); err != nil {
		t.Fatalf("WriteAsset binary: %v", err)
	}
	got, binaryEntry, err := k.ReadAsset("map/owner", "evidence/screen.png")
	if err != nil || !bytes.Equal(got, binary) || binaryEntry.SHA256 == "" {
		t.Fatalf("ReadAsset binary: data=%v entry=%+v err=%v", got, binaryEntry, err)
	}
	entries, err := k.ListAssets("map/owner")
	if err != nil || len(entries) != 2 {
		t.Fatalf("ListAssets: entries=%+v err=%v", entries, err)
	}
}

func TestAssetRejectsBadOwnerPathAndSize(t *testing.T) {
	k := expandedAssetKB(t)
	if _, err := k.WriteAsset("map/missing", "report.csv", []byte("x"), "", nil); !errors.Is(err, okf.ErrNotFound) {
		t.Fatalf("missing owner: expected ErrNotFound, got %v", err)
	}
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: Direct")
	if _, err := k.WriteConcept("map/direct", fm, "# Direct\n", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := k.WriteAsset("map/direct", "report.csv", []byte("x"), "", nil); !errors.Is(err, okf.ErrInvalidPath) {
		t.Fatalf("direct owner: expected ErrInvalidPath, got %v", err)
	}
	for _, path := range []string{"../escape.csv", "report.md", "nested/.hidden.csv"} {
		if _, err := k.WriteAsset("map/owner", path, []byte("x"), "", nil); !errors.Is(err, okf.ErrInvalidPath) {
			t.Fatalf("%s: expected ErrInvalidPath, got %v", path, err)
		}
	}
	if _, err := k.WriteAsset("map/owner", "large.csv", make([]byte, AssetMaxFileSize+1), "", nil); !errors.Is(err, okf.ErrInvalidPath) {
		t.Fatalf("oversize: expected ErrInvalidPath, got %v", err)
	}
}

func TestAssetOverwriteModesDeleteAndWalk(t *testing.T) {
	k := expandedAssetKB(t)
	trueValue, falseValue := true, false
	first, err := k.WriteAsset("map/owner", "nested/tool.sh", []byte("#!/bin/sh\n"), "", &trueValue)
	if err != nil || !first.Executable {
		t.Fatalf("create executable: %+v %v", first, err)
	}
	if _, err := k.WriteAsset("map/owner", "nested/tool.sh", []byte("new"), "wrong", nil); !errors.Is(err, okf.ErrStaleWrite) {
		t.Fatalf("mismatch: expected ErrStaleWrite, got %v", err)
	}
	second, err := k.WriteAsset("map/owner", "nested/tool.sh", []byte("new"), first.SHA256, nil)
	if err != nil || !second.Executable {
		t.Fatalf("preserve mode: %+v %v", second, err)
	}
	third, err := k.WriteAsset("map/owner", "nested/tool.sh", []byte("newer"), second.SHA256, &falseValue)
	if err != nil || third.Executable {
		t.Fatalf("change mode: %+v %v", third, err)
	}
	if err := k.DeleteAsset("map/owner", "nested/tool.sh", third.SHA256); err != nil {
		t.Fatalf("DeleteAsset: %v", err)
	}
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "map", "owner", "nested")); !os.IsNotExist(err) {
		t.Fatalf("nested directory not pruned: %v", err)
	}
	var concepts []okf.ConceptID
	if err := k.WalkConcepts(func(id okf.ConceptID, _ string) error { concepts = append(concepts, id); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(concepts) != 1 || concepts[0] != "map/owner" {
		t.Fatalf("WalkConcepts included an asset: %v", concepts)
	}
}
