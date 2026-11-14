package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// exeFile creates a cartographer executable in a fresh directory and returns
// its path. On Windows the name carries .exe, which PATHEXT makes runnable.
func exeFile(t *testing.T, content string) string {
	t.Helper()
	name := "cartographer"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// pathStubs describes the machine: the cartographer files on PATH, the version
// each one reports and the program the service runs.
func pathStubs(t *testing.T, onPath []string, versions map[string]string, service string) *[]string {
	t.Helper()
	oPath, oVer, oSvc := pathBinariesFn, binaryVersionFn, serviceBinaryFn
	t.Cleanup(func() { pathBinariesFn, binaryVersionFn, serviceBinaryFn = oPath, oVer, oSvc })
	var (
		mu    sync.Mutex // the copies are probed in parallel
		asked []string
	)
	pathBinariesFn = func() []string { return onPath }
	binaryVersionFn = func(p string) string {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, p)
		return versions[p]
	}
	serviceBinaryFn = func() string { return service }
	return &asked
}

// Two different files on PATH are one warning that names each with its version,
// says which one the shell runs and which one the service runs.
func TestCheckPathBinaries_TwoCopiesWarn(t *testing.T) {
	a, b := exeFile(t, "brew"), exeFile(t, "script")
	pathStubs(t, []string{a, b}, map[string]string{a: "0.30.1", b: "0.29.0"}, b)

	got := checkPathBinaries()
	if len(got) != 1 {
		t.Fatalf("findings = %+v, want one", got)
	}
	f := got[0]
	if f.Check != "path-binaries" || f.Severity != doctorWarning {
		t.Errorf("finding = %+v, want a path-binaries warning", f)
	}
	for _, want := range []string{
		a + " (0.30.1, the one the shell runs)",
		b + " (0.29.0, the one the service runs)",
		"2 different cartographer binaries",
	} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message %q lacks %q", f.Message, want)
		}
	}
	if strings.Contains(f.Message, "which is not among them") {
		t.Errorf("the service binary is on PATH, message = %q", f.Message)
	}
}

// A service that runs a binary outside PATH is named; a copy whose version
// cannot be read says so.
func TestCheckPathBinaries_ServiceOffPathAndUnknownVersion(t *testing.T) {
	a, b, svc := exeFile(t, "one"), exeFile(t, "two"), exeFile(t, "three")
	pathStubs(t, []string{a, b}, map[string]string{a: "0.30.1"}, svc)

	got := checkPathBinaries()
	if len(got) != 1 {
		t.Fatalf("findings = %+v, want one", got)
	}
	for _, want := range []string{b + " (version unknown)", "the service runs " + svc + ", which is not among them"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("message %q lacks %q", got[0].Message, want)
		}
	}
}

// One binary, reached through two PATH entries or through a symlink (Homebrew's
// stable link and its Caskroom target), is one binary: no finding, and no other
// binary is run to ask its version.
func TestCheckPathBinaries_SameFileIsOneBinary(t *testing.T) {
	real := exeFile(t, "real")
	link := filepath.Join(t.TempDir(), filepath.Base(real))
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	asked := pathStubs(t, []string{link, real, real}, nil, real)

	if got := checkPathBinaries(); len(got) != 0 {
		t.Fatalf("findings = %+v, want none", got)
	}
	if len(*asked) != 0 {
		t.Errorf("a version was asked for with a single binary: %v", *asked)
	}
}

func TestCheckPathBinaries_NoneOrOneIsSilent(t *testing.T) {
	pathStubs(t, nil, nil, "")
	if got := checkPathBinaries(); len(got) != 0 {
		t.Errorf("no binary: findings = %+v", got)
	}
	pathStubs(t, []string{exeFile(t, "only")}, nil, "")
	if got := checkPathBinaries(); len(got) != 0 {
		t.Errorf("one binary: findings = %+v", got)
	}
}

// pathBinaries asks each PATH directory on its own, with the platform's rules
// for what is runnable: an empty directory entry and a directory without the
// file are skipped, a file without the execute bit is not a command.
func TestPathBinaries_OnePerDirectory(t *testing.T) {
	a, c := exeFile(t, "a"), exeFile(t, "c")
	empty := t.TempDir()
	dirs := []string{filepath.Dir(a), empty, "", filepath.Dir(c)}
	if runtime.GOOS != "windows" {
		noExec := exeFile(t, "plain")
		if err := os.Chmod(noExec, 0o644); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, filepath.Dir(noExec))
	}
	t.Setenv("PATH", strings.Join(dirs, string(os.PathListSeparator)))

	got := pathBinaries()
	if len(got) != 2 || got[0] != a || got[1] != c {
		t.Errorf("pathBinaries() = %v, want [%s %s]", got, a, c)
	}
}

