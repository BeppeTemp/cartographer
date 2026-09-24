package kb

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/okf"
)

// graphFixture is a KB whose files the tests edit directly, the way an
// operator's editor or a git pull would: the cache must follow them with no
// notification at all.
type graphFixture struct {
	t *testing.T
	k *KB
}

func newGraphFixture(t *testing.T) *graphFixture {
	t.Helper()
	f := &graphFixture{t: t, k: mustInitKB(t)}
	f.write("infra/_map.md", "---\ntype: Map\ntitle: Infra\n---\n# Infra\n")
	f.write("notes/_map.md", "---\ntype: Map\ntitle: Notes\n---\n# Notes\n")
	f.write("infra/gateway.md", "---\ntype: Service\ntitle: Gateway\nstatus: active\n---\nSee [dns](dns.md), [[infra/cluster]] and [gone](missing.md).\n")
	f.write("infra/dns.md", "---\ntype: Service\ntitle: DNS\n---\nBack to [gateway](gateway.md). Diagram: [d](diagram).\n")
	f.write("infra/cluster.md", "---\ntype: Entity\n---\nSelf [me](cluster.md), [notes](../notes/log-review.md).\n")
	f.write("notes/log-review.md", "---\ntype: Note\ntitle: [unparseable\n---\n[[infra/gateway]]\n")
	f.write("notes/plain.md", "No frontmatter, links [dns](../infra/dns.md).\n")
	return f
}

func (f *graphFixture) abs(rel string) string {
	if strings.HasPrefix(rel, "services/") {
		return filepath.Join(f.k.Root, filepath.FromSlash(rel))
	}
	return filepath.Join(f.k.DataRoot(), filepath.FromSlash(rel))
}

func (f *graphFixture) write(rel, content string) {
	f.t.Helper()
	p := f.abs(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *graphFixture) remove(rel string) {
	f.t.Helper()
	if err := os.Remove(f.abs(rel)); err != nil {
		f.t.Fatal(err)
	}
}

func (f *graphFixture) rename(from, to string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.abs(to)), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(f.abs(from), f.abs(to)); err != nil {
		f.t.Fatal(err)
	}
}

// assertMatchesOracle compares every cached graph reader with its uncached
// pre-D241 copy (graphcache_oracle_test.go).
func (f *graphFixture) assertMatchesOracle(step string) {
	f.t.Helper()
	k := f.k
	oracle, err := k.oracleAdjacency()
	if err != nil {
		f.t.Fatalf("%s: oracle adjacency: %v", step, err)
	}
	in, err := k.IncomingLinks()
	if err != nil {
		f.t.Fatalf("%s: IncomingLinks: %v", step, err)
	}
	if !reflect.DeepEqual(in, oracle.in) {
		f.t.Fatalf("%s: IncomingLinks = %v, oracle %v", step, in, oracle.in)
	}

	ids := []okf.ConceptID{"never/existed"}
	for id := range oracle.out {
		ids = append(ids, id)
	}
	for _, id := range ids {
		for _, dir := range []string{"out", "in", "both"} {
			for depth := 1; depth <= 3; depth++ {
				got, err := k.GraphNeighbors(id, depth, dir)
				want, werr := k.oracleNeighbors(id, depth, dir)
				if (err == nil) != (werr == nil) || !reflect.DeepEqual(got, want) {
					f.t.Fatalf("%s: GraphNeighbors(%s, %d, %s) = %v, %v; oracle %v, %v", step, id, depth, dir, got, err, want, werr)
				}
			}
		}
	}

	hideNotes := func(id string) bool { return !strings.HasPrefix(id, "notes/") }
	for _, opts := range []GraphSnapshotOptions{
		{},
		{Include: hideNotes},
		{Scope: "infra", Limit: 2},
	} {
		got, err := k.GraphSnapshot(opts)
		want, werr := k.oracleSnapshot(opts)
		if (err == nil) != (werr == nil) || !reflect.DeepEqual(got, want) {
			f.t.Fatalf("%s: GraphSnapshot(%+v)\n got %+v\nwant %+v", step, opts.Scope, got, want)
		}
	}
}

