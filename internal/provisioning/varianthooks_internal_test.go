package provisioning

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

const variantHookDir = ".fork-ext/cartographer-hooks"

func variantHookArtifact(name, event string) Artifact {
	return builtInHook(name, "h-"+name, map[string]string{
		"hook.json": `{"event":"` + event + `","command":"./extra.sh"}`,
		"extra.sh":  "#!/bin/sh\ncat >/dev/null\necho 'from " + name + "' >&2\nexit 2\n",
	})
}

func applyHooks(t *testing.T, p configurator.Provider, base string, lock Lock, arts ...Artifact) AppliedResult {
	t.Helper()
	res, err := Apply(Manifest{Revision: "r", Artifacts: arts}, ApplyOptions{Provider: p, BaseDir: base, Lock: lock, SkipLockWrite: true})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// A variant with a hook cell on a base with the shared dispatcher gets a hook
// destination of its own and the base's mechanism; the project scope and the
// other kinds are untouched.
func TestVariantHookCellIsBuiltForTheSharedDispatcher(t *testing.T) {
	notes := registerTestVariant(t, testVariant(t, "fork-ext", testForkExt, "linux", nil))
	if len(notes) != 0 {
		t.Fatalf("notes = %q", notes)
	}
	p := configurator.Provider("fork-ext")
	if got := destDir("hook", "x", p); got != variantHookDir+"/x" {
		t.Errorf("hook destination = %q", got)
	}
	if got := destDirScoped("hook", "x", p, ScopeProject); got != "" {
		t.Errorf("project hook destination = %q", got)
	}
	if _, ok := mechanismFor(p); !ok {
		t.Error("no mechanism for a variant with a hook cell")
	}
	if !SupportsSessionHook(p) {
		t.Error("SupportsSessionHook must answer as the base does")
	}
	if hookProviderFromPath(variantHookDir+"/x/hook.json") != "cline" {
		t.Error("a variant's hook files are removed through the base's registrar")
	}
	if !hookEventReaches("PostToolUse", p) || hookEventReaches("Stop", p) {
		t.Error("event reach must follow the base")
	}
}

// scan: base on a base without the shared dispatcher loses only the cell, with
// a reason; without a hook cell a variant has no mechanism at all.
func TestVariantHookCellIsLostOnABaseWithoutTheSharedDispatcher(t *testing.T) {
	src := strings.Replace(testForkExt, "base: cline", "base: pi", 1)
	v := testVariant(t, "fork-ext", src, "linux", nil)
	notes := registerTestVariant(t, v)
	if len(notes) != 1 || !strings.Contains(notes[0], "hook cell ignored") {
		t.Fatalf("notes = %q", notes)
	}
	p := configurator.Provider("fork-ext")
	if got := destDir("hook", "x", p); got != "" {
		t.Errorf("hook destination = %q, want none", got)
	}
	if _, ok := mechanismFor(p); ok {
		t.Error("a variant without a hook cell has no hook mechanism")
	}
	if SupportsSessionHook(p) {
		t.Error("SupportsSessionHook = true without a hook cell")
	}
}

// The dispatcher is shared and never written by a variant: a hook of Cline and
// one of the variant share one dispatcher per event, the variant's retirement
// keeps it while Cline's hook remains, and removes it with the last hook.
func TestVariantHooksShareTheDispatcherAndRetirementKeepsIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix path")
	}
	registerTestVariant(t, testVariant(t, "fork-ext", testForkExt, "linux", nil))
	base, _ := clineStubBase(t)
	fork := configurator.Provider("fork-ext")

	resF := applyHooks(t, fork, base, Lock{}, variantHookArtifact("vhook", "PostToolUse"))
	resC := applyHooks(t, configurator.ProviderCline, base, Lock{}, variantHookArtifact("chook", "PostToolUse"))
	if len(resF.Written) == 0 || len(resC.Written) == 0 {
		t.Fatalf("written: %v / %v", resF.Written, resC.Written)
	}
	for _, d := range []string{variantHookDir + "/vhook/hook.json", ".cline/cartographer-hooks/chook/hook.json"} {
		if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(d))); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(clineHooksDir(base))
	if err != nil || len(entries) != 1 || entries[0].Name() != "PostToolUse" {
		t.Fatalf("one dispatcher expected: %v %v", entries, err)
	}
	// Both hooks run under the one dispatcher.
	if got := DispatchClineHooks(base, "PostToolUse", []byte(`{"postToolUse":{"toolName":"x","parameters":{},"result":"ok","success":true}}`)).ContextModification; !strings.Contains(got, "from vhook") || !strings.Contains(got, "from chook") {
		t.Fatalf("context = %q", got)
	}

	// Retire the variant's hook: the dispatcher stays for Cline's.
	applyHooks(t, fork, base, resF.NewLock)
	if _, err := os.Stat(clineDispatcherPath(base, "PostToolUse")); err != nil {
		t.Fatalf("the dispatcher left with the variant's hook: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(variantHookDir+"/vhook"))); !os.IsNotExist(err) {
		t.Errorf("the variant's hook files stayed: %v", err)
	}
	// The last hook takes the dispatcher with it.
	applyHooks(t, configurator.ProviderCline, base, resC.NewLock)
	if _, err := os.Stat(clineDispatcherPath(base, "PostToolUse")); !os.IsNotExist(err) {
		t.Errorf("the dispatcher outlived its last hook: %v", err)
	}
}

