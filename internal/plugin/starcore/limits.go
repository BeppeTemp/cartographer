package starcore

import "time"

// Limits of one run, named in one place. Starlark has no allocation cap, and
// the tree reader loads a script before any approval, so memory is bounded by
// a watchdog (watchHeap, D1746, #1741): while a thread runs it samples the live
// heap and cancels the thread when the heap has grown past MaxHeapGrowth over
// its value at the start (one baseline for a whole call, its re-run of
// plugin.star included). It is approximate by design, in both directions: the
// metric counts garbage not yet swept and the allocations of every other
// goroutine, so it can fire on a script that did little in a process with a
// large or busy heap, and one huge allocation between two samples can pass the
// cap. It bounds the common case, a script that keeps allocating, not a single
// hostile allocation. A plugin is trusted only as far
// as it was approved; ReadTree runs the scripts only on an otherwise valid
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
	// MaxCallStepsRecipe caps one verb call of a Surveyor recipe: it pages
	// through a source, so it gets more than a tool.
	MaxCallStepsRecipe uint64 = 100_000_000
	// MaxPrintBytes caps what print and log write in one call; the rest is
	// dropped and a "truncated" line ends the output.
	MaxPrintBytes = 64 << 10
	// MaxResultBytes caps the text a tool or command returns.
	MaxResultBytes = 1 << 20
)

// MaxHeapGrowth is how far the live heap may grow during one run (a load, or the
// thread of a call) before it is cancelled. HeapSampleEvery is how often the
// watchdog looks. Variables, not constants, so a test can use a small cap.
var (
	MaxHeapGrowth   uint64 = 256 << 20
	HeapSampleEvery        = 5 * time.Millisecond
)

// Wall clock of one call; a command has none (it ends when done or on Ctrl-C).
// A caller's context can only shorten them.
const (
	ToolTimeout  = 30 * time.Second
	EventTimeout = 10 * time.Second
	// RecipeTimeout is one verb call of a Surveyor recipe.
	RecipeTimeout = 5 * time.Minute
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
	MaxGlobDepth  = 32
)
