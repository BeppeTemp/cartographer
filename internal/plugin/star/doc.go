// Package star runs the Starlark script of a plugin for a client: plugin.star
// at the root and the lib/*.star files it loads. The runtime itself (loading,
// limits, the call, the settings, log, env and http builtins) is
// internal/plugin/starcore, which a server-side caller can link; this package
// adds what only a client has: the command, tool and event kinds
// (kinds.go), the arguments and results of a call (request.go) and the files
// and kb modules. It, with starcore, is the only code that imports
// go.starlark.net (TestStarlarkIsConfined holds it): everything else sees
// Program, Registration and Check.
//
// See the package comment of starcore for the two phases of a script (LOAD and
// CALL) and why the modules are late-bound.
package star