// binaryVersion reads what `version` prints; a program that fails says nothing.
func TestBinaryVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a shell script")
	}
	ok := exeFile(t, "#!/bin/sh\necho 1.2.3\n")
	if got := binaryVersion(ok); got != "1.2.3" {
		t.Errorf("binaryVersion = %q, want 1.2.3", got)
	}
	bad := exeFile(t, "#!/bin/sh\nexit 1\n")
	if got := binaryVersion(bad); got != "" {
		t.Errorf("binaryVersion of a failing program = %q, want empty", got)
	}
}

// The check is part of the report, a warning that makes the exit code 1.
func TestRunDoctor_ReportsASecondBinaryOnPath(t *testing.T) {
	doctorStubs(t, []string{"claude"}, false)
	dir := doctorFixture(t, "claude")
	pathStubs(t, []string{exeFile(t, "a"), exeFile(t, "b")}, nil, "")

	report := runDoctor(dir, "")
	if got := findingsFor(report, "path-binaries"); len(got) != 1 || got[0].Severity != doctorWarning {
		t.Fatalf("path-binaries findings = %+v, want one warning", got)
	}
	if code := doctorExitCode(report); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
}

// The timeout bounds the probe even when the copy leaves a grandchild holding
// its stdout: without WaitDelay, Output() waited for that pipe, not for the
// deadline (12 s were seen). Several hung copies are probed at once, so the
// report waits for one bound, not for their sum.
func TestBinaryVersion_BoundedByAGrandchildHoldingStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell script: a background child that keeps stdout open")
	}
	oT, oW := binaryVersionTimeout, binaryVersionWaitDelay
	t.Cleanup(func() { binaryVersionTimeout, binaryVersionWaitDelay = oT, oW })
	binaryVersionTimeout, binaryVersionWaitDelay = 200*time.Millisecond, 200*time.Millisecond

	// The shim hangs, and its background child keeps the pipe open well past the bound.
	hung := "#!/bin/sh\nsleep 5 &\nsleep 5\n"
	copies := []string{exeFile(t, hung), exeFile(t, hung), exeFile(t, hung)}

	start := time.Now()
	var wg sync.WaitGroup
	got := make([]string, len(copies))
	for i, p := range copies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i] = binaryVersion(p)
		}()
	}
	wg.Wait()
	if elapsed, limit := time.Since(start), binaryVersionTimeout+binaryVersionWaitDelay+2*time.Second; elapsed > limit {
		t.Errorf("binaryVersion of hung copies took %v, want under %v", elapsed, limit)
	}
	for i, v := range got {
		if v != "" {
			t.Errorf("copy %d reported %q, want empty (version unknown)", i, v)
		}
	}
}

// checkPathBinaries probes the copies together: every probe waits until all
// three have started, so a serial loop never gets past the first one. The test
// asserts concurrency, not elapsed time, which a loaded host stretches.
func TestCheckPathBinaries_ProbesInParallel(t *testing.T) {
	a, b, c := exeFile(t, "a"), exeFile(t, "b"), exeFile(t, "c")
	pathStubs(t, []string{a, b, c}, map[string]string{a: "1", b: "2", c: "3"}, "")
	slow := binaryVersionFn
	var inFlight atomic.Int32
	all := make(chan struct{})
	var serial atomic.Bool
	binaryVersionFn = func(p string) string {
		if inFlight.Add(1) == 3 {
			close(all)
		}
		select {
		case <-all:
		case <-time.After(5 * time.Second):
			serial.Store(true)
		}
		return slow(p)
	}

	got := checkPathBinaries()
	if serial.Load() {
		t.Error("the three probes never ran together, probes are serial")
	}
	if len(got) != 1 || !strings.Contains(got[0].Message, a+" (1,") || !strings.Contains(got[0].Message, c+" (3)") {
		t.Errorf("findings = %+v, want each copy with its own version", got)
	}
}
