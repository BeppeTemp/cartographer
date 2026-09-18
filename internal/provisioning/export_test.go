package provisioning

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
