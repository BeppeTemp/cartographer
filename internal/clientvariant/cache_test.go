package clientvariant_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientvariant"
)

func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	c := clientvariant.Cache{"fork-cli": clientvariant.NewCacheEntry("kb-a", "h1", []byte(forkCLI), time.Unix(0, 0))}
	if err := clientvariant.SaveCache(dir, c); err != nil {
		t.Fatal(err)
	}
	got, err := clientvariant.LoadCache(dir)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	e := got["fork-cli"]
	data, err := e.Descriptor()
	if err != nil || string(data) != forkCLI || e.SourceKB != "kb-a" || e.ContentHash != "h1" {
		t.Fatalf("%+v %v", e, err)
	}
	if n := clientvariant.CachedNames(dir); !n["fork-cli"] {
		t.Fatalf("CachedNames %v", n)
	}
	if without := got.Without("fork-cli"); len(without) != 0 || len(got) != 1 {
		t.Fatalf("Without mutated or kept: %v / %v", without, got)
	}
}

func TestCorruptCacheIsAnErrorNotAFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, clientvariant.CacheFileName), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := clientvariant.LoadCache(dir); err == nil {
		t.Fatal("a corrupt cache loaded")
	}
	if c, err := clientvariant.LoadCache(t.TempDir()); err != nil || len(c) != 0 {
		t.Fatalf("absent cache: %v %v", c, err)
	}
}

func TestRetiredNeedsACompletePull(t *testing.T) {
	c := clientvariant.Cache{"a": {}, "b": {}, "c": {}}
	declared := map[string]bool{"b": true}
	if got := c.Retired(declared, true); strings.Join(got, ",") != "a,c" {
		t.Fatalf("retired %v", got)
	}
	if got := c.Retired(declared, false); got != nil {
		t.Fatalf("a partial pull retired %v", got)
	}
}
