package star

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/plugin"
	"github.com/BeppeTemp/cartographer/internal/plugin/core"
	"github.com/BeppeTemp/cartographer/internal/repodocs"
)

// examplePlugin reads test/e2e/fixtures/plugin-example, the plugin the docs
// tutorial and the e2e scenario 28 are built on.
func examplePlugin(t *testing.T) (core.Plugin, *Program) {
	t.Helper()
	root, err := repodocs.Root()
	if err != nil {
		t.Fatal(err)
	}
	src, err := plugin.ParseSource(filepath.Join(root, "test", "e2e", "fixtures", "plugin-example"))
	if err != nil {
		t.Fatal(err)
	}
	files, _, err := plugin.Fetch(context.Background(), src, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, problems := core.ReadTree(files, core.Options{})
	if len(problems) != 0 {
		t.Fatalf("the example plugin does not validate: %v", problems)
	}
	prog, err := Load(files)
	if err != nil {
		t.Fatal(err)
	}
	return p, prog
}

func TestExamplePluginRegistersWhatTheTutorialUses(t *testing.T) {
	p, prog := examplePlugin(t)
	got := strings.Join(prog.Lines(p.Manifest.Name), "\n")
	for _, want := range []string{"command example-forks hello", "tool example_forks_echo", "event sync.after"} {
		if !strings.Contains(got, want) {
			t.Errorf("registrations lack %q:\n%s", want, got)
		}
	}
}

func TestExamplePluginRuns(t *testing.T) {
	p, prog := examplePlugin(t)
	home := t.TempDir()
	base := Request{
		Plugin: p.Manifest.Name, Version: p.Manifest.Version, Permissions: p.Manifest.Permissions,
		Declared: p.Manifest.Settings, Host: Host{Home: home, ConfigDir: filepath.Join(home, "cfg"), CacheDir: filepath.Join(home, "cache")},
	}

	var out bytes.Buffer
	cmd := base
	cmd.Kind, cmd.Name, cmd.Args, cmd.Stdout = KindCommand, "hello", []string{"ada"}, &out
	cmd.Values = map[string]any{"greeting": "hi"}
	if _, err := prog.Call(context.Background(), cmd); err != nil || out.String() != "hi, ada!\n" {
		t.Errorf("hello: %q, %v", out.String(), err)
	}

	tool := base
	tool.Kind, tool.Name, tool.Params, tool.ReadOnly = KindTool, "echo", []byte(`{"kb":"kb-a","text":"x"}`), true
	tool.Host.KB = newFakeKB()
	res, err := prog.Call(context.Background(), tool)
	if err != nil || !strings.Contains(res.Text, `"echo": "x"`) {
		t.Errorf("echo: %q, %v", res.Text, err)
	}

	ev := base
	ev.Kind, ev.Name, ev.Event = KindEvent, "sync.after", map[string]any{"installed": 2}
	if _, err := prog.Call(context.Background(), ev); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, ".example-forks", "last-sync.txt"))
	if err != nil || string(b) != "synced 2 installed\n" {
		t.Errorf("marker: %q, %v", b, err)
	}
	// A read-only session must not write the marker.
	ev.ReadOnly = true
	if _, err := prog.Call(context.Background(), ev); err == nil {
		t.Error("the sync handler wrote in a read-only session")
	}
}
