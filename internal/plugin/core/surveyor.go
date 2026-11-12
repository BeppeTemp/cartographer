package core

import "time"

// The surveyor side of a plugin ships recipes (recipes/<name>/) and MCP
// connectors (connectors/<name>.yaml) as data plus the files they run. The
// loaders live in internal/surveyor, which knows the limits of both; this
// package keeps the client import firewall. Nothing here is a path on disk: the
// Surveyor joins the store path of the pinned tree itself.

// SurveyorRecipe is the contribution of an exec recipe folder. Command is a
// slash path inside the recipe folder; Env is never here, because a plugin
// names no operator variable (the manifest's permissions.env does).
type SurveyorRecipe struct {
	Shape        string // feed or system
	Command      string
	Args         []string
	Timeout      time.Duration // 0 = the Surveyor's default
	MaxOutput    int
	MaxContent   int
	Connector    string // a local connector of the plugin, git or fetch; "" = none
	Instructions string
}

// SurveyorConnector is the contribution of connectors/<name>.yaml. Command is a
// slash path inside connectors/ (a file of the tree) or, when CommandOnPath, a
// bare program name looked up on the Surveyor's PATH. Env, TokenEnv and the
// values of Headers are NAMES declared in the manifest's permissions.env.
type SurveyorConnector struct {
	Transport     string
	Command       string
	CommandOnPath bool
	Args          []string
	Env           []string
	URL           string
	TokenEnv      string
	Headers       map[string]string
	Tools         []string
	CallTimeout   time.Duration
	MaxResult     int
}
