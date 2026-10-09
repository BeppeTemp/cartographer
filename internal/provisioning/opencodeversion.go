package provisioning

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
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
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	return string(out), err
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
// (not on PATH, probe failed). Unknown keeps the 1.x shape: it is what every
// release before this one wrote, so an undetectable client changes nothing.
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