func TestGraphCacheMatchesTheUncachedReaders(t *testing.T) {
	f := newGraphFixture(t)
	k := f.k
	steps := []struct {
		name string
		do   func()
	}{
		{"initial KB", func() {}},
		{"WriteConcept adds a link", func() {
			fm, _ := okf.ParseFrontmatter("type: Note\ntitle: New")
			if _, err := k.WriteConcept("notes/new", fm, "Links [[infra/dns]].\n", ""); err != nil {
				t.Fatal(err)
			}
		}},
		{"external edit", func() {
			f.write("infra/dns.md", "---\ntype: Service\ntitle: DNS v2\n---\nNow links [cluster](cluster.md) only.\n")
		}},
		{"delete", func() { f.remove("notes/new.md") }},
		{"move with backlink rewrite", func() {
			f.rename("infra/cluster.md", "infra/k8s.md")
			f.write("infra/gateway.md", "---\ntype: Service\ntitle: Gateway\nstatus: active\n---\nSee [dns](dns.md), [[infra/k8s]], [gone](missing.md) and [d](diagram).\n")
			f.write("infra/dns.md", "---\ntype: Service\ntitle: DNS v2\n---\nNow links [cluster](k8s.md) only.\n")
		}},
		{"ExpandConcept", func() {
			if err := k.ExpandConcept("infra/gateway"); err != nil {
				t.Fatal(err)
			}
		}},
		{"collapse", func() {
			f.rename("infra/gateway/index.md", "infra/gateway.md")
			if err := os.Remove(f.abs("infra/gateway")); err != nil {
				t.Fatal(err)
			}
		}},
		{"both map/c.md and map/c/index.md", func() {
			f.write("infra/k8s/index.md", "---\ntype: Runbook\ntitle: K8s expanded\n---\nExpanded links [[notes/plain]].\n")
		}},
		{"asset appears", func() { f.write("infra/diagram", "binary-ish") }},
		{"asset vanishes", func() { f.remove("infra/diagram") }},
		{"same-size rewrite with the mtime restored", func() {
			p := f.abs("notes/plain.md")
			// A recent mtime, cached as such: the racy window is what keeps
			// the rewrite below from passing as unchanged.
			now := time.Now()
			if err := os.Chtimes(p, now, now); err != nil {
				t.Fatal(err)
			}
			if _, err := k.IncomingLinks(); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			// Same length, different target: only the racy rule or the hash
			// can notice.
			f.write("notes/plain.md", "No frontmatter, links [gwy](../infra/gateway.md).\n"[:info.Size()])
			if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
				t.Fatal(err)
			}
		}},
		{"services concept", func() {
			f.write("services/keycloak.md", "---\ntype: Service\ntitle: Keycloak\n---\nFronts [[infra/gateway]].\n")
		}},
	}
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		steps = append(steps, struct {
			name string
			do   func()
		}{"unreadable file", func() {
			if err := os.Chmod(f.abs("infra/dns.md"), 0o000); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(f.abs("infra/dns.md"), 0o644) })
		}})
	}
	target := filepath.Join(t.TempDir(), "outside.md")
	steps = append(steps, struct {
		name string
		do   func()
	}{"concept replaced by a symlink", func() {
		if err := os.WriteFile(target, []byte("---\ntype: Note\n---\nOutside links [[infra/gateway]].\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		f.remove("notes/log-review.md")
		if err := os.Symlink(target, f.abs("notes/log-review.md")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}}, struct {
		name string
		do   func()
	}{"symlink target edited", func() {
		// The link itself is untouched: its lstat signature cannot tell. Its
		// own mtime cannot be backdated with os, so the racy window is shut
		// for this step: only the symlink rule forces the re-read.
		saved := racyWindow
		racyWindow = 0
		t.Cleanup(func() { racyWindow = saved })
		old := time.Now().Add(-time.Hour)
		if err := os.WriteFile(target, []byte("---\ntype: Note\n---\nNow links [[notes/plain]].\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(target, old, old)
	}})

	for _, step := range steps {
		step.do()
		f.assertMatchesOracle(step.name)
		// Out of the racy window, so the next step is seen through the
		// signature, probe and symlink rules rather than by a forced
		// re-read; then once more warm, where a reused view must agree.
		backdate(t, k)
		f.assertMatchesOracle(step.name + " (warm)")
	}
}

// The racy rule: a file rewritten with its size and mtime unchanged is
// re-read while its mtime is too recent to vouch for its content.
func TestGraphCacheCatchesARacilyCleanRewrite(t *testing.T) {
	f := newGraphFixture(t)
	if _, err := f.k.IncomingLinks(); err != nil {
		t.Fatal(err)
	}
	p := f.abs("infra/dns.md")
	info, _ := os.Stat(p)
	f.write("infra/dns.md", "---\ntype: Service\ntitle: DNS\n---\nBack to [gateway](gateway.md). Diagram: [d](diagxam).\n")
	if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	out, err := f.k.GraphNeighbors("infra/dns", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["infra/diagxam"]; !ok {
		t.Fatalf("the rewrite was missed: %v", out)
	}
}

func TestGraphCacheWarmCallParsesNothing(t *testing.T) {
	f := newGraphFixture(t)
	backdate(t, f.k)
	if _, err := f.k.GraphNeighbors("infra/gateway", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.k.GraphNeighbors("infra/gateway", 1); err != nil {
		t.Fatal(err)
	}
	s := f.k.graph.stats
	if s.parsed != 0 || s.reused == 0 {
		t.Fatalf("warm call: parsed %d, reused %d", s.parsed, s.reused)
	}
}

func TestConceptFacetsReadsOneFileWithoutAValidation(t *testing.T) {
	f := newGraphFixture(t)
	backdate(t, f.k)
	if _, err := f.k.IncomingLinks(); err != nil {
		t.Fatal(err)
	}
	validations := f.k.graph.stats.validations
	if got := f.k.ConceptFacets("infra/dns").Type; got != "Service" {
		t.Fatalf("type = %q", got)
	}
	if f.k.graph.stats.facetReads != 0 {
		t.Fatalf("a trusted cached entry was re-read")
	}
	f.write("infra/dns.md", "---\ntype: Runbook\n---\nChanged.\n")
	if got := f.k.ConceptFacets("infra/dns").Type; got != "Runbook" {
		t.Fatalf("type after edit = %q", got)
	}
	s := f.k.graph.stats
	if s.validations != validations || s.facetReads != 1 {
		t.Fatalf("validations %d→%d, facet reads %d", validations, s.validations, s.facetReads)
	}
	// The direct form wins over an expanded index.md, as in ReadConcept.
	f.write("infra/dns/index.md", "---\ntype: Entity\n---\nExpanded twin.\n")
	if got := f.k.ConceptFacets("infra/dns").Type; got != "Runbook" {
		t.Fatalf("ambiguous pair: type = %q, want the direct form's", got)
	}
	if got := f.k.ConceptFacets("infra/nope"); got.Exists || got.Type != "" {
		t.Fatalf("missing concept: %+v", got)
	}
}

func TestGraphCacheConcurrentReadsAndWrites(t *testing.T) {
	f := newGraphFixture(t)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 30; i++ {
			if _, err := f.k.GraphSnapshot(GraphSnapshotOptions{}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		fm, _ := okf.ParseFrontmatter("type: Note")
		for i := 0; i < 30; i++ {
			body := "Links [[infra/dns]].\n"
			if i%2 == 1 {
				body = "Links [[infra/gateway]].\n"
			}
			if _, err := f.k.WriteConcept("notes/churn", fm, body, ""); err != nil {
				// Windows refuses to rename over a file another goroutine
				// has open for reading ("Access is denied"), and this test
				// reads every file while it writes. That is the OS's sharing
				// rule, not the cache's: the uncached walk read them too.
				// The test is here for the race detector, so the write is
				// simply skipped.
				if runtime.GOOS == "windows" {
					continue
				}
				t.Error(err)
				return
			}
			_ = f.k.ConceptFacets("notes/churn")
		}
	}()
	wg.Wait()
	f.assertMatchesOracle("after concurrent churn")
}

// backdate moves every concept file's mtime out of the racy window, so a test
// can observe reuse without sleeping two seconds.
func backdate(t *testing.T, k *KB) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	for _, root := range []string{k.DataRoot(), filepath.Join(k.Root, "services")} {
		_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err == nil && !info.IsDir() {
				return os.Chtimes(p, old, old)
			}
			return nil
		})
	}
}

// A symlinked concept's lstat signature says nothing about its target, so
// ConceptFacets re-reads it every time — even when the link itself is old
// enough to be trusted.
func TestConceptFacetsNeverTrustsASymlink(t *testing.T) {
	f := newGraphFixture(t)
	target := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(target, []byte("---\ntype: Note\n---\nOutside.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.remove("notes/plain.md")
	if err := os.Symlink(target, f.abs("notes/plain.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	saved := racyWindow
	racyWindow = 0
	t.Cleanup(func() { racyWindow = saved })
	if got := f.k.ConceptFacets("notes/plain").Type; got != "Note" {
		t.Fatalf("type = %q", got)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.WriteFile(target, []byte("---\ntype: Runbook\n---\nRetyped.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(target, old, old)
	if got := f.k.ConceptFacets("notes/plain").Type; got != "Runbook" {
		t.Fatalf("type after the target changed = %q, want Runbook", got)
	}
}

// TestFacetsCarryBodyPlaceholders pins D262: the cached facets list the
// placeholder keys a body cites — escaped ones and metasyntax excluded,
// duplicates collapsed, frontmatter ignored — and follow the file on edit.
func TestFacetsCarryBodyPlaceholders(t *testing.T) {
	f := newGraphFixture(t)
	f.write("infra/tools.md", "---\ntype: Service\ntitle: \"{{path:in-frontmatter}}\"\n---\n"+
		"Clone {{repo:tool}} into {{path:work}}; again {{repo:tool}}. Syntax: {{\\repo:x}}, {{repo:<name>}}, {{repo:…}}.\n")

	lg, err := f.k.LinkGraph(nil)
	if err != nil {
		t.Fatal(err)
	}
	got := lg.Facets[lg.Index["infra/tools"]].Placeholders
	if want := []string{"path:work", "repo:tool"}; !reflect.DeepEqual(got, want) {
		t.Errorf("placeholders = %v, want %v", got, want)
	}
	if p := f.k.ConceptFacets("infra/tools").Placeholders; !reflect.DeepEqual(p, []string{"path:work", "repo:tool"}) {
		t.Errorf("ConceptFacets placeholders = %v", p)
	}

	f.write("infra/tools.md", "---\ntype: Service\n---\nNow only {{path:other}}.\n")
	lg, err = f.k.LinkGraph(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := lg.Facets[lg.Index["infra/tools"]].Placeholders; !reflect.DeepEqual(got, []string{"path:other"}) {
		t.Errorf("after edit placeholders = %v, want [path:other]", got)
	}
}
