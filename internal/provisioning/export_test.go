package provisioning

import (
	"os"
	"testing"
)

// TestMain pins the OpenCode probe to "not installed" so no test depends on the
// client present on the machine running it (D359).
func TestMain(m *testing.M) {
	openCodeVersionProbe = func() (string, error) { return "", os.ErrNotExist }
	os.Exit(m.Run())
}

// Internals the external test package (provisioning_test, same directory) needs
// to state a platform-dependent expectation without hard-coding one platform's
// answer.

// BootstrapScriptNameForTest is the bootstrap hook's generated script file name
// on this GOOS (D216 WP2): bootstrap.sh on unix, bootstrap.cmd on Windows. A test
// that writes the literal "bootstrap.sh" is a test that only passes on unix, and
// on the other leg it fails for a reason that looks like a missing file.
const BootstrapScriptNameForTest = bootstrapScriptName

// BootstrapScriptContentForTest is that script's body, so a test can assert its
// three guarantees (silent, always exit 0, exits when cartographer is not
// resolvable) per platform without pinning the exact text.
const BootstrapScriptContentForTest = bootstrapScriptContent

// WriteFindingsScriptNameForTest and WriteFindingsScriptContentForTest are the
// write-findings hook's platform script (D353), for the same reason as above.
const WriteFindingsScriptNameForTest = writeFindingsScriptName
const WriteFindingsScriptContentForTest = writeFindingsScriptContent

// SetOpenCodeVersionForTest makes the generator see an installed OpenCode of
// the given `--version` output (empty: not installed); the returned func
// restores the real probe.
func SetOpenCodeVersionForTest(out string) func() {
	prev := openCodeVersionProbe
	openCodeVersionProbe = func() (string, error) {
		if out == "" {
			return "", errOpenCodeAbsent
		}
		return out, nil
	}
	return func() { openCodeVersionProbe = prev }
}

var errOpenCodeAbsent = os.ErrNotExist
