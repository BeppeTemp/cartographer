// Package pluginserver loads the data families of a plugin's server side:
// roles/ (role templates the operator maps onto KBs) and presets/ (a map
// contract and its templates, handed out by preset_list). It is the server-side
// half of internal/plugin, which cannot reach the data plane: the loaders judge
// roles with the config grammar and presets with the validators of the tools
// that will apply them. It never loads code: a tree with an executable file or
// a file outside the two shapes is invalid.
package pluginserver
