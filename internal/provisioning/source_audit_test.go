package provisioning_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// sourceSites classifies every non-test Go file that compares an artifact's
// source with "kb:" or "bundle" (D1729). A plugin artifact has
// source "plugin:<name>": it is neither bundled nor a KB's, so each site must
// have been looked at and its answer for a plugin is the value here. A new file,
// or a new comparison in a listed file (the count is pinned), fails
// TestSourceSitesAreClassified until someone classifies it and moves the count.
var sourceSites = map[string]struct {
	n   int // comparisons in the file: a new one fails until classified here
	why string
}{
	"cmd/cartographer/approve.go":                    {6, "KB approval paths: a plugin artifact has no pending state and never reaches them"},
	"cmd/cartographer/approve_migration.go":          {1, "D1141 migration: InstalledBeforeApprovals refuses a non-KB source"},
	"cmd/cartographer/clientsync.go":                 {10, "a held KB (the server could not mount it) carries the lock entries and the region of its kb: source; a plugin entry is never one; pullTarget rejects a served source that is not the bundle or the pulled KB; kb_options apply to kb: only; kbShadowsBundle: a plugin artifact is not kb:, so it is dropped when a KB ships the same kind and name and never shadows anything"},
	"cmd/cartographer/cliskill.go":                   {1, "bundle only"},
	"cmd/cartographer/doctor.go":                     {3, "lock entries from explicitly bound or declared transversal KBs are allowed; plugin entries are residues only when the plugin is no longer configured"},
	"cmd/cartographer/doctor_mcpvars.go":             {2, "reads the approved KB descriptors and names their KB; a plugin descriptor has no approval entry and is skipped"},
	"cmd/cartographer/doctor_shadow.go":              {3, "a KB artifact shadowed by a workspace file: a plugin artifact is not a KB's, so it is skipped"},
	"cmd/cartographer/kbrename.go":                   {1, "rename prefix of a KB, unrelated"},
	"cmd/cartographer/mcpprobe.go":                   {3, "probes an approved KB descriptor: a plugin artifact has no approval entry and is not a target"},
	"cmd/cartographer/mcpreach.go":                   {1, "names the KB of a descriptor the client did not receive; a plugin's has none"},
	"cmd/cartographer/plugins_local.go":              {4, "builds the plugin artifacts from approved trees; words a collision that names the bundle, a KB or a plugin; splitHeldKBs holds the lock entries of a KB the server could not mount, by its kb: source, so a plugin entry is never held by it"},
	"cmd/cartographer/status_snapshot.go":            {4, "heldWithInstructions names the held KBs with a kb: instructions entry, a plugin has none; trust is `plugin`, never needs_approval; kbAgentIssuePrefix: only a KB's agent has a sync_pull issue to match, a bundle or plugin agent yields none"},
	"cmd/cartographer/sync_follow.go":                {1, "names the artifacts of an added KB that wait for approval: a plugin artifact has no per-KB approval and is not listed"},
	"cmd/cartographer/usagesync.go":                  {1, "groups usage per KB and ignores other sources"},
	"cmd/cartographer/workspace.go":                  {6, "workspace projections permit bundle and bound KBs; plugins join the global scope only (forProjection); the bundle-only projection adds the instructions of a transversal KB by its kb: source, so a plugin artifact never matches; the lineage instructions projection selects the instructions of a bound KB by its kb: source, so a plugin artifact never matches and is left out"},
	"internal/artifactsig/artifactsig.go":            {2, "identity requires kb:, so a plugin artifact can never be signed"},
	"internal/clientconfig/clientconfig.go":          {1, "approval keys of KB sources"},
	"internal/kb/dashboard.go":                       {1, "link scheme of a links widget id naming another KB (kb:<kb>/<id>), refused; not an artifact source"},
	"internal/kb/graph.go":                           {1, "link scheme of a link to another KB (kb:<kb>/<id>), not an artifact source"},
	"internal/kb/usage.go":                           {1, "server-side usage skips bundle and plugin sources"},
	"internal/lint/values.go":                        {1, "token scope string, unrelated"},
	"internal/mcpserver/tools_sync.go":               {1, "server side, KB only"},
	"internal/plugin/core/manifest.go":               {1, "reserved plugin names, not a source comparison"},
	"internal/provisioning/approvalmigration.go":     {1, "refuses a non-KB source"},
	"internal/provisioning/expand.go":                {2, "placeholders expand with an empty KB for a plugin (global placeholders only)"},
	"internal/provisioning/kbguide.go":               {1, "instructions are KB-only"},
	"internal/provisioning/contract/provisioning.go": {16, "authorization, verification and merge: see source.go and source_test.go"},
	"internal/provisioning/contract/resolver.go":     {1, "requires applies to everything not bundled, plugins included"},
	"internal/provisioning/provisioning.go":          {29, "apply, authorization and warnings: see source.go and source_test.go; kbWithheld: a withheld lineage KB is matched by its kb: source, a plugin or bundle source is never withheld; launcherSpec: `mcp exec <kb>/<slug>` reads a KB descriptor, so a plugin descriptor gets no launcher and is skipped"},
	"internal/provisioning/source.go":                {1, "DropPluginCollisions: a bundled artifact stays, a plugin never overrides anything"},
	"internal/provisioning/stamp.go":                 {2, "plugin branch of the provenance stamp"},
	"internal/provisioning/unplaced.go":              {2, "KB skills only"},
	"internal/provisioning/usage.go":                 {1, "usage entries carry the source unchanged"},
}

var sourceSiteRe = regexp.MustCompile(`"kb:"|"bundle"`)

func TestSourceSitesAreClassified(t *testing.T) {
	root := filepath.Join("..", "..")
	found := map[string]int{}
	for _, dir := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if n := len(sourceSiteRe.FindAll(data, -1)); n > 0 {
				rel, _ := filepath.Rel(root, p)
				found[filepath.ToSlash(rel)] = n
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for f, n := range found {
		site, ok := sourceSites[f]
		switch {
		case !ok:
			t.Errorf("%s compares an artifact source with \"kb:\" or \"bundle\" and is not classified: decide what it does for a plugin: source and add it to sourceSites", f)
		case site.n != n:
			t.Errorf("%s has %d such comparisons, sourceSites pins %d: classify the change for a plugin: source, then update the count", f, n, site.n)
		}
	}
	for f := range sourceSites {
		if _, ok := found[f]; !ok {
			t.Errorf("sourceSites lists %s, which no longer has such a comparison: remove it", f)
		}
	}
}
