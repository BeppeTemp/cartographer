// Package atlasext loads the data a plugin's atlas side declares: views/ (a
// read-only view of a public API route, drawn by the Atlas with one of four
// fixed renderers) and apps/ (an entry that opens an app on its own origin in a
// new tab). Both are data only: nothing a plugin ships runs in the Atlas, and
// no string from a declaration or from the data it reads is interpreted as
// markup, script or a link. It imports internal/plugin/core (and the provisioning contract it
// builds on) and no data plane;
// whether a view's route is a documented public operation is asked, by
// Validate, of a RouteSet the server provides; loading checks only its shape.
package atlasext
