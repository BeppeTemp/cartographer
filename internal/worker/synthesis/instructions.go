package synthesis

import _ "embed"

// Instructions is the system prompt of the synthesis task, embedded in the
// binary (D15): instructions the worker follows come only from a trusted
// source, never from the KB or a config path.
//
//go:embed instructions.md
var Instructions string
