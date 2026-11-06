// Package plugin reads and checks a Cartographer plugin: a folder or git
// repository with a cartographer-plugin.yaml manifest at its root. This part is
// pure data checking: the manifest, the version ranges it speaks and nothing
// that touches disk, git or the network.
package plugin
