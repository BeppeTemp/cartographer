package main

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
)

// merge adds the pulls of o to p; the relations of o are not read (a lineage
// KB is never a seed).
func (p *prefetched) merge(o *prefetched) *prefetched {
	if p == nil {
		return o
	}
	maps.Copy(p.cs.Named, o.cs.Named)
	maps.Copy(p.cs.Placeholders, o.cs.Placeholders)
	maps.Copy(p.cs.PathRegistries, o.cs.PathRegistries)
	maps.Copy(p.cs.Issues, o.cs.Issues)
	maps.Copy(p.cs.FeedbackModes, o.cs.FeedbackModes)
	maps.Copy(p.kbs, o.kbs)
	p.cs.HasBare = p.cs.HasBare || o.cs.HasBare
	p.failed = append(p.failed, o.failed...)
	return p
}

// lineageKBsToPull is the KBs the effective bindings and provider sets of
// targets hold by lineage that the pulls in pre do not cover.
func lineageKBsToPull(cfg *clientconfig.Config, targets []string, pre *prefetched) []string {
	var out []string
	add := func(k string) {
		if !pre.covers([]string{k}) && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	for _, p := range targets {
		for _, k := range cfg.ProviderLineage(p) {
			add(k)
		}
		for _, w := range cfg.WorkspaceBindings(p) {
			for k := range w.Lineage {
				add(k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// restoredLineage puts back the stored entry of every seed whose closure holds
// a KB that could not be pulled. It copies: cfg keeps the new closure.
func restoredLineage(prev clientconfig.DerivedState, lineage map[string]clientconfig.DerivedLineage, failed []kbPullFailure, errw io.Writer) map[string]clientconfig.DerivedLineage {
	if len(failed) == 0 {
		return lineage
	}
	out := maps.Clone(lineage)
	for seed, l := range lineage {
		for _, f := range failed {
			if !slices.Contains(l.Closure, f.KB) {
				continue
			}
			fmt.Fprintf(errw, "lineage of %s left as it was: %s could not be pulled\n", escapeControl(seed), escapeControl(f.KB))
			if old, ok := prev.Lineage[seed]; ok {
				out[seed] = old
			} else {
				delete(out, seed)
			}
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// widening is the difference between two snapshots of the bindings.
type widening struct {
	Added, Removed []clientconfig.DerivedChangeItem
}

// count is the number of added items that came by via.
func (w widening) count(via string) (n int) {
	for _, it := range w.Added {
		if it.Via == via {
			n++
		}
	}
	return n
}

// countPaths is the number of distinct repositories that gained a binding by
// via: one repository bound to two KBs counts once.
func (w widening) countPaths(via string) int {
	seen := map[string]bool{}
	for _, it := range w.Added {
		if it.Via == via {
			seen[it.Path] = true
		}
	}
	return len(seen)
}

// record is the LastChange the run stores, capped as the loader requires.
func (w widening) record(now time.Time) *clientconfig.DerivedChange {
	c := &clientconfig.DerivedChange{At: now.UTC().Format(time.RFC3339), Added: w.Added, Removed: w.Removed}
	c.Added = c.Added[:min(len(c.Added), clientconfig.MaxLastChangeItems)]
	c.Removed = c.Removed[:min(len(c.Removed), clientconfig.MaxLastChangeItems-len(c.Added))]
	return c
}

// bindingSnapshot is every KB held through paths.yaml or lineage, per path or provider.
func bindingSnapshot(cfg *clientconfig.Config, targets []string) map[clientconfig.DerivedChangeItem]bool {
	out := map[clientconfig.DerivedChangeItem]bool{}
	for _, p := range targets {
		for _, b := range cfg.Derived().Bindings[p] {
			for _, kb := range b.KBs {
				out[clientconfig.DerivedChangeItem{KB: kb, Path: b.Path, Via: clientconfig.LastChangeViaPaths}] = true
			}
		}
		for _, w := range cfg.WorkspaceBindings(p) {
			for kb, seeds := range w.Lineage {
				out[clientconfig.DerivedChangeItem{KB: kb, Path: w.Path, Via: clientconfig.LastChangeViaLineage, From: slices.Min(seeds)}] = true
			}
		}
		own, _ := cfg.BoundOwnKBs(p)
		for _, kb := range cfg.ProviderLineage(p) {
			i := slices.IndexFunc(own, func(seed string) bool { return slices.Contains(cfg.Lineage(seed).Closure, kb) })
			out[clientconfig.DerivedChangeItem{KB: kb, Via: clientconfig.LastChangeViaLineage, From: own[max(i, 0)], Provider: p}] = true
		}
	}
	return out
}

// diffSnapshots lists what appeared and went; a KB whose pull failed is not
// announced (its holders are skipped and the failure is reported).
func diffSnapshots(before, after map[clientconfig.DerivedChangeItem]bool, pre *prefetched) widening {
	var w widening
	for it := range after {
		if !before[it] && len(pre.failedFor([]string{it.KB})) == 0 {
			w.Added = append(w.Added, it)
		}
	}
	for it := range before {
		if !after[it] {
			w.Removed = append(w.Removed, it)
		}
	}
	key := func(it clientconfig.DerivedChangeItem) string {
		return it.Provider + "\x00" + it.Path + "\x00" + it.KB + "\x00" + it.Via
	}
	for _, l := range [][]clientconfig.DerivedChangeItem{w.Added, w.Removed} {
		slices.SortFunc(l, func(a, b clientconfig.DerivedChangeItem) int { return strings.Compare(key(a), key(b)) })
	}
	return w
}

// printWidenedBindings announces each change of a derived or lineage binding,
// before the apply, under the dry-run prefix when nothing is written.
func printWidenedBindings(out io.Writer, w widening, dryRun bool) {
	prefix := ""
	if dryRun {
		prefix = "[dry-run] "
	}
	line := func(it clientconfig.DerivedChangeItem, no string) string {
		kb, from := escapeControl(it.KB), ""
		if it.From != "" && no == "" {
			from = " (from " + escapeControl(it.From) + ")"
		}
		if it.Provider != "" {
			return fmt.Sprintf("%s: %s %sbound by lineage%s", escapeControl(it.Provider), kb, no, from)
		}
		return fmt.Sprintf("%sbound by %s: %s%s in %s", no, it.Via, kb, from, escapeControl(it.Path))
	}
	for _, it := range w.Added {
		fmt.Fprintln(out, prefix+line(it, ""))
	}
	for _, it := range w.Removed {
		fmt.Fprintln(out, prefix+line(it, "no longer "))
	}
}

// syncWidening is what one sync added to what is bound, by stage.
type syncWidening struct{ Mounted, Derived, Lineage int }

// printSyncWidening is the closing line: nothing when no count is set.
func printSyncWidening(out io.Writer, s syncWidening, dryRun bool) {
	var parts []string
	for _, c := range []struct {
		n    int
		what string
	}{{s.Mounted, "KB(s) mounted"}, {s.Derived, "repository(ies) bound by paths.yaml"}, {s.Lineage, "KB(s) brought by lineage"}} {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.what))
		}
	}
	if len(parts) == 0 {
		return
	}
	verb, prefix := "widened", ""
	if dryRun {
		verb, prefix = "would widen", "[dry-run] "
	}
	fmt.Fprintf(out, "%sthis sync %s what is bound: %s; review with cartographer status\n", prefix, verb, strings.Join(parts, ", "))
}