// A foreign file at the dispatcher path stays the user's even when the hook
// comes from a variant (the base's rule).
func TestVariantHookLeavesAForeignDispatcherAlone(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix path")
	}
	registerTestVariant(t, testVariant(t, "fork-ext", testForkExt, "linux", nil))
	base, _ := clineStubBase(t)
	path := clineDispatcherPath(base, "PostToolUse")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := applyHooks(t, "fork-ext", base, Lock{}, variantHookArtifact("vhook", "PostToolUse"))
	if len(res.Warnings) == 0 {
		t.Error("the unwired hook must be reported")
	}
	if b, _ := os.ReadFile(path); string(b) != "#!/bin/sh\necho mine\n" {
		t.Errorf("the user's file changed: %q", b)
	}
}

// Dispatch enumerates a variant's root only while the variant is registered:
// a root that left the registry is not read even if its files remain.
func TestDispatchFindsAVariantRootOnlyWhileRegistered(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix path")
	}
	base := clineBase(t)
	dir := filepath.Join(base, filepath.FromSlash(variantHookDir), "vhook")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "hook.json"), []byte(`{"event":"PostToolUse","command":"./run.sh"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte("#!/bin/sh\ncat >/dev/null\necho ran >&2\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := dispatch(base, "PostToolUse", `{"postToolUse":{"toolName":"x","parameters":{},"result":"ok","success":true}}`); got.ContextModification != "" {
		t.Fatalf("an unregistered variant's hook ran: %+v", got)
	}
	registerTestVariant(t, testVariant(t, "fork-ext", testForkExt, "linux", nil))
	if got := dispatch(base, "PostToolUse", `{"postToolUse":{"toolName":"x","parameters":{},"result":"ok","success":true}}`); !strings.Contains(got.ContextModification, "ran") {
		t.Fatalf("a registered variant's hook did not run: %+v", got)
	}
	UnregisterVariantCells("fork-ext")
	if got := dispatch(base, "PostToolUse", `{"postToolUse":{"toolName":"x","parameters":{},"result":"ok","success":true}}`); got.ContextModification != "" {
		t.Fatalf("a retired variant's hook ran: %+v", got)
	}
}

// A hook directory that overlaps the base's own hook files is refused like any root.
func TestVariantHookDirOverlappingTheBaseIsRefused(t *testing.T) {
	src := strings.Replace(testForkExt, "{home}/.fork-ext/cartographer-hooks", "{home}/.cline/cartographer-hooks/sub", 1)
	if _, err := RegisterVariantCells(testVariant(t, "fork-ext", src, "linux", nil)); err == nil {
		UnregisterVariantCells("fork-ext")
		t.Fatal("an overlapping hook directory was registered")
	}
}
