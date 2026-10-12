package provisioning

import (
	"bytes"
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// openCodeShapeMarker precedes the plugin shape's major version in the header
// of every generated plugin, so doctor can tell which loader a file was written
// for without parsing JavaScript (D359).
const openCodeShapeMarker = "cartographer:opencode-plugin-shape="

// openCodeVersionProbe returns the installed OpenCode's `--version` output.
// A variable so tests state a version without a client on PATH.
var openCodeVersionProbe = func() (string, error) {
	path, err := exec.LookPath("opencode")
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--version")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	return pickVersionOutput(string(out), stderr.String()), err
}

// pickVersionOutput prefers stdout and falls back to stderr when stdout
// carries no version (a client may print it on either stream, D719).
func pickVersionOutput(stdout, stderr string) string {
	if parseOpenCodeMajor(stdout) > 0 {
		return stdout
	}
	if parseOpenCodeMajor(stderr) > 0 {
		return stderr
	}
	return stdout
}

// openCodeVersionUnreadableMarker is in the sync warning for a guessed shape
// (D719). It is informational: the bootstrap hook must keep being written
// (bootstrap.go treats other OpenCode warnings as fatal), so it is matched there.
const openCodeVersionUnreadableMarker = "OpenCode version unreadable"

// openCodeLatestShape is the newest plugin shape the generator knows: the
// highest HookPluginMajors entry of the OpenCode descriptor, 1 if absent.
func openCodeLatestShape() int {
	latest := 1
	if d, ok := configurator.Lookup(configurator.ProviderOpenCode); ok {
		for _, m := range d.HookPluginMajors {
			if m > latest {
				latest = m
			}
		}
	}
	return latest
}

var openCodeVersionRE = regexp.MustCompile(`(\d+)\.\d+`)

// parseOpenCodeMajor extracts the major version from `opencode --version`
// output ("2.0.20", "opencode 1.18.34"); 0 when none is found.
func parseOpenCodeMajor(out string) int {
	m := openCodeVersionRE.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// openCodeMajor is the installed OpenCode's major version, 0 when unknown
// (not on PATH, probe failed). The generator maps unknown to the newest known
// shape (openCodeLatestShape, D719) because OpenCode 2.x rejects a 1.x plugin
// silently; write-findings does NOT follow that fallback: it stays gated on a
// version actually read as >= 2 (SupportsWriteFindingsHook, D362).
func openCodeMajor() int {
	out, err := openCodeVersionProbe()
	if err != nil {
		return 0
	}
	return parseOpenCodeMajor(out)
}

// OpenCodeMajor exposes the probe to doctor.
func OpenCodeMajor() int { return openCodeMajor() }

var openCodeShapeRE = regexp.MustCompile(`cartographer:opencode-plugin-shape=(\d+)`)

// OpenCodePluginShape returns the shape major recorded in a generated plugin's
// content; a file written before D359 carries no marker and is shape 1.
func OpenCodePluginShape(content string) int {
	m := openCodeShapeRE.FindStringSubmatch(content)
	if m == nil {
		return 1
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// OpenCodePluginRelPath is a hook's generated plugin path relative to the base
// directory, in slash form.
func OpenCodePluginRelPath(hookName string) string { return openCodePluginRelPath(hookName) }
