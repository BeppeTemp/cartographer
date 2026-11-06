package star

// Limits of one run, named in one place. Memory is not bounded by Starlark: a
// hostile script can exhaust its own process, and the tree reader loads it
// before any approval, so it is bounded in CPU (MaxLoadSteps) but not in memory:
// a denial of service of the user's own client, accepted for now (the decision
// D1746, follow-up #1741). A plugin is trusted only as
// far as it was approved; ReadTree runs the scripts only on an otherwise valid
// tree, and sync compares the digest first.
const (
	// MaxLoadSteps caps the interpreter steps of loading plugin.star and each
	// lib file, so a top-level script that never ends fails the load.
	MaxLoadSteps uint64 = 20_000_000
	// MaxRegistrationText caps a help, usage or description string.
	MaxRegistrationText = 1024
)
