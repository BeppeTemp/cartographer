package clientconfig

import (
	"github.com/BeppeTemp/cartographer/internal/defaults"
	"github.com/BeppeTemp/cartographer/internal/settingschema"
)

// Schema is the key table of the client .cartographer.yaml (D747): every key a
// person owns. The server-owned caches (known KBs, mount mode, routed path) are
// not settings and live in the state file, so they are not here.
// TestSchemaMatchesYAMLStruct fails when a key is added to the file without an
// entry in this table, or an entry outlives its key: a new setting starts here.
var Schema = settingschema.Schema{Keys: []settingschema.Key{
	{Path: "server_url", Scope: settingschema.ScopeClient, Type: "string", Default: defaults.DefaultMCPURL,
		Description: "MCP endpoint of the server this machine talks to. The first connect seeds it from CARTOGRAPHER_SERVER_URL when that is set."},
	{Path: "server_name", Scope: settingschema.ScopeClient, Type: "string", Default: "cartographer",
		Description: "Name under which the server is registered in each agent's MCP configuration."},
	{Path: "auth", Scope: settingschema.ScopeClient, Type: "bool", Default: "false",
		Description: "The server requires a bearer token; the clients then send the token."},
	{Path: "token", Scope: settingschema.ScopeClient, Type: "string", Default: "${CARTOGRAPHER_TOKENS}",
		Description: "The bearer token: ${NAME} reads it from the environment variable NAME (an unattended run falls back to the private token file `connect` wrote), anything else is the token itself, and the file is then kept private (mode 0600). The agents' MCP entries can only name a variable, so a literal token has them read $CARTOGRAPHER_TOKENS."},
	{Path: "clients", Scope: settingschema.ScopeClient, Type: "map", Default: "{}",
		Description: "Which KBs each connected provider receives, as <provider>: {kbs: [...]}. A provider with no entry, or an entry without kbs, receives every known KB. clients.claude.config_dirs lists extra Claude Code config directories (CLAUDE_CONFIG_DIR) whose .claude.json also receives the MCP entry. Edit it with `cartographer client`."},
	{Path: "scopes", Scope: settingschema.ScopeClient, Type: "map", Values: "<provider>: provider | workspace", Default: "{}",
		Description: "Where a provider receives KB artifacts: provider (global, under the home directory) or workspace (each bound workspace gets its own)."},
	{Path: "workspaces", Scope: settingschema.ScopeClient, Type: "map", Default: "{}",
		Description: "Which KBs a provider receives while working in a given directory, as <provider>: [{path, kbs}]. An unbound workspace gets only the transversal bundle."},
	{Path: "search_roots", Scope: settingschema.ScopeClient, Type: "list", Default: `["~/Documents", "~/src", "~/code", "~/projects"]`,
		Description: "Directories scanned, 4 levels deep, for git clones to resolve {{repo:<key>}} placeholders; a root that does not exist is skipped. A key with an entry in paths is never searched."},
	{Path: "paths", Scope: settingschema.ScopeClient, Type: "map", Default: "{}",
		Description: "Manual name-to-directory mapping for {{path:<name>}}, and an override for {{repo:<key>}}. Edit it with `cartographer paths set`."},
	{Path: "ignored_paths", Scope: settingschema.ScopeClient, Type: "list", Default: "[]",
		Description: "Placeholder ids known to be absent on this machine, for example repo:work-tools; sync stops reporting them. Edit it with `cartographer paths ignore`."},
	{Path: "signing_keys", Scope: settingschema.ScopeClient, Type: "map", Default: "{}",
		Description: "Ed25519 public keys pinned per source KB, to verify the signed artifacts it serves. Pins are configured out of band, never learned from a sync."},
	{Path: "identities", Scope: settingschema.ScopeClient, Type: "map", Values: "<kb> | default: Name <email>", Default: "{}",
		Description: "The git author of this machine's writes, per KB name, with default for every other KB; without an entry, the identity git resolves in the session's repository. An empty value sends no author, and the server then names the token."},
	{Path: "mcp_approvals", Scope: settingschema.ScopeClient, Type: "map", Default: "{}",
		Description: "Local approvals of third-party MCP descriptors, bound to the exact content hash. Written by the approval commands; do not edit by hand."},
	{Path: "update_check", Scope: settingschema.ScopeClient, Type: "bool", Default: "true",
		Description: "Look up the latest release once a day and tell when a newer one exists. false makes no lookup at all. The client never replaces itself."},
	{Path: "usage_scan", Scope: settingschema.ScopeClient, Type: "bool", Default: "true",
		Description: "Scan session transcripts to tell the server which skills and agents this machine loads. false reads no transcript and reports no usage."},
}}
