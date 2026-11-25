package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// lineageHome is trustHome with the stored lineage `client-kb -> core-kb`.
func lineageHome(t *testing.T) (home, ws string) {
	t.Helper()
	home, ws = trustHome(t)
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg.KnownKBs = []string{"client-kb", "core-kb"}
	if err := clientconfig.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	state := clientconfig.DerivedState{Lineage: map[string]clientconfig.DerivedLineage{
		"client-kb": {Closure: []string{"core-kb"}},
	}}
	if err := clientconfig.SaveDerived(home, state); err != nil {
		t.Fatal(err)
	}
	return home, ws
}

func loadBinding(t *testing.T, home, ws string) (*clientconfig.Config, clientconfig.WorkspaceBinding) {
	t.Helper()
	cfg, err := clientconfig.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := clientconfig.CanonicalWorkspacePath(ws)
	for _, w := range cfg.WorkspaceBindings("codex") {
		if w.Path == canonical {
			return cfg, w
		}
	}
	t.Fatalf("no binding for %s", ws)
	return nil, clientconfig.WorkspaceBinding{}
}

// AC-lineage-21
func TestWorkspaceBindExclude(t *testing.T) {
	home, ws := lineageHome(t)
	withStdout(t, func() { cmdWorkspaceBind([]string{"codex", ws, "--kb", "client-kb"}) })
	if _, w := loadBinding(t, home, ws); !slices.Equal(w.KBs, []string{"client-kb", "core-kb"}) {
		t.Fatalf("without the flag: KBs = %v", w.KBs)
	}
	var code int
	out := withStdout(t, func() { code = cmdWorkspaceBind([]string{"codex", ws, "--kb", "client-kb", "--exclude", "core-kb"}) })
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	_, w := loadBinding(t, home, ws)
	if !slices.Equal(w.Exclude, []string{"core-kb"}) || !slices.Equal(w.KBs, []string{"client-kb"}) {
		t.Fatalf("exclude = %v, KBs = %v", w.Exclude, w.KBs)
	}
	if strings.Contains(out, "by lineage") {
		t.Errorf("the bind line announces an excluded KB: %q", out)
	}
	withStdout(t, func() { cmdWorkspaceBind([]string{"codex", ws, "--kb", "client-kb"}) })
	if _, w := loadBinding(t, home, ws); len(w.Exclude) != 0 || !slices.Contains(w.KBs, "core-kb") {
		t.Errorf("a rebind without the flag kept the exclusion: %+v", w)
	}
}

// AC-lineage-22
func TestWorkspaceBindLineageUsageErrors(t *testing.T) {
	_, ws := lineageHome(t)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no-lineage with exclude", []string{"--kb", "client-kb", "--no-lineage", "--exclude", "core-kb"}, "redundant"},
		{"exclude with no-kb", []string{"--no-kb", "--exclude", "core-kb"}, "--no-kb"},
		{"no-lineage with no-kb", []string{"--no-kb", "--no-lineage"}, "--no-kb"},
		{"kb and exclude", []string{"--kb", "client-kb,core-kb", "--exclude", "core-kb"}, "both --kb and --exclude"},
		{"exclude without value", []string{"--kb", "client-kb", "--exclude"}, "--exclude needs a value"},
	}
	for _, c := range cases {
		var code int
		errOut := withStderr(t, func() { code = cmdWorkspaceBind(append([]string{"codex", ws}, c.args...)) })
		if code != 2 || !strings.Contains(errOut, c.want) {
			t.Errorf("%s: code=%d err=%q, want 2 and %q", c.name, code, errOut, c.want)
		}
	}
}

// AC-lineage-23
func TestFixLinesKeepTheOptions(t *testing.T) {
	for _, opt := range []string{"--exclude core-kb", "--no-lineage"} {
		home, ws := lineageHome(t)
		args := append([]string{"codex", ws, "--kb", "client-kb"}, strings.Fields(opt)...)
		withStdout(t, func() { cmdWorkspaceBind(args) })
		cfg, w := loadBinding(t, home, ws)
		want := w

		var lines []string
		for _, st := range workspaceStatuses(cfg, "codex", home) {
			lines = append(lines, st.Detail, st.Bind)
		}
		for _, f := range checkWorkspaceProjections(home, cfg, []string{"codex"}) {
			lines = append(lines, f.Message, f.Fix)
		}
		lines = append(lines, codexUntrustedDetail(w, provisioning.CodexConfigPath(home)))
		var bound int
		for _, l := range lines {
			i := strings.Index(l, "cartographer workspace bind")
			if i < 0 {
				continue
			}
			bound++
			cmd := strings.TrimSuffix(strings.SplitN(l[i:], "`", 2)[0], " --trust")
			if !strings.Contains(cmd, opt) || strings.Contains(cmd, "core-kb") && !strings.Contains(cmd, "--exclude core-kb") {
				t.Errorf("%s: printed command %q does not repeat the option or names a lineage KB", opt, cmd)
			}
		}
		if bound == 0 {
			t.Fatalf("%s: no printed bind command found in %q", opt, lines)
		}

		// Round trip: the status command rebinds to the same binding.
		fields := strings.Fields(bindCommand("codex", w.Path, w))
		withStdout(t, func() { cmdWorkspaceBind(fields[3:]) })
		_, got := loadBinding(t, home, ws)
		if got.NoLineage != want.NoLineage || !slices.Equal(got.Exclude, want.Exclude) || !slices.Equal(got.OwnKBs(), want.OwnKBs()) {
			t.Errorf("%s: round trip changed the binding: %+v -> %+v", opt, want, got)
		}
	}
}

// AC-lineage-24
func TestWorkspaceListAndResolveShowLineage(t *testing.T) {
	home, ws := lineageHome(t)
	withStdout(t, func() { cmdWorkspaceBind([]string{"codex", ws, "--kb", "client-kb"}) })
	list := withStdout(t, func() { cmdWorkspaceList(nil) })
	if !strings.Contains(list, "client-kb  + core-kb (lineage of client-kb)") {
		t.Errorf("list: %q", list)
	}
	cfg, _ := loadBinding(t, home, ws)
	canonical, _ := clientconfig.CanonicalWorkspacePath(ws)
	sub := filepath.Join(canonical, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	res := renderWorkspaceResolve(cfg, sub, []string{"codex"})
	for _, want := range []string{
		"->  client-kb, core-kb",
		"  lineage: core-kb from client-kb (opt out: workspace bind codex " + canonical + " --kb client-kb --exclude core-kb)",
	} {
		if !strings.Contains(res, want) {
			t.Errorf("resolve lacks %q:\n%s", want, res)
		}
	}

	withStdout(t, func() { cmdWorkspaceBind([]string{"codex", ws, "--kb", "client-kb", "--exclude", "core-kb"}) })
	list = withStdout(t, func() { cmdWorkspaceList(nil) })
	if !strings.Contains(list, "[exclude core-kb]") || strings.Contains(list, "lineage of") {
		t.Errorf("list after exclude: %q", list)
	}
	withStdout(t, func() { cmdWorkspaceBind([]string{"codex", ws, "--kb", "client-kb", "--no-lineage"}) })
	if list = withStdout(t, func() { cmdWorkspaceList(nil) }); !strings.Contains(list, "[no lineage]") {
		t.Errorf("list after no-lineage: %q", list)
	}
}
