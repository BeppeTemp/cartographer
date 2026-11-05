package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientvariant"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// runDispatch runs `hook dispatch` with a payload on stdin and a fake home,
// returning the exit code and the one line it printed.
func runDispatch(t *testing.T, stdin string, args ...string) (int, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	old := hookStdin
	defer func() { hookStdin = old }()
	hookStdin = strings.NewReader(stdin)
	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	code := cmdHook(append([]string{"dispatch"}, args...))
	w.Close()
	os.Stdout = oldOut
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return code, strings.TrimSpace(buf.String())
}

func TestHookDispatchIsAlwaysExitZeroWithJSON(t *testing.T) {
	for name, args := range map[string][]string{
		"garbage payload":  {"--provider", "cline", "--event", "PreToolUse"},
		"unknown provider": {"--provider", "nope", "--event", "PreToolUse"},
		"unknown event":    {"--provider", "cline", "--event", "Nope"},
		"no flags":         {},
		"dangling flag":    {"--provider"},
	} {
		code, out := runDispatch(t, "not json", args...)
		if code != 0 || out != "{}" {
			t.Errorf("%s: code %d out %q", name, code, out)
		}
	}
}

func TestAsciiJSON(t *testing.T) {
	got := asciiJSON(struct {
		M string `json:"errorMessage"`
	}{"è<😀"})
	if want := `{"errorMessage":"\u00e8\u003c\ud83d\ude00"}`; got != want {
		t.Errorf("asciiJSON = %s, want %s", got, want)
	}
	for _, r := range got {
		if r > 0x7f {
			t.Fatalf("non-ASCII rune in %s", got)
		}
	}
}

const hookVariant = `schema: 1
name: fork-cli
display_name: Fork CLI
base: cline
detect:
  binaries: [fork-cli]
mcp:
  mode: cli
paths:
  global:
    hook:
      dir: "{home}/.fork-cli/cartographer-hooks"
      scan: base
`

// `hook dispatch` is a process of its own started by the client: it finds a
// variant's hooks only because main registered the variant from the validated
// cache. After a revoke the files remain and nothing runs; an unapproved
// variant never ran either.
func TestHookDispatchRunsAVariantsHookOnlyWhileItIsApproved(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sh scripts")
	}
	home := variantHome(t)
	a := variantArtifact(hookVariant)
	dir := filepath.Join(home, ".fork-cli", "cartographer-hooks", "vhook")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.json"), []byte(`{"event":"PostToolUse","command":"./run.sh"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\ncat >/dev/null\necho 'variant ran' >&2\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := clientvariant.SaveCache(home, cacheOf(a)); err != nil {
		t.Fatal(err)
	}
	dispatchNow := func() string {
		t.Helper()
		old := hookStdin
		defer func() { hookStdin = old }()
		hookStdin = strings.NewReader(`{"postToolUse":{"toolName":"x","parameters":{},"result":"ok","success":true}}`)
		r, w, _ := os.Pipe()
		oldOut := os.Stdout
		os.Stdout = w
		code := cmdHook([]string{"dispatch", "--provider", "cline", "--event", "PostToolUse"})
		w.Close()
		os.Stdout = oldOut
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		if code != 0 {
			t.Fatalf("exit %d", code)
		}
		return buf.String()
	}

	writeConfig(t, home) // not approved: main registers nothing
	registerCachedVariants(&bytes.Buffer{})
	if out := dispatchNow(); strings.Contains(out, "variant ran") {
		t.Fatalf("an unapproved variant's hook ran: %s", out)
	}

	writeConfig(t, home, a)
	registerCachedVariants(&bytes.Buffer{})
	if out := dispatchNow(); !strings.Contains(out, "variant ran") {
		t.Fatalf("the approved variant's hook did not run: %s", out)
	}

	if err := forgetCachedVariant(home, "fork-cli"); err != nil { // approve revoke variant
		t.Fatal(err)
	}
	provisioning.ResetVariants()
	registerCachedVariants(&bytes.Buffer{}) // the next process
	if out := dispatchNow(); strings.Contains(out, "variant ran") {
		t.Fatalf("a revoked variant's hook ran: %s", out)
	}
}
