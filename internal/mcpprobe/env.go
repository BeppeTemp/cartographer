// Package mcpprobe checks an MCP descriptor without launching it:
// which ${VAR} references are unset, whether its host can be reached, and which
// of the usual network and TLS faults an error is. It prints and writes nothing;
// the command in cmd/cartographer shows the Result. A value is never part of it.
package mcpprobe

// Unresolved returns the names in refs that lookup reports unset or empty, in
// the order given. lookup is os.LookupEnv for the real command.
//
// This is the environment of the process that runs the probe. A client started
// from the dock or from an older shell may have another one, which is the fault
// the probe exists to explain: the report says so.
func Unresolved(refs []string, lookup func(string) (string, bool)) []string {
	var out []string
	for _, r := range refs {
		if v, ok := lookup(r); !ok || v == "" {
			out = append(out, r)
		}
	}
	return out
}
