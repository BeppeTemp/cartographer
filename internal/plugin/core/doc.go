// Package core is the half of the plugin system the server and the Surveyor
// link: the manifest and its version ranges, the families and the
// contribution-point registry, the reader and digest of an in-memory tree, the
// pinned store and the provider descriptor with its resolver. It is pure data
// checking and touches disk only to read a stored or local tree.
//
// It must never import internal/provisioning, internal/plugin or
// internal/mcpbridge, directly or through any dependency: those write into
// other people's home directories, and the server may not link them (D1884).
// TestServerNeverLinksTheApply in internal/boundaries pins it; a client-only
// import creeping into a file of this package fails there. Client loaders
// register their points with Register from internal/plugin's init; ReadTree
// classifies by family, so its result does not depend on which are linked.
package core
