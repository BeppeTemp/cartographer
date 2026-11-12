package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/procattr"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// The probes of checkPathBinaries, variables so a test describes the machine
// instead of depending on what happens to be installed on it.
var (
	// pathBinariesFn lists, in PATH order, every executable file named
	// cartographer on PATH.
	pathBinariesFn = pathBinaries
	// binaryVersionFn is the version a binary reports, "" when it cannot say.
	binaryVersionFn = binaryVersion
	// serviceBinaryFn is the program the installed service definition runs, ""
	// when there is none.
	serviceBinaryFn = func() string {
		bin, err := service.NewManager().DefinitionBinary()
		if err != nil {
			return ""
		}
		return bin
	}
)

// binaryVersionTimeout bounds the run of another binary, which doctor does not
// control: a hung copy must not hang the report. binaryVersionWaitDelay is the
// grace after it: a shim can leave a grandchild holding stdout, and without
// WaitDelay Output() would wait for that pipe long after the deadline. Variables
// so a test can pin both.
var (
	binaryVersionTimeout   = 3 * time.Second
	binaryVersionWaitDelay = time.Second
)

// pathBinaries finds every cartographer on PATH. Each directory is asked on its
// own with exec.LookPath, so the platform's rules (the execute bit, PATHEXT
// and the .exe suffix on Windows) decide what counts, not a name match.
func pathBinaries() []string {
	var out []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		if p, err := exec.LookPath(filepath.Join(dir, "cartographer")); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// binaryVersion runs `<path> version`. Any failure is "", never an error: the
// finding then says the version is unknown.
func binaryVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), binaryVersionTimeout)
	defer cancel()
	cmd := procattr.Hide(exec.CommandContext(ctx, path, "version"))
	cmd.WaitDelay = binaryVersionWaitDelay
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// pathCopy is one distinct file among the cartographer binaries on PATH.
type pathCopy struct {
	path     string // as found on PATH (first spelling)
	resolved string // symlinks resolved: what makes two entries one file
	info     os.FileInfo
}

// checkPathBinaries: more than one distinct cartographer on PATH (a Homebrew
// copy and an install.sh one, an old leftover) means the shell may run one and
// the service the other. Symlinks are resolved and a hard link is the same
// file, so Homebrew's stable symlink and its Caskroom target are one binary.
// Reports nothing for a single binary, and runs no binary then.
func checkPathBinaries() []doctorFinding {
	var copies []pathCopy
	for _, p := range pathBinariesFn() {
		resolved, err := filepath.EvalSymlinks(p)
		if err != nil {
			resolved = p
		}
		fi, err := os.Stat(resolved)
		if err != nil {
			continue
		}
		dup := false
		for _, c := range copies {
			dup = dup || c.resolved == resolved || os.SameFile(c.info, fi)
		}
		if !dup {
			copies = append(copies, pathCopy{path: p, resolved: resolved, info: fi})
		}
	}
	if len(copies) < 2 {
		return nil
	}
	svc := serviceBinaryFn()
	var svcInfo os.FileInfo
	if svc != "" {
		if r, err := filepath.EvalSymlinks(svc); err == nil {
			svcInfo, _ = os.Stat(r)
		}
	}
	svcListed := false
	// The copies are asked at once, so the whole report waits for the slowest
	// one (timeout plus WaitDelay), not for the sum of them.
	versions := make([]string, len(copies))
	var wg sync.WaitGroup
	for i, c := range copies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			versions[i] = binaryVersionFn(c.path)
		}()
	}
	wg.Wait()
	parts := make([]string, len(copies))
	for i, c := range copies {
		v := versions[i]
		if v == "" {
			v = "version unknown"
		}
		notes := []string{v}
		if i == 0 {
			notes = append(notes, "the one the shell runs")
		}
		if svcInfo != nil && os.SameFile(svcInfo, c.info) {
			notes = append(notes, "the one the service runs")
			svcListed = true
		}
		parts[i] = fmt.Sprintf("%s (%s)", escapeControl(c.path), strings.Join(notes, ", "))
	}
	msg := fmt.Sprintf("%d different cartographer binaries on PATH: %s", len(copies), strings.Join(parts, "; "))
	if svc != "" && !svcListed {
		msg += fmt.Sprintf("; the service runs %s, which is not among them", escapeControl(svc))
	}
	return []doctorFinding{{
		Check: "path-binaries", Severity: doctorWarning, Path: copies[0].path,
		Message: msg,
		Fix:     "keep one install (remove the others from PATH or from disk), then `cartographer upgrade-repair` so the service runs it",
	}}
}
