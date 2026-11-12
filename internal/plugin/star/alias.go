package star

import "github.com/BeppeTemp/cartographer/internal/plugin/starcore"

// What the runtime (internal/plugin/starcore) defines and a client of this
// package sees under the old names: the kinds, the errors, the limits.
type (
	// Registration is one contribution the script made while loading.
	Registration = starcore.Registration
	// File is a script file of the tree.
	File = starcore.File
	// ErrKind is how a call failed.
	ErrKind = starcore.ErrKind
	// CallError is what Call returns when the call did not succeed.
	CallError = starcore.CallError
	// LoadError is a script that does not load, with the position of the failure.
	LoadError = starcore.LoadError
)

// The kinds of failure of a call.
const (
	Failed        = starcore.Failed
	Denied        = starcore.Denied
	LimitExceeded = starcore.LimitExceeded
	Cancelled     = starcore.Cancelled
	Internal      = starcore.Internal
)

// The caps of a call, which the tests and the CLI name.
const (
	MaxLoadSteps         = starcore.MaxLoadSteps
	MaxCallStepsTool     = starcore.MaxCallStepsTool
	MaxCallStepsCommand  = starcore.MaxCallStepsCommand
	MaxResultBytes       = starcore.MaxResultBytes
	MaxPrintBytes        = starcore.MaxPrintBytes
	MaxHTTPRequests      = starcore.MaxHTTPRequests
	MaxHTTPResponseBytes = starcore.MaxHTTPResponseBytes
	MaxHTTPRequestBytes  = starcore.MaxHTTPRequestBytes
	MaxHTTPRedirects     = starcore.MaxHTTPRedirects
	HTTPTimeout          = starcore.HTTPTimeout
	MaxRegistrationText  = starcore.MaxRegistrationText
	MaxFileBytes         = starcore.MaxFileBytes
	MaxGlobResults       = starcore.MaxGlobResults
	MaxGlobVisits        = starcore.MaxGlobVisits
	ToolTimeout          = starcore.ToolTimeout
	EventTimeout         = starcore.EventTimeout
)
