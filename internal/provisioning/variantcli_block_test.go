package provisioning_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientvariant"
	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

func cliVariantProvider(t *testing.T, mode string) {
	t.Helper()
	spec := configurator.VariantSpec{Name: "fork-cli", Base: string(configurator.ProviderCrush), MCPMode: mode, Binaries: []string{"fork-cli"}}
	if mode != configurator.MCPModeCLI {
		spec.MCPFile = []string{".fork-cli", "mcp.json"}
	}
	if err := configurator.ReplaceVariants([]configurator.VariantSpec{spec}); err != nil {
		t.Fatal(err)
	}
	v := clientvariant.Variant{Name: "fork-cli", Base: "crush", MCPMode: mode, Global: clientvariant.Scope{Instructions: &clientvariant.Cell{File: []string{".fork-cli", "FORK.md"}}}}
	if _, err := provisioning.RegisterVariantCells(v); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		provisioning.UnregisterVariantCells("fork-cli")
		configurator.ReplaceVariants(nil)
	})
}

func applyVariantBlock(t *testing.T, m provisioning.Manifest, base string, lock provisioning.Lock) (provisioning.AppliedResult, string) {
	t.Helper()
	res, err := provisioning.Apply(m, provisioning.ApplyOptions{Provider: "fork-cli", BaseDir: base, Lock: lock, ExpandPlaceholders: true})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(base, ".fork-cli", "FORK.md"))
	return res, string(data)
}

// The cli sentence is client-written text of the block, so it has a rewrite
// trigger (the recorded hash): a mode flip rewrites the block in both directions,
// a changed sentence rewrites it once, and an unchanged one writes nothing.
func TestCLISentenceHasARewriteTrigger(t *testing.T) {
	m := sharedKBs(t, 1, nil)
	base := t.TempDir()

	cliVariantProvider(t, "file")
	r1, c1 := applyVariantBlock(t, m, base, provisioning.Lock{})
	if len(r1.Written) == 0 || strings.Contains(c1, "cartographer call") {
		t.Fatalf("a file variant must not carry the sentence:\n%s", c1)
	}

	cliVariantProvider(t, configurator.MCPModeCLI)
	r2, c2 := applyVariantBlock(t, m, base, r1.NewLock)
	if len(r2.Written) == 0 || !strings.Contains(c2, "cartographer call --provider fork-cli") {
		t.Fatalf("a flip to cli must rewrite the block:\n%s", c2)
	}
	if r2.NewLock.PathsSectionHash == r1.NewLock.PathsSectionHash {
		t.Error("the recorded hash did not change with the sentence")
	}
	r3, _ := applyVariantBlock(t, m, base, r2.NewLock)
	if len(r3.Written) != 0 {
		t.Errorf("an unchanged block must converge: %+v", r3.Written)
	}

	// An older release wrote other wording: the lock says so, the block is rewritten.
	old := strings.Replace(c2, "run `cartographer call", "invoke `cartographer call", 1)
	if err := os.WriteFile(filepath.Join(base, ".fork-cli", "FORK.md"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := r2.NewLock
	stale.PathsSectionHash = "from-an-older-release"
	r4, c4 := applyVariantBlock(t, m, base, stale)
	if len(r4.Written) == 0 || c4 != c2 {
		t.Errorf("a changed sentence must rewrite the block: %+v", r4.Written)
	}

	// And back to a file variant: the sentence leaves.
	cliVariantProvider(t, "file")
	r5, c5 := applyVariantBlock(t, m, base, r4.NewLock)
	if len(r5.Written) == 0 || strings.Contains(c5, "cartographer call") {
		t.Errorf("a flip back must drop the sentence:\n%s", c5)
	}
}
