// Package contract is the wire contract of provisioning.
//
// It holds what both sides of the wire share: the symlink-free KB tree readers
// and the MCP descriptor schema with its validation. It imports none of kb,
// provisioning, plugin or mcpbridge and writes nothing, so the server can link
// it without linking the code that writes into other people's home directories
// (see ../AGENTS.md).
package contract
