package star

// Limits of one run, named in one place. Memory is not bounded by Starlark: a
// hostile script can exhaust its own process, which is why a plugin is trusted
// only as far as it was approved.
const (
	// MaxLoadSteps caps the interpreter steps of loading plugin.star and each
	// lib file, so a top-level script that never ends fails the load.
	MaxLoadSteps uint64 = 20_000_000
	// MaxRegistrationText caps a help, usage or description string.
	MaxRegistrationText = 1024
)
