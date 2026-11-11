package core

import "encoding/json"

// The server side of a plugin is data only: a role template and a preset. The
// loaders live in internal/pluginserver, which may reach the data plane to
// validate what they read; this package keeps the client import firewall.

// Role is the contribution of roles/<name>.yaml: allow rules with no KB, which
// the operator maps onto KBs of the deployment (auth.roles[].from, kbs).
type Role struct {
	Description string
	Rules       []RoleRule
}

// RoleRule is one allow rule of a role template; the fields are those of
// config.RuleSpec minus KB (a plugin does not know the deployment's KB names).
type RoleRule struct {
	Access             string
	Maps, Journals     []string
	Types              []string
	Artifacts, Secrets string
	// Declared records the selector keys the file wrote ("maps", "journals",
	// "types"), so a key present but empty is judged as the operator's is.
	Declared map[string]bool
}

// Preset is the contribution of presets/<name>/: the arguments of map_create
// (minus name) and the templates its contract names, handed to an authorised
// writer by preset_list. It carries no KB data.
type Preset struct {
	Description string
	Kind        string
	Create      json.RawMessage // the map_create arguments, minus name
	Templates   []PresetTemplate
}

// PresetTemplate is a template of a preset: a plugin-local slug and its file.
type PresetTemplate struct {
	Slug    string
	Content []byte
}
