package main

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning/contract"
)

// lineageUnresolvedStatus is one relation `status` reports as not followed:
// the seed KB that declares it, the name as written and the server's reason.
type lineageUnresolvedStatus struct {
	KB     string `json:"kb"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// lineageReasonText is the sentence for a reason of the server's closed set. One
// sentence covers an absent and an unreadable target, so output never tells
// which; an unknown reason is printed escaped, as sent.
func lineageReasonText(reason string) string {
	switch reason {
	case "not_mounted":
		return "no mounted KB readable with this token matches"
	case "remote_mismatch":
		return "the mounted KB's origin differs from the declared remote"
	case "no_remote":
		return "the relation declares no remote to match a mounted KB against"
	case "self":
		return "the KB declares itself"
	case "cycle":
		return "the relation closes a cycle"
	case "too_deep":
		return "the chain of relations is too deep"
	}
	return escapeControl(reason)
}

// lineageReasonFix is the fix a reason implies; none says the KB exists.
func lineageReasonFix(reason string) string {
	if reason == "not_mounted" {
		return "mount it on the server or fix the relation in the KB's settings.yaml"
	}
	return "fix the relation in the KB's settings.yaml"
}

// storedUnresolved lists the stored unresolved relations of seeds, in order.
func storedUnresolved(cfg *clientconfig.Config, seeds []string) []lineageUnresolvedStatus {
	var out []lineageUnresolvedStatus
	for _, seed := range seeds {
		for _, u := range cfg.Lineage(seed).Unresolved {
			out = append(out, lineageUnresolvedStatus{KB: seed, Name: u.Name, Reason: u.Reason})
		}
	}
	return out
}

// printLineageLines prints the lineage and the relations not followed.
func printLineageLines(lineage map[string][]string, unresolved []lineageUnresolvedStatus) {
	for _, k := range slices.Sorted(maps.Keys(lineage)) {
		fmt.Printf("    lineage: %s (from %s)\n", escapeControl(k), escapeControl(strings.Join(lineage[k], ", ")))
	}
	for _, u := range unresolved {
		fmt.Printf("    relation %s -> %s not followed: %s\n", escapeControl(u.KB), escapeControl(u.Name), lineageReasonText(u.Reason))
	}
}

// providerLineage is the lineage of a provider-scope binding by KB, as the
// workspace one: each KB with the own KBs whose closure holds it.
func providerLineage(cfg *clientconfig.Config, provider string) map[string][]string {
	own, _ := cfg.BoundOwnKBs(provider)
	var out map[string][]string
	for _, k := range cfg.ProviderLineage(provider) {
		for _, seed := range own {
			if slices.Contains(cfg.Lineage(seed).Closure, k) {
				if out == nil {
					out = map[string][]string{}
				}
				out[k] = append(out[k], seed)
			}
		}
	}
	return out
}

// lineageSeedPlaces maps each seed KB a provider binds by hand to where it is
// bound (a path, or the provider's own list) and what that binding already
// holds or excludes.
type lineageSeedPlace struct {
	where              string
	own, have, exclude []string
	stored             []string
}

func lineageSeedPlaces(cfg *clientconfig.Config, provider string) map[string][]lineageSeedPlace {
	out := map[string][]lineageSeedPlace{}
	add := func(where string, own, have, exclude []string) {
		for _, seed := range own {
			out[seed] = append(out[seed], lineageSeedPlace{where: where, own: own, have: have, exclude: exclude, stored: cfg.Lineage(seed).Closure})
		}
	}
	for _, w := range cfg.WorkspaceBindings(provider) {
		if !w.NoLineage {
			add(w.Path, w.OwnKBs(), w.KBs, w.Exclude)
		}
	}
	if own, explicit := cfg.BoundOwnKBs(provider); explicit && !cfg.Clients[provider].NoLineage {
		eff, _ := cfg.BoundKBs(provider)
		add("the client's own list", own, eff, cfg.Clients[provider].Exclude)
	}
	return out
}

// lineageDrift compares the closures the status pull saw (rels) with the stored
// ones, only for a seed whose pull carried `relations`; a seed not pulled is
// not compared. Each difference is a reason sync would act on.
func lineageDrift(cfg *clientconfig.Config, provider string, rels map[string]contract.Relations) []string {
	var out []string
	places := lineageSeedPlaces(cfg, provider)
	for _, seed := range slices.Sorted(maps.Keys(places)) {
		rel, ok := rels[seed]
		if !ok {
			continue
		}
		fresh := lineageFromRelations(cfg, seed, rel).Closure
		for _, pl := range places[seed] {
			for _, k := range fresh {
				if !slices.Contains(pl.have, k) && !slices.Contains(pl.exclude, k) {
					out = append(out, fmt.Sprintf("lineage changed (%s would be bound in %s)", escapeControl(k), escapeControl(pl.where)))
				}
			}
			for _, k := range pl.stored {
				if !slices.Contains(fresh, k) && slices.Contains(pl.have, k) && !slices.Contains(pl.own, k) {
					out = append(out, fmt.Sprintf("lineage changed (%s would be unbound in %s)", escapeControl(k), escapeControl(pl.where)))
				}
			}
		}
	}
	return out
}

// lastChangeLine is the `last change` line of `status`: the date, the vias and
// every KB added or removed with where it went and why.
func lastChangeLine(c *clientconfig.DerivedChange) string {
	if c == nil || len(c.Added)+len(c.Removed) == 0 {
		return ""
	}
	vias := map[string]bool{}
	var parts []string
	item := func(sign string, it clientconfig.DerivedChangeItem) {
		vias[it.Via] = true
		where := it.Path
		if it.Provider != "" {
			where = "provider " + it.Provider
		}
		why := "paths.yaml of " + it.KB
		if it.Via == clientconfig.LastChangeViaLineage {
			why = "lineage of " + it.From
		}
		parts = append(parts, fmt.Sprintf("%s %s in %s (%s)", sign, escapeControl(it.KB), escapeControl(where), escapeControl(why)))
	}
	for _, it := range c.Added {
		item("+", it)
	}
	for _, it := range c.Removed {
		item("-", it)
	}
	via := slices.Sorted(maps.Keys(vias))
	sort.Strings(via)
	date, _, _ := strings.Cut(c.At, "T")
	return fmt.Sprintf("last change %s (%s): %s", escapeControl(date), escapeControl(strings.Join(via, ", ")), strings.Join(parts, ", "))
}

// checkLineage is `doctor`'s view of the relations the server could not
// resolve: one warning per stored relation, for workspace and provider seeds
// alike. No finding says the target exists: the server answers alike for an
// absent and an unreadable KB.
func checkLineage(dir string, cfg *clientconfig.Config) []doctorFinding {
	var out []doctorFinding
	seeds := slices.Sorted(maps.Keys(lineageSeedsOf(cfg, cfg.Derived().Bindings)))
	for _, u := range storedUnresolved(cfg, seeds) {
		out = append(out, doctorFinding{
			Check: "lineage", Severity: doctorWarning, Path: filepath.Join(dir, clientconfig.FileName),
			Message: fmt.Sprintf("relation %s -> %s not followed: %s", escapeControl(u.KB), escapeControl(u.Name), lineageReasonText(u.Reason)),
			Fix:     lineageReasonFix(u.Reason),
		})
	}
	return out
}
