// Package starcore is the server-safe Starlark runtime of a plugin script. It is
// the runtime without the half that only a client needs: the program loader and runner, the
// registrar mechanism (a host adds the kinds of registration), Script.Invoke
// with its limits, and the settings, log, env and http builtins. It imports
// internal/plugin/core and the interpreter and nothing that writes into other
// people's home directories (internal/provisioning, internal/plugin) or reads
// the server's configuration, so a server-side caller such as the Surveyor can
// link it (internal/boundaries holds it). Package star builds the command,
// tool and event kinds and the files and kb modules on top of it.
//
// Together with package star it is the only code that imports go.starlark.net
// (TestStarlarkIsConfined holds it).
//
// A script runs in two phases. LOAD runs plugin.star once, with only the
// registration builtins (command, tool, on), the pure modules (json, math, and
// time without now) and capability modules that refuse every call, and reports
// what the script registered; what a user approves is a pure function of the
// tree, so LOAD reads no clock, no setting and no file. CALL runs a registered
// function on demand, on a fresh thread, under step and time caps, with typed
// errors (CallError) and the settings of the call.
//
// Starlark binds the names of a function when the function is created, so the
// modules are values whose behavior is decided at the call, from the context
// kept in the running thread (callCtx): a function defined while loading must
// see the CALL-time context when it runs. A thread-less attribute read
// (settings.x) cannot do that: see settingsValue.
package starcore
