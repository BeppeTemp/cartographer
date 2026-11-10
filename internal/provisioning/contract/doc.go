// Package contract is the wire contract of provisioning.
//
// It holds what both sides of the wire share: the manifest and its builder
// (BuildManifest, ReadArtifactFiles), the kinds and the destination matrix, the
// client capability table, the projection resolver, the guide and instruction
// renderers, the requirement and hook validation, the symlink-free KB tree
// readers and the MCP descriptor schema. It imports none of kb, provisioning,
// plugin or mcpbridge and writes nothing, so the server can link it without
// linking the code that writes into other people's home directories (see
// ../AGENTS.md). Some names are exported only because the apply half, one
// package up, needs them (PluginRegistry, CellFor, HookSpec…): they are not API.
package contract
