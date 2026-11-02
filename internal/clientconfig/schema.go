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
	{Path: "token", Scope: settingschema.ScopeClient, Type: "string", Default: "${CARTOGRAPHER_TOKENS}", Secret: true,
		Description: "The bearer token: ${NAME} reads it from the environment variable NAME (an unattended run falls back to the private token file `connect` wrote), anything else is the token itself, and the file is then kept private (mode 0600). The agents' MCP entries can only name a variable, so a literal token has them read $CARTOGRAPHER_TOKENS."},
	{Path: "clients", Scope: settingschema.ScopeClient, Type: "map", Default: "{}",
		Example:     "claude:\n  kbs: [kb-a, work-kb]\n  config_dirs: [$HOME/.claude-work]",
		Description: "Which KBs each connected provider receives, as <provider>: {kbs: [...]}. A provider with no entry, or an entry without kbs, receives every known KB. clients.<provider>.access: read-min connects the provider read-only with the minimal tool list (written by `connect --access read-min`; remove the key to clear it). clients.claude.config_dirs lists extra Claude Code config directories (CLAUDE_CONFIG_DIR) whose .claude.json also receives the MCP entry. Edit it with `cartographer client`."},
	{Path: "scopes", Scope: settingschema.ScopeClient, Type: "map", Values: "provider | workspace", Default: "{}",
		Example:     "claude: workspace",
		Description: "Where a provider receives KB artifacts. provider puts them under the home directory and gives every session every KB the provider receives, read-write, wherever it starts (the default; nothing moves until a provider is switched). workspace makes the folder decide: a bound folder gets its KBs with their artifacts, any other folder ($HOME included) gets the provider's KBs read-only unless unbound_access says write, and no artifact either way. A repository a KB declares in its paths.yaml is bound to that KB automatically (a derived binding, recomputed by every sync and not stored in this file)."},
	{Path: "unbound_access", Scope: settingschema.ScopeClient, Type: "map", Values: "default | provider → read | write", Default: "{}",
		Example:     "default: write\nkiro: read",
		Description: "The access mode of a folder under no workspace binding ($HOME included), as default: <mode> for every provider and <provider>: <mode> overrides; a provider entry beats default, which beats the built-in read. read gives the provider's KBs with no write tool; write gives them read-write, still with no KB artifact (artifacts stay tied to a binding). Any other value is read. It only matters for a provider whose MCP entry is the workspace bridge (it has a binding); not a security boundary."},
	{Path: "kb_options", Scope: settingschema.ScopeClient, Type: "map", Values: "<kb> → artifacts: true | false; access: read | write", Default: "{}",
		Example:     "work-kb:\n  artifacts: false\nkb-a:\n  access: read",
		Description: "Per-KB settings, applied to every provider on this machine. artifacts: false stops sync from provisioning that KB's skills, subagents, hooks and MCP descriptors: the ones already installed are removed at the next sync, and its skills line and subagent sentence leave the instruction block. The KB stays connected: its MCP tools and routing sentence remain. It wins over every scope and binding (scopes, clients.<provider>.kbs, workspaces, derived bindings) and never adds a KB to any of them. Absent, or artifacts: true, is the default: artifacts are synced. It has no effect on a server that does not name its KBs. access: read connects that KB read-only on every provider that receives it, while the provider's other KBs stay writable; write, or absent, is the default. It is a scope filter, not a security boundary (a client that edits its own URL drops it): hard isolation is a server token role (kb:<name>:r). It needs a server with a routed endpoint. Edit it with `cartographer client kb`."},
	{Path: "workspaces", Scope: settingschema.ScopeClient, Type: "map", Default: "{}",
		Example:     "claude:\n  - path: $HOME/projects/work\n    kbs: [work-kb]",
		Description: "Which KBs a provider receives while working in a given directory, as <provider>: [{path, kbs}]. A folder under no binding is read-only unless unbound_access says write: the provider's KBs with no write tool and no KB artifact, $HOME included. The deepest binding wins. These entries are the explicit bindings: the ones derived from a KB's paths.yaml are not stored here. An entry on a declared repository's path replaces the derived binding (kbs: [] opts it out on this machine; bind: false in the KB's paths.yaml opts it out for everyone)."},
	{Path: "search_roots", Scope: settingschema.ScopeClient, Type: "list", Default: `["~/Documents", "~/src", "~/code", "~/projects"]`,
		Description: "Directories scanned, 4 levels deep, for git clones to resolve {{repo:<key>}} placeholders; a root that does not exist is skipped. A key with an entry in paths is never searched. The scan also finds the repositories that derived workspace bindings bind."},
	{Path: "paths", Scope: settingschema.ScopeClient, Type: "map", Default: "{}",
		Example:     "work-tools: $HOME/projects/work-tools",
		Description: "Manual name-to-directory mapping for {{path:<name>}}, and an override for {{repo:<key>}}. Edit it with `cartographer paths set`."},
	{Path: "ignored_paths", Scope: settingschema.ScopeClient, Type: "list", Default: "[]",
		Example:     "- repo:work-tools",
		Description: "Placeholder ids known to be absent on this machine, for example repo:work-tools; sync stops reporting them. Edit it with `cartographer paths ignore`."},
	{Path: "signing_keys", Scope: settingschema.ScopeClient, Type: "map", Default: "{}",
		Example:     "kb-a:\n  - 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Description: "Ed25519 public keys pinned per source KB, to verify the signed artifacts it serves. Pins are configured out of band, never learned from a sync."},
	{Path: "identities", Scope: settingschema.ScopeClient, Type: "map", Values: "<kb> | default: Name <email>", Default: "{}",
		Example:     "default: User <user@example.com>\nwork-kb: User <user@work.example.com>",
		Description: "The git author of this machine's writes, per KB name, with default for every other KB; without an entry, the identity git resolves in the session's repository. An empty value sends no author, and the server then names the token."},
	{Path: "mcp_approvals", Scope: settingschema.ScopeClient, Type: "map", Default: "{}",
		Example:     "kb-a:\n  docs-server:\n    content_hash: 9f86d081884c7d659a2feaa0c55ad015a3bf4f1b2b0b822cd15d6c15b0f00a08\n    approved_at: 2026-01-01T00:00:00Z",
		Description: "Local approvals of third-party MCP descriptors, hooks and executable artifacts (stored as <kind>/<name>), bound to the exact content hash. Written by the approval commands; do not edit by hand."},
	{Path: "approvals_migrated", Scope: settingschema.ScopeClient, Type: "bool", Default: "false",
		Description: "Set by the first sync, which approves the hooks and executable artifacts already installed with the content the KB serves now. Never cleared, so a revoked approval is not granted again. Do not edit by hand."},
	{Path: "allow_insecure_http", Scope: settingschema.ScopeClient, Type: "bool", Default: "false",
		Description: "Accept a plain http:// server_url that is not loopback, sending the bearer token in clear. Off by default: the client refuses such a URL. Set by `connect --allow-insecure-http`; CARTOGRAPHER_ALLOW_INSECURE_HTTP=1 allows one run."},
	{Path: "update_check", Scope: settingschema.ScopeClient, Type: "bool", Default: "true",
		Description: "Look up the latest release once a day and tell when a newer one exists. false makes no lookup at all. The client never replaces itself."},
	{Path: "usage_scan", Scope: settingschema.ScopeClient, Type: "bool", Default: "true",
		Description: "Scan session transcripts to tell the server which skills and agents this machine loads. false reads no transcript and reports no usage."},
}}
