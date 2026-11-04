package provisioning

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// clineStubBase is a fake home with a stub cartographer binary the dispatchers
// point at; the binary records its arguments and stdin next to itself.
func clineStubBase(t *testing.T) (base, stub string) {
	t.Helper()
	base = t.TempDir()
	stub = filepath.Join(t.TempDir(), "cartographer")
	old := ClineDispatcherCommand
	ClineDispatcherCommand = func() (string, error) { return stub, nil }
	t.Cleanup(func() { ClineDispatcherCommand = old })
	oldTmp := clineTempDir
	tmp := t.TempDir()
	clineTempDir = func() string { return tmp }
	t.Cleanup(func() { clineTempDir = oldTmp })
	return base, stub
}

// addClineHook materializes a hook the way Apply does and registers it.
func addClineHook(t *testing.T, base, name, event, matcher, script string) (warning string, registered bool) {
	t.Helper()
	dir := filepath.Join(base, filepath.FromSlash(clineHookFilesPrefix), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	spec, _ := json.Marshal(hookSpec{Event: event, Matcher: matcher, Command: "./run.sh"})
	if err := os.WriteFile(filepath.Join(dir, "hook.json"), spec, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, w, reg, err := registerClineHook(base, name, dir)
	if err != nil {
		t.Fatal(err)
	}
	return w, reg
}

func TestClineDispatcherGolden(t *testing.T) {
	for _, tc := range []struct {
		file    string
		windows bool
		bin     string
	}{
		{"dispatcher_posix.golden", false, "/opt/it's/bin/cartographer"},
		{"dispatcher_windows.golden.ps1", true, `C:\Users\Zoë\it's\cartographer.exe`},
	} {
		got := string(generateClineDispatcher("PostToolUse", tc.bin, tc.windows))
		path := filepath.Join("testdata", "cline", tc.file)
		if os.Getenv("UPDATE_GOLDEN") != "" {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if want := readFile(t, path); got != want {
			t.Errorf("%s differs:\n%s", tc.file, got)
		}
	}
	// PowerShell also reads the typographic single quotes as quotes: doubled too.
	ps := string(generateClineDispatcher("PostToolUse", "C:\\a\u2018b\u2019c\u201ad\u201be'f\\x.exe", true))
	if !strings.Contains(ps, "'C:\\a\u2018\u2018b\u2019\u2019c\u201a\u201ad\u201b\u201be''f\\x.exe'") {
		t.Errorf("typographic quotes not doubled:\n%s", ps)
	}
	// The Windows form is the only file the loader runs there: <Event>.ps1.
	base, _ := clineStubBase(t)
	pretendWindows(t, true)
	if p := clineDispatcherPath(base, "PreToolUse"); filepath.Base(p) != "PreToolUse.ps1" {
		t.Errorf("windows path = %s", p)
	}
	if !strings.HasPrefix(string(generateClineDispatcher("PreToolUse", "x", true)), "\xef\xbb\xbf") {
		t.Error("the Windows dispatcher has no BOM")
	}
}

func TestClineDocumentsDirIsHomeRelativeForATempBase(t *testing.T) {
	base := t.TempDir()
	if got := clineDocumentsDir(base); got != filepath.Join(base, "Documents") {
		t.Errorf("clineDocumentsDir = %s", got)
	}
}

func TestClineDispatcherOwnership(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec bit")
	}
	base, stub := clineStubBase(t)
	path := clineDispatcherPath(base, "PreToolUse")

	// absent -> created 0755, and Documents survives a prune
	if w, reg := addClineHook(t, base, "g", "PreToolUse", "", "#!/bin/sh\n"); w != "" || !reg {
		t.Fatalf("warning %q registered %v", w, reg)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o755 {
		t.Fatalf("dispatcher: %v %v", fi, err)
	}
	if !strings.Contains(readFile(t, path), stub) {
		t.Error("dispatcher does not call the binary by absolute path")
	}

	// own + changed content -> rewritten, mode kept (the user disabled it in Cline's UI)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(t.TempDir(), "cartographer")
	ClineDispatcherCommand = func() (string, error) { return moved, nil }
	addClineHook(t, base, "g2", "PreToolUse", "", "#!/bin/sh\n")
	if !strings.Contains(readFile(t, path), moved) {
		t.Error("an owned dispatcher was not rewritten")
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, a disabled hook must stay disabled", fi.Mode().Perm())
	}

	// two hooks on one event: one dispatcher, removed only with the last
	if err := removeClineHook(base, "g"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("dispatcher removed while g2 still needs it: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(base, filepath.FromSlash(clineHookFilesPrefix), "g")); err != nil {
		t.Fatal(err)
	}
	if err := removeClineHook(base, "g2"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("dispatcher survived its last hook: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "Documents")); err != nil {
		t.Errorf("Documents was pruned: %v", err)
	}
	if fi, err := os.Stat(clineHooksDir(base)); err != nil || !fi.IsDir() {
		t.Errorf("Cline/Hooks belongs to the extension and must survive: %v", err)
	}
}

func TestClineDispatcherIsNeverAUsersFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	base, _ := clineStubBase(t)
	hooks := clineHooksDir(base)
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	user := "#!/bin/sh\necho mine\n"
	if err := os.WriteFile(filepath.Join(hooks, "PreToolUse"), []byte(user), 0o755); err != nil {
		t.Fatal(err)
	}
	// a directory and a symlink at a path are foreign too
	if err := os.Mkdir(filepath.Join(hooks, "PostToolUse"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(hooks, "PreToolUse"), filepath.Join(hooks, "UserPromptSubmit")); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"PreToolUse", "PostToolUse", "UserPromptSubmit"} {
		w, reg := addClineHook(t, base, "h-"+event, event, "", "#!/bin/sh\n")
		want := "already exists and is not Cartographer's"
		if event == "UserPromptSubmit" {
			want = "refusing to write through symlink" // safewrite's wording, TestClineSymlinkedDispatcherIsReportedAsARefusal
		}
		if reg || !strings.Contains(w, want) || !strings.Contains(w, event) {
			t.Errorf("%s: warning %q registered %v", event, w, reg)
		}
	}
	if got := readFile(t, filepath.Join(hooks, "PreToolUse")); got != user {
		t.Errorf("the user's file was touched: %q", got)
	}
	// prune leaves a file that lost the marker, with no error
	for _, h := range []string{"h-PreToolUse", "h-PostToolUse", "h-UserPromptSubmit"} {
		if err := removeClineHook(base, h); err != nil {
			t.Errorf("prune of %s: %v", h, err)
		}
	}
	if got := readFile(t, filepath.Join(hooks, "PreToolUse")); got != user {
		t.Errorf("prune removed the user's file")
	}
}

func TestClineHooksDirSymlinkIsRefusedBeforeAnyWrite(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	base, _ := clineStubBase(t)
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(base, "Documents", "Cline"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, clineHooksDir(base)); err != nil {
		t.Fatal(err)
	}
	if err := checkHookRegistrationTarget(base, configurator.ProviderCline, "g", ".cline/cartographer-hooks/g"); err == nil {
		t.Error("a symlinked Documents/Cline/Hooks was not refused")
	}
	if _, err := reconcileClineDispatchers(base, ""); err != nil {
		t.Fatal(err) // nothing needed: nothing written, nothing refused here
	}
	dir := filepath.Join(base, filepath.FromSlash(clineHookFilesPrefix), "g")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "hook.json"), []byte(`{"event":"PreToolUse","command":"./x"}`), 0o644)
	w, err := reconcileClineDispatchers(base, "")
	if err != nil || !strings.Contains(w["PreToolUse"], "refusing to write through symlink") {
		t.Errorf("a linked Hooks directory must be a warning, not an error: %v %v", w, err)
	}
	if entries, _ := os.ReadDir(elsewhere); len(entries) != 0 {
		t.Errorf("a write went through the symlink: %v", entries)
	}

	// remove path: an owned-looking dispatcher seen through the link survives, no error
	owned := filepath.Join(elsewhere, "PostToolUse")
	body := "#!/bin/sh\n# " + clineDispatcherMarker + "\n"
	if err := os.WriteFile(owned, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	w, err = reconcileClineDispatchers(base, "")
	if err != nil || w["PostToolUse"] == "" {
		t.Errorf("remove through a linked Hooks directory: %v %v", w, err)
	}
	if got := readFile(t, owned); got != body {
		t.Errorf("the file behind the link was touched: %q", got)
	}
	if err := removeClineHook(base, "g"); err != nil {
		t.Errorf("removeClineHook through a link: %v", err)
	}
	if _, err := os.Stat(owned); err != nil {
		t.Errorf("the file behind the link was removed: %v", err)
	}
}

func TestClineRegisterRefusesWhatClineNeverFires(t *testing.T) {
	base, _ := clineStubBase(t)
	if w, reg := addClineHook(t, base, "stop", "Stop", "", "#!/bin/sh\n"); reg || w == "" {
		t.Errorf("Stop: %q %v", w, reg)
	}
	if w, reg := addClineHook(t, base, "bad", "PreToolUse", "(", "#!/bin/sh\n"); reg || !strings.Contains(w, "not a valid regular expression") {
		t.Errorf("bad matcher: %q %v", w, reg)
	}
	if _, err := os.Stat(clineHooksDir(base)); !os.IsNotExist(err) {
		t.Error("a dispatcher was written for a hook that is not registered")
	}
}

func TestClineEventChangeMovesTheDispatcher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix path")
	}
	base, _ := clineStubBase(t)
	addClineHook(t, base, "g", "PreToolUse", "", "#!/bin/sh\n")
	addClineHook(t, base, "g", "PostToolUse", "", "#!/bin/sh\n") // same hook, new event
	if _, err := os.Stat(clineDispatcherPath(base, "PreToolUse")); !os.IsNotExist(err) {
		t.Errorf("the old dispatcher stayed: %v", err)
	}
	if _, err := os.Stat(clineDispatcherPath(base, "PostToolUse")); err != nil {
		t.Errorf("the new dispatcher is missing: %v", err)
	}
}

func TestClineBootstrapNeedsTheTaskStartDispatcher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix path")
	}
	base, _ := clineStubBase(t)
	if !SupportsSessionHook(configurator.ProviderCline) || SessionHookLimit(configurator.ProviderCline) != "" {
		t.Fatal("Cline fires its bootstrap hook in every task")
	}
	lock, err := EnsureBootstrapHook(base, configurator.ProviderCline, Lock{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, clineDispatcherPath(base, "TaskStart")); !strings.Contains(got, "--event TaskStart") {
		t.Errorf("TaskStart dispatcher: %q", got)
	}
	if SupportsWriteFindingsHook(configurator.ProviderCline) {
		t.Error("write-findings is claimed before probe P2 shows what postToolUse.result carries")
	}
	if _, err := PruneManaged(lock.Managed, base, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(clineDispatcherPath(base, "TaskStart")); !os.IsNotExist(err) {
		t.Errorf("the dispatcher survived the prune: %v", err)
	}
}

// The dispatcher, executed by a real sh against a stub binary: arguments are
// exactly `hook dispatch --provider cline --event X`, the payload arrives
// byte-exact, the exit status is 0, and a missing binary prints {}.
func TestClineDispatcherRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows form runs in clinehooks_windows_test.go")
	}
	base, stub := clineStubBase(t)
	rec := filepath.Join(t.TempDir(), "rec")
	script := "#!/bin/sh\necho \"$@\" > '" + rec + ".args'\ncat > '" + rec + ".in'\necho '{\"cancel\":true}'\nexit 7\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	addClineHook(t, base, "g", "PostToolUse", "", "#!/bin/sh\n")
	d := clineDispatcherPath(base, "PostToolUse")
	payload := `{"taskId":"t","note":"caffè"}`
	cmd := exec.Command(d)
	cmd.Stdin = strings.NewReader(payload)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("exit must be 0: %v", err)
	}
	if strings.TrimSpace(string(out)) != `{"cancel":true}` {
		t.Errorf("stdout = %q", out)
	}
	if got := strings.TrimSpace(readFile(t, rec+".args")); got != "hook dispatch --provider cline --event PostToolUse" {
		t.Errorf("args = %q", got)
	}
	if got := readFile(t, rec+".in"); got != payload {
		t.Errorf("payload = %q", got)
	}
	if err := os.Remove(stub); err != nil {
		t.Fatal(err)
	}
	out, err = exec.Command(d).Output()
	if err != nil || strings.TrimSpace(string(out)) != "{}" {
		t.Errorf("missing binary: %q %v", out, err)
	}
}

