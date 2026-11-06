package star

import "time"

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

	// Per-call step caps: a command is the user's own run, a tool or an event
	// is a bridge request or a hook.
	MaxCallStepsTool    uint64 = 20_000_000
	MaxCallStepsCommand uint64 = 500_000_000
	// MaxPrintBytes caps what print and log write in one call; the rest is
	// dropped and a "truncated" line ends the output.
	MaxPrintBytes = 64 << 10
	// MaxResultBytes caps the text a tool or command returns.
	MaxResultBytes = 1 << 20
)

// Wall clock of one call; a command has none (it ends when done or on Ctrl-C).
// A caller's context can only shorten them.
const (
	ToolTimeout  = 30 * time.Second
	EventTimeout = 10 * time.Second
)

// Network caps of one call (http.request).
const (
	MaxHTTPResponseBytes = 8 << 20
	MaxHTTPRequestBytes  = 4 << 20
	MaxHTTPRequests      = 50
	MaxHTTPRedirects     = 10
	// HTTPTimeout is the default for one request; the call's own deadline can
	// only shorten it.
	HTTPTimeout = 30 * time.Second
)

// File caps of one call (files.*).
const (
	// MaxFileBytes caps one files.read and one files.write.
	MaxFileBytes = 8 << 20
	// MaxGlobResults caps what files.glob returns; the walk stops there.
	MaxGlobResults = 1000
	// MaxGlobVisits caps the directory entries one files.glob looks at.
	MaxGlobVisits = 100_000
	maxGlobDepth  = 32
)
