package provisioning_test

// Pi hooks: one generated extension per hook (probed on Pi 1.5.0).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
)

// applyPiHook applies a one-hook KB for Pi. script, when not empty, replaces
// the hook's notify.sh.
func applyPiHook(t *testing.T, baseDir, event, matcher, script string) (provisioning.AppliedResult, error) {
	t.Helper()
	kbRoot := t.TempDir()
	writeHookKB(t, kbRoot, "guard", event, matcher, "./notify.sh")
	if script != "" {
		if err := os.WriteFile(filepath.Join(kbRoot, "hooks", "guard", "notify.sh"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return provisioning.Apply(m, provisioning.ApplyOptions{ApprovedMCP: approveAll(m),
		KBRoots:  map[string]string{"kb": kbRoot},
		Provider: configurator.ProviderPi, BaseDir: baseDir, Lock: provisioning.Lock{},
	})
}

func piExtensionPath(baseDir string) string {
	return filepath.Join(baseDir, ".pi", "agent", "extensions", "cartographer-guard.ts")
}

func TestApplyAndPrune_PiHookIsAGeneratedExtension(t *testing.T) {
	baseDir := t.TempDir()
	res, err := applyPiHook(t, baseDir, "PreToolUse", "bash", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(piExtensionPath(baseDir)); err != nil {
		t.Fatalf("extension missing: %v", err)
	}
	// Pi autoloads extensions/*/index.ts: the hook's own files must sit outside.
	if _, err := os.Stat(filepath.Join(baseDir, ".pi", "agent", "cartographer-hooks", "guard", "hook.json")); err != nil {
		t.Errorf("hook files not under cartographer-hooks: %v", err)
	}
	err = filepath.WalkDir(filepath.Join(baseDir, ".pi", "agent", "extensions"), func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != filepath.Join(baseDir, ".pi", "agent", "extensions") {
			t.Errorf("subdirectory under extensions/ would be autoloaded: %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	managed := crushHookManaged(res, "guard")
	var saw bool
	for _, mf := range managed {
		saw = saw || mf.Path == ".pi/agent/extensions/cartographer-guard.ts"
	}
	if !saw {
		t.Fatalf("extension is not a managed file: %+v", managed)
	}
	if _, err := provisioning.PruneManaged(managed, baseDir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(piExtensionPath(baseDir)); !os.IsNotExist(err) {
		t.Errorf("extension survived the prune: %v", err)
	}
}

// piUpdateGolden rewrites a golden file when UPDATE_GOLDEN is set.
func piUpdateGolden(t *testing.T, name, content string) {
	t.Helper()
	if os.Getenv("UPDATE_GOLDEN") != "" {
		if err := os.WriteFile(filepath.Join("testdata", "pi", name+".golden.ts"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestApply_PiHookGolden(t *testing.T) {
	for _, tc := range []struct{ file, event, matcher string }{
		{"session_start", "SessionStart", ""},
		{"pre_tool_use", "PreToolUse", "Edit|Write"},
		{"post_tool_use", "PostToolUse", "mcp__.*__(concept_write)$"},
	} {
		baseDir := t.TempDir()
		if _, err := applyPiHook(t, baseDir, tc.event, tc.matcher, ""); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(piExtensionPath(baseDir))
		if err != nil {
			t.Fatal(err)
		}
		// The command is an absolute path under the temp base dir.
		norm := strings.ReplaceAll(string(got), filepath.ToSlash(baseDir), "$BASE")
		piUpdateGolden(t, tc.file, norm)
		want, err := os.ReadFile(filepath.Join("testdata", "pi", tc.file+".golden.ts"))
		if err != nil {
			t.Fatal(err)
		}
		if norm != string(want) {
			t.Errorf("%s: generated extension differs from testdata/pi/%s.golden.ts:\n%s", tc.event, tc.file, norm)
		}
		// Idempotent: a second sync rewrites the same bytes (same KB, same base dir).
		kbRoot := t.TempDir()
		writeHookKB(t, kbRoot, "guard", tc.event, tc.matcher, "./notify.sh")
		for i := 0; i < 2; i++ {
			m, err := provisioning.BuildManifest(nil, map[string]string{"kb": kbRoot}, provisioning.BuildOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := provisioning.Apply(m, provisioning.ApplyOptions{ApprovedMCP: approveAll(m), KBRoots: map[string]string{"kb": kbRoot},
				Provider: configurator.ProviderPi, BaseDir: baseDir, Lock: provisioning.Lock{}}); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				got, _ = os.ReadFile(piExtensionPath(baseDir))
			}
		}
		again, _ := os.ReadFile(piExtensionPath(baseDir))
		if string(again) != string(got) {
			t.Errorf("%s: re-register is not byte-identical", tc.event)
		}
	}
}

func TestApply_PiWriteFindingsGolden(t *testing.T) {
	baseDir := t.TempDir()
	if _, err := provisioning.EnsureWriteFindingsHook(baseDir, configurator.ProviderPi, provisioning.Lock{}, true, false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(baseDir, ".pi", "agent", "extensions", "cartographer-"+provisioning.WriteFindingsHookName+".ts"))
	if err != nil {
		t.Fatal(err)
	}
	norm := strings.ReplaceAll(string(got), filepath.ToSlash(baseDir), "$BASE")
	piUpdateGolden(t, "write_findings", norm)
	want, err := os.ReadFile(filepath.Join("testdata", "pi", "write_findings.golden.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if norm != string(want) {
		t.Errorf("write-findings extension differs from golden:\n%s", norm)
	}
}

func TestApply_PiHookBadMatcherOrEventIsNotRegistered(t *testing.T) {
	for _, tc := range []struct{ event, matcher, warn string }{
		{"PreToolUse", "(unclosed", "regular expression"},
		{"Stop", "", "not fired by this client"},
	} {
		baseDir := t.TempDir()
		res, err := applyPiHook(t, baseDir, tc.event, tc.matcher, "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(piExtensionPath(baseDir)); !os.IsNotExist(err) {
			t.Errorf("%s/%q: an extension was written: %v", tc.event, tc.matcher, err)
		}
		if !strings.Contains(strings.Join(res.Warnings, "\n"), tc.warn) {
			t.Errorf("%s/%q: warnings = %v, want %q", tc.event, tc.matcher, res.Warnings, tc.warn)
		}
	}
}

func TestApply_PiHookRefusesSymlinkedExtensionsDir(t *testing.T) {
	baseDir := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(baseDir, ".pi", "agent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(baseDir, ".pi", "agent", "extensions")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	res, err := applyPiHook(t, baseDir, "PreToolUse", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Apply reports the refusal per artifact (D148), it does not fail the sync.
	if len(res.Refused) != 1 || !strings.Contains(strings.Join(res.Warnings, "\n"), "symlink") {
		t.Fatalf("refused = %d, warnings = %v", len(res.Refused), res.Warnings)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("a write went through the symlink: %v", entries)
	}
}

func TestEnsureBootstrapAndWriteFindings_Pi(t *testing.T) {
	baseDir := t.TempDir()
	lock, err := provisioning.EnsureBootstrapHook(baseDir, configurator.ProviderPi, provisioning.Lock{}, false)
	if err != nil {
		t.Fatal(err)
	}
	lock, err = provisioning.EnsureWriteFindingsHook(baseDir, configurator.ProviderPi, lock, true, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{provisioning.BootstrapHookName, provisioning.WriteFindingsHookName} {
		for _, p := range []string{
			filepath.Join(".pi", "agent", "cartographer-hooks", name, "hook.json"),
			filepath.Join(".pi", "agent", "extensions", "cartographer-"+name+".ts"),
		} {
			if _, err := os.Stat(filepath.Join(baseDir, p)); err != nil {
				t.Errorf("%s missing: %v", p, err)
			}
			var inLock bool
			for _, mf := range lock.Managed {
				inLock = inLock || mf.Path == filepath.ToSlash(p)
			}
			if !inLock {
				t.Errorf("%s not in the lock", p)
			}
		}
	}
	if _, err := provisioning.PruneManaged(lock.Managed, baseDir, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, ".pi", "agent", "extensions", "cartographer-"+provisioning.BootstrapHookName+".ts")); !os.IsNotExist(err) {
		t.Errorf("extension survived the prune: %v", err)
	}
}

// piDriver loads a generated extension with a stub `pi` and fires one event.
const piDriver = `
const [,, file, ev, evtJson, ctxJson] = process.argv
const handlers = {}
const mod = await import(file)
mod.default({ on: (n, h) => { handlers[n] = h } })
try {
  const out = await handlers[ev](JSON.parse(evtJson), JSON.parse(ctxJson))
  console.log(JSON.stringify(out ?? null))
} catch (e) { console.log("THROWN") }
`

// firePi applies the hook with the given script, copies the extension to a
// .mjs and fires one event; it returns the handler result as JSON and the
// payload the script received on stdin.
func firePi(t *testing.T, event, matcher, script, piEvent, evtJSON string) (result, payload string) {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH")
	}
	baseDir := t.TempDir()
	out := filepath.Join(t.TempDir(), "payload.json")
	script = strings.ReplaceAll(script, "$OUT", out)
	if _, err := applyPiHook(t, baseDir, event, matcher, script); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(piExtensionPath(baseDir))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	ext := filepath.Join(dir, "ext.mjs")
	drv := filepath.Join(dir, "driver.mjs")
	if err := os.WriteFile(ext, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(drv, []byte(piDriver), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", drv, ext, piEvent, evtJSON, `{"cwd":"`+filepath.ToSlash(dir)+`"}`)
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, b)
	}
	p, _ := os.ReadFile(out)
	return strings.TrimSpace(string(b)), string(p)
}

func TestPiExtension_Behaviour(t *testing.T) {
	const block = "#!/bin/sh\ncat > $OUT\necho 'the reason' >&2\nexit 2\n"
	const soft = "#!/bin/sh\ncat > $OUT\necho 'noise' >&2\nexit 1\n"

	t.Run("PreToolUse exit 2 blocks with the stderr reason", func(t *testing.T) {
		res, payload := firePi(t, "PreToolUse", "bash", block, "tool_call", `{"toolName":"bash","input":{"command":"ls"},"toolCallId":"c1"}`)
		if res != `{"block":true,"reason":"the reason"}` {
			t.Errorf("result = %s", res)
		}
		var p map[string]any
		if err := json.Unmarshal([]byte(payload), &p); err != nil {
			t.Fatalf("payload %q: %v", payload, err)
		}
		if p["hook_event_name"] != "PreToolUse" || p["tool_name"] != "bash" || p["tool_use_id"] != "c1" {
			t.Errorf("payload = %v", p)
		}
	})
	t.Run("PreToolUse exit 1 does not block", func(t *testing.T) {
		if res, _ := firePi(t, "PreToolUse", "", soft, "tool_call", `{"toolName":"bash"}`); res != "null" {
			t.Errorf("result = %s", res)
		}
	})
	t.Run("matcher is unanchored and case-insensitive", func(t *testing.T) {
		if res, _ := firePi(t, "PreToolUse", "Bash", block, "tool_call", `{"toolName":"bash"}`); !strings.Contains(res, "block") {
			t.Errorf("Bash did not match bash: %s", res)
		}
		if res, _ := firePi(t, "PreToolUse", "^edit$", block, "tool_call", `{"toolName":"bash"}`); res != "null" {
			t.Errorf("a non-matching tool fired the hook: %s", res)
		}
	})
	t.Run("PostToolUse exit 2 appends a text block and keeps structuredContent", func(t *testing.T) {
		res, _ := firePi(t, "PostToolUse", "", block, "tool_result",
			`{"toolName":"x","content":[{"type":"text","text":"ok"}],"structuredContent":{"a":1},"isError":false}`)
		want := `{"content":[{"type":"text","text":"ok"},{"type":"text","text":"the reason"}],"structuredContent":{"a":1}}`
		if res != want {
			t.Errorf("result = %s\nwant     %s", res, want)
		}
	})
	t.Run("PostToolUse is skipped on isError", func(t *testing.T) {
		res, payload := firePi(t, "PostToolUse", "", block, "tool_result", `{"toolName":"x","content":[],"isError":true}`)
		if res != "null" || payload != "" {
			t.Errorf("result = %s, payload = %q", res, payload)
		}
	})
	t.Run("SessionStart sends the mapped source and returns nothing", func(t *testing.T) {
		res, payload := firePi(t, "SessionStart", "", block, "session_start", `{"reason":"new"}`)
		if res != "null" || !strings.Contains(payload, `"source":"clear"`) {
			t.Errorf("result = %s, payload = %s", res, payload)
		}
	})
	t.Run("a handler never throws", func(t *testing.T) {
		for _, ev := range []string{"null", `{}`, `{"toolName":null}`} {
			if res, _ := firePi(t, "PreToolUse", "bash", block, "tool_call", ev); res == "THROWN" {
				t.Errorf("event %s made the handler throw", ev)
			}
		}
	})
}
