// Package star runs the Starlark script of a plugin: plugin.star at the root
// and the lib/*.star files it loads. It is the only package that imports
// go.starlark.net (TestStarlarkIsConfined holds it): everything else sees
// Program, Registration and Check.
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
package star