// A user's own TaskStart file means the bootstrap hook will never run: the
// bootstrap fails with the warning instead of recording the hook as installed.
func TestClineBootstrapFailsWhenTaskStartIsTheUsers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posix path")
	}
	base, _ := clineStubBase(t)
	hooks := clineHooksDir(base)
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	user := "#!/bin/sh\necho mine\n"
	if err := os.WriteFile(filepath.Join(hooks, "TaskStart"), []byte(user), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := EnsureBootstrapHook(base, configurator.ProviderCline, Lock{}, false)
	if err == nil || !strings.Contains(err.Error(), "not Cartographer's") {
		t.Fatalf("err = %v, want the not-wired warning", err)
	}
	if got := readFile(t, filepath.Join(hooks, "TaskStart")); got != user {
		t.Errorf("the user's file was touched: %q", got)
	}
}

// A symlink at the dispatcher path is a safewrite refusal, in its words.
func TestClineSymlinkedDispatcherIsReportedAsARefusal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	base, _ := clineStubBase(t)
	hooks := clineHooksDir(base)
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "elsewhere"), filepath.Join(hooks, "PreToolUse")); err != nil {
		t.Fatal(err)
	}
	w, reg := addClineHook(t, base, "g", "PreToolUse", "", "#!/bin/sh\n")
	if reg || !strings.Contains(w, "refusing to write through symlink") || strings.Contains(w, "already exists and is not Cartographer's") {
		t.Errorf("warning %q registered %v", w, reg)
	}
}

// The walk is rooted at the Documents folder: a Documents folder that is itself
// a link (a user's own arrangement) is exempt, a link below it is refused.
func TestClineDocumentsRootMayBeALinkButNothingBelowIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	base, _ := clineStubBase(t)
	real := t.TempDir()
	if err := os.Symlink(real, filepath.Join(base, "Documents")); err != nil {
		t.Fatal(err)
	}
	if w, reg := addClineHook(t, base, "g", "PreToolUse", "", "#!/bin/sh\n"); !reg {
		t.Fatalf("a symlinked Documents folder must work: %q", w)
	}
	if _, err := os.Stat(filepath.Join(real, "Cline", "Hooks", "PreToolUse")); err != nil {
		t.Errorf("dispatcher not written through the Documents link: %v", err)
	}
	// A link at Documents/Cline is refused before any write.
	if err := os.RemoveAll(filepath.Join(real, "Cline")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(real, "Cline")); err != nil {
		t.Fatal(err)
	}
	if err := checkHookRegistrationTarget(base, configurator.ProviderCline, "g", ".cline/cartographer-hooks/g"); err == nil || !errors.Is(err, ErrSymlinkDestination) {
		t.Errorf("a link at Documents/Cline must be refused, got %v", err)
	}
}
