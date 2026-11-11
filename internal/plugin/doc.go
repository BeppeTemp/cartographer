// Package plugin is the client side of a Cartographer plugin: the loaders and
// previews of the skill, agent, hook and provider points, the acquisition of a
// source (folder or git repository), the diff of two trees and the provider
// cells handed to provisioning. The data it works on (manifest, tree reader,
// pinned store, point registry) lives in internal/plugin/core, which the
// server may link; this package may not be linked by the server.
package plugin
