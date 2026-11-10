package contract

// BootstrapHookName is the reserved hook name for the client-generated bootstrap
// hook (D60). A KB that defines a hook with this exact name collides with it:
// Apply ignores that KB artifact with a warning (see the reserved-name check in
// Apply) rather than letting it shadow the files EnsureBootstrapHook manages.
const BootstrapHookName = "cartographer-bootstrap"
