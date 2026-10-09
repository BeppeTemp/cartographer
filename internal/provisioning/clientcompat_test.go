package provisioning

// clientcompat_test.go (D192 WP5) — the alarm for a divergence between the path
// Cartographer writes and the path the client actually reads.
//
// One destination is known to differ from what the provider documents, proven
// working against the real client: Codex skills under .codex/skills
// (documented: $HOME/.agents/skills). It is a path the vendor no longer
// presents as canonical, so it can break on a client release with nothing in CI
// to catch it. (OpenCode's former divergence, .opencode/agent, did break on 2.x
// and was moved to its config directory, D360.)
//
// These tests assert the **declared destination against the client's own
// discovery output**, never a hardcoded path — so they survive D193 moving the
// Codex skill destination to the repository scope. They **skip** when the client
// binary is absent, so CI and a contributor's machine without those clients stay
// green: the signal is for the machine that has the client installed.

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/agents"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// discoveryTimeout bounds a client's own discovery command. These clients are
// interactive tools: one of them can sit waiting for a TTY, and a test suite
// must not hang on that.
const discoveryTimeout = 20 * time.Second

// clientInstalled reports whether internal/agents detects provider on this
// machine — the same answer `cartographer agents` gives, rather than a second
// detection heuristic.
func clientInstalled(provider configurator.Provider) bool {
	for _, a := range agents.Detect() {
		if a.Provider == provider {
			return a.Installed
		}
	}
	return false
}

// runDiscovery executes a client's own discovery command and returns its
// output. It **skips** the test when the command cannot answer — it errored,
// timed out, or printed nothing — because that is not evidence of a path
// divergence, and turning an unrelated client problem into a red suite would
// teach everyone to ignore this test. Only a successful run with output that
// does not mention the declared directory is a real signal. Either way the
// message names the exact command, so a change in its output format is obvious
// rather than mysterious.
func runDiscovery(t *testing.T, name string, args ...string) string {
	t.Helper()
	cmdline := name + " " + strings.Join(args, " ")

	ctx, cancel := context.WithTimeout(context.Background(), discoveryTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if ctx.Err() != nil {
		t.Skipf("`%s` did not answer within %s: cannot check the destination this way", cmdline, discoveryTimeout)
	}
	if err != nil {
		t.Skipf("`%s` failed (%v): cannot check the destination this way\n%s", cmdline, err, out)
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Skipf("`%s` printed nothing on this client version: cannot check the destination this way", cmdline)
	}
	return string(out)
}

func TestCodexDiscoversDeclaredSkillDestination(t *testing.T) {
	if !clientInstalled(configurator.ProviderCodex) {
		t.Skip("codex is not installed on this machine")
	}
	dest := destDir("skill", "probe", configurator.ProviderCodex)
	if dest == "" {
		t.Skip("codex declares no skill destination")
	}
	// The declared directory is the one whose name must appear in the client's
	// own catalogue output. Compare on the directory, not the full path: the
	// client prints its own absolute form.
	dir := strings.SplitN(filepath.ToSlash(dest), "/", 2)[0]
	out := runDiscovery(t, "codex", "debug", "prompt-input")
	if !strings.Contains(out, dir) {
		t.Errorf("`codex debug prompt-input` does not mention %q, the directory Cartographer writes skills into.\n"+
			"Either the client changed its discovery path or its output format did — check https://developers.openai.com/codex/skills", dir)
	}
}

// TestOpenCodeDeclaresItsConfigDirectory asserts the global skill and agent
// cells sit under the `config` directory `opencode debug paths` reports
// (D360). It FAILS, not skips, when the binary is present but the command
// errors or has no `config` line: the former skip-on-error is how 2.x silently
// stopped loading `.opencode/agent` unnoticed.
func TestOpenCodeDeclaresItsConfigDirectory(t *testing.T) {
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode is not installed on this machine")
	}
	ctx, cancel := context.WithTimeout(context.Background(), discoveryTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "opencode", "debug", "paths").CombinedOutput()
	if err != nil {
		t.Fatalf("`opencode debug paths` failed (%v): the declared destinations cannot be checked\n%s", err, out)
	}
	var home, config string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "home" {
			home = f[1]
		}
		if len(f) == 2 && f[0] == "config" {
			config = f[1]
		}
	}
	if config == "" || home == "" {
		t.Fatalf("`opencode debug paths` printed no home/config line: its output format changed\n%s", out)
	}
	for _, kind := range []string{"skill", "agent"} {
		dest := destDir(kind, "probe", configurator.ProviderOpenCode)
		if dest == "" {
			t.Errorf("opencode declares no global %s destination", kind)
			continue
		}
		full := filepath.Join(home, filepath.FromSlash(dest))
		if full != config && !strings.HasPrefix(full, config+string(filepath.Separator)) {
			t.Errorf("global %s destination %s is not under OpenCode's config directory %s (`opencode debug paths`)", kind, full, config)
		}
	}
}
