package kb

import (
	"testing"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

func changesOf(t *testing.T, k *KB) map[okf.ConceptID]ConceptChange {
	t.Helper()
	list, err := k.ConceptChanges()
	if err != nil {
		t.Fatalf("ConceptChanges: %v", err)
	}
	out := make(map[okf.ConceptID]ConceptChange, len(list))
	for _, c := range list {
		out[c.ID] = c
	}
	return out
}

// D245: the first call reports every concept, then only what changed, and
// each change exactly once.
func TestConceptChanges_ReportsEachChangeOnce(t *testing.T) {
	f := newGraphFixture(t)
	first := changesOf(t, f.k)
	if c, ok := first["infra/dns"]; !ok || c.Known || c.Hash != okf.ContentHash(c.Content) {
		t.Fatalf("first call: infra/dns = %+v (present %v)", c, ok)
	}
	if len(first) != 5 {
		t.Fatalf("first call reported %d concepts, want 5: %v", len(first), first)
	}
	backdate(t, f.k)
	if got := changesOf(t, f.k); len(got) != 0 {
		t.Fatalf("unchanged KB reported %v", got)
	}

	f.write("infra/dns.md", "---\ntype: Service\ntitle: DNS\n---\nEdited outside.\n")
	got := changesOf(t, f.k)
	if c := got["infra/dns"]; len(got) != 1 || !c.Known || c.Content != "---\ntype: Service\ntitle: DNS\n---\nEdited outside.\n" {
		t.Fatalf("external edit reported %v", got)
	}
	backdate(t, f.k)
	if got := changesOf(t, f.k); len(got) != 0 {
		t.Fatalf("external edit reported twice: %v", got)
	}

	f.remove("notes/plain.md")
	got = changesOf(t, f.k)
	if c := got["notes/plain"]; len(got) != 1 || !c.Removed || c.Content != "" {
		t.Fatalf("delete reported %v", got)
	}
	if got := changesOf(t, f.k); len(got) != 0 {
		t.Fatalf("delete reported twice: %v", got)
	}
}

// Expanding a concept with identical content is not a change for an index:
// the id still resolves to the same bytes.
func TestConceptChanges_RenameToExpandedWithSameContent(t *testing.T) {
	f := newGraphFixture(t)
	changesOf(t, f.k)
	content := "---\ntype: Service\ntitle: DNS\n---\nBack to [gateway](gateway.md). Diagram: [d](diagram).\n"
	f.remove("infra/dns.md")
	f.write("infra/dns/index.md", content)
	if got := changesOf(t, f.k); len(got) != 0 {
		t.Fatalf("identical rename reported %v", got)
	}
}

// With both forms on disk, the direct one wins, as ReadConcept resolves it.
func TestConceptChanges_AmbiguousPairReportsDirectForm(t *testing.T) {
	f := newGraphFixture(t)
	f.write("infra/dns/index.md", "---\ntype: Service\n---\nExpanded form.\n")
	if c := changesOf(t, f.k)["infra/dns"]; c.Content != "---\ntype: Service\ntitle: DNS\n---\nBack to [gateway](gateway.md). Diagram: [d](diagram).\n" {
		t.Fatalf("ambiguous pair reported %q", c.Content)
	}
	// The direct form goes: the expanded one now wins, and is a change.
	f.remove("infra/dns.md")
	if c := changesOf(t, f.k)["infra/dns"]; c.Content != "---\ntype: Service\n---\nExpanded form.\n" || c.Removed {
		t.Fatalf("after removing the direct form: %+v", c)
	}
}

// Seeded hashes suppress reporting; a nil seed reports everything again.
func TestConceptChanges_SeedSuppressesReporting(t *testing.T) {
	f := newGraphFixture(t)
	all := changesOf(t, f.k)
	seed := map[okf.ConceptID]string{}
	for id, c := range all {
		seed[id] = c.Hash
	}
	f.k.SeedReportedHashes(seed)
	if got := changesOf(t, f.k); len(got) != 0 {
		t.Fatalf("seeded call reported %v", got)
	}
	delete(seed, "notes/plain")
	f.k.SeedReportedHashes(seed)
	if got := changesOf(t, f.k); len(got) != 1 || got["notes/plain"].Known {
		t.Fatalf("partial seed reported %v", got)
	}
	f.k.SeedReportedHashes(nil)
	if got := changesOf(t, f.k); len(got) != len(all) {
		t.Fatalf("nil seed reported %d, want %d", len(got), len(all))
	}
}
