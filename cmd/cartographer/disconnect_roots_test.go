package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// noteTree records roots and every directory below them in the created-files
// ledger, as the connect that made a hand-built fixture would have.
func noteTree(t *testing.T, base string, roots ...string) {
	t.Helper()
	var rels []string
	for _, r := range roots {
		rels = append(rels, r)
		_ = filepath.WalkDir(filepath.Join(base, filepath.FromSlash(r)), func(path string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				if rel, rerr := filepath.Rel(base, path); rerr == nil {
					rels = append(rels, filepath.ToSlash(rel))
				}
			}
			return nil
		})
	}
	if err := configurator.NoteCreatedDirs(base, rels); err != nil {
		t.Fatal(err)
	}
}

func countLines(out, want string) int {
	n := 0
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, want) {
			n++
		}
	}
	return n
}

// AC-sdc-17: disconnect, disconnect --dry-run, workspace unbind + sync and a
// plugin removal each print one line per root removed (or that would be).
func TestDisconnectAndUnbindAnnounceRoots(t *testing.T) {
	t.Run("disconnect", func(t *testing.T) {
		dir := setupDisconnectFixture(t, "claude")
		noteTree(t, dir, ".claude")
		res, err := doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir, DryRun: true})
		if err != nil {
			t.Fatal(err)
		}
		out := withStdout(t, func() { printDisconnectSummary(res, true) })
		if got := countLines(out, "[dry-run] [claude] would remove empty directory .claude"); got != 1 {
			t.Errorf("dry run printed %d lines for .claude:\n%s", got, out)
		}
		if _, err := os.Stat(filepath.Join(dir, ".claude")); err != nil {
			t.Fatalf("a dry run removed .claude: %v", err)
		}

		res, err = doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		out = withStdout(t, func() { printDisconnectSummary(res, false) })
		if got := countLines(out, "[claude] removed empty directory .claude"); got != 1 {
			t.Errorf("real run printed %d lines for .claude:\n%s", got, out)
		}
		if _, err := os.Stat(filepath.Join(dir, ".claude")); !os.IsNotExist(err) {
			t.Errorf(".claude survived: %v", err)
		}
	})

	t.Run("disconnect keeps a root it did not create", func(t *testing.T) {
		dir := setupDisconnectFixture(t, "claude")
		res, err := doDisconnect(disconnectOptions{Providers: []string{"claude"}, Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		out := withStdout(t, func() { printDisconnectSummary(res, false) })
		if strings.Contains(out, "empty directory") {
			t.Errorf("a root the ledger does not list was announced:\n%s", out)
		}
	})

	t.Run("workspace unbind + sync", func(t *testing.T) {
		ws := t.TempDir()
		lf, _ := copilotWorkspaceSync(t, ws)
		var out, errb bytes.Buffer
		if _, err := pruneOrphanProjections(syncStreams{Out: &out, Err: &errb}, lf, otherWorkspace(), nil, t.TempDir(), nil); err != nil {
			t.Fatal(err)
		}
		if got := countLines(out.String(), "[copilot] removed empty directory .github"); got != 1 {
			t.Errorf("printed %d lines for .github:\n%s", got, out.String())
		}
	})

	t.Run("plugin removal", func(t *testing.T) {
		home := pluginFixture(t)
		runPlugin(t, "install", writePlugin(t, "team-forks", nil), "--yes")
		mine := skillEntry(t, home, "helper", "plugin:team-forks")
		lockWith(t, home, mine)
		noteTree(t, home, ".claude")
		code, out := runPlugin(t, "remove", "team-forks")
		if code != 0 || countLines(out, "[claude] removed empty directory .claude") != 1 {
			t.Fatalf("exit %d\n%s", code, out)
		}
	})
}

// A dry-run disconnect lists the roots the real one removes, for the common case
// of claude, codex and opencode: the real run deletes the MCP config files and
// the hook registrations first, which is what empties the roots.
func TestDisconnectDryRunMatchesRealRoots(t *testing.T) {
	dir := setupDisconnectFixture(t, "claude", "codex", "opencode")
	lockPath := lockFilePath(dir)
	lf, err := provisioning.ReadLockFile(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"claude", "codex", "opencode"} {
		lock, err := provisioning.EnsureBootstrapHook(dir, configurator.Provider(p), lf.ForProvider(p), false)
		if err != nil {
			t.Fatal(err)
		}
		lf.SetProvider(p, lock)
	}
	if err := provisioning.WriteLockFile(lockPath, lf); err != nil {
		t.Fatal(err)
	}
	// The fixture makes the skill directories itself; record what a connect would.
	roots := []string{".claude", ".codex", ".opencode", ".config", ".config/opencode"}
	noteTree(t, dir, roots...)

	collect := func(res disconnectResult) []string {
		var out []string
		for _, pr := range res.Providers {
			out = append(out, pr.RemovedRoots...)
		}
		sort.Strings(out)
		return out
	}
	dry, err := doDisconnect(disconnectOptions{Providers: []string{"claude", "codex", "opencode"}, Dir: dir, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	want := collect(dry)
	for _, r := range roots {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(r))); err != nil {
			t.Fatalf("the dry run touched %s: %v", r, err)
		}
	}
	real, err := doDisconnect(disconnectOptions{Providers: []string{"claude", "codex", "opencode"}, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	got := collect(real)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dry run listed %v, the real run removed %v", want, got)
	}
	if len(got) != len(roots) {
		t.Errorf("removed %v, want every recorded root %v", got, roots)
	}
}

// Two providers sharing a root (.config holds opencode's and crush's own
// directories): the dry run lists it at the provider where the real run removes
// it, the last one, and never at the first.
func TestDisconnectDryRunSharedRoot(t *testing.T) {
	dir := setupDisconnectFixture(t, "opencode", "crush")
	roots := []string{".opencode", ".config", ".config/opencode", ".config/crush"}
	noteTree(t, dir, roots...)
	perProvider := func(res disconnectResult) map[string][]string {
		out := map[string][]string{}
		for _, pr := range res.Providers {
			out[pr.Provider] = pr.RemovedRoots
		}
		return out
	}
	opts := disconnectOptions{Providers: []string{"opencode", "crush"}, Dir: dir}
	opts.DryRun = true
	dry, err := doDisconnect(opts)
	if err != nil {
		t.Fatal(err)
	}
	opts.DryRun = false
	real, err := doDisconnect(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(perProvider(dry), perProvider(real)) {
		t.Fatalf("dry run %v, real run %v", perProvider(dry), perProvider(real))
	}
	if slicesContains(perProvider(real)["opencode"], ".config") || !slicesContains(perProvider(real)["crush"], ".config") {
		t.Errorf(".config must go with the last provider only: %v", perProvider(real))
	}
}

// A directory that was already empty and held no config file is the user's: a
// disconnect that deleted nothing there leaves it.
func TestDisconnectKeepsAnEmptyConfigDirItDidNotEmpty(t *testing.T) {
	dir := setupDisconnectFixture(t, "kiro")
	if err := os.Remove(filepath.Join(dir, ".kiro", "settings", "mcp.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".kiro", "settings"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := doDisconnect(disconnectOptions{Providers: []string{"kiro"}, Dir: dir}); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dir, ".kiro", "settings")); err != nil || !fi.IsDir() {
		t.Fatalf("the user's empty .kiro/settings was removed: %v", err)
	}
}
