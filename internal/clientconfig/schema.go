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
	{Path: "server.url", Scope: settingschema.ScopeClient, Type: "string", Default: defaults.DefaultMCPURL,
		Description: "MCP endpoint of the server this machine talks to. The first connect seeds it from CARTOGRAPHER_SERVER_URL when that is set."},
	{Path: "server.name", Scope: settingschema.ScopeClient, Type: "string", Default: "cartographer",
		Description: "Name under which the server is registered in each agent's MCP configuration."},
	{Path: "server.auth", Scope: settingschema.ScopeClient, Type: "bool", Default: "false",
		Description: "The server requires a bearer token; the clients then send the token.",
		Previous: []string{
			"The server requires a bearer token; the clients then send the one read from token_env.",
		}},
	{Path: "server.token", Scope: settingschema.ScopeClient, Type: "string", Default: "${CARTOGRAPHER_TOKENS}", Secret: true,
		Description: "The bearer token: ${NAME} reads it from the environment variable NAME (an unattended run falls back to the private token file `connect` wrote), anything else is the token itself, and the file is then kept private (mode 0600). The agents' MCP entries can only name a variable, so a literal token has them read $CARTOGRAPHER_TOKENS."},
	{Path: "server.allow_insecure_http", Scope: settingschema.ScopeClient, Type: "bool", Default: "false",
		Description: "Accept a plain http:// server.url that is not loopback, sending the bearer token in clear. Off by default: the client refuses such a URL. Set by `connect --allow-insecure-http`; CARTOGRAPHER_ALLOW_INSECURE_HTTP=1 allows one run."},
	{Path: "defaults.unbound_access", Scope: settingschema.ScopeClient, Type: "string", Values: "read | write", Default: "read",
		Description: "The access mode of a folder under no workspace binding ($HOME included), for every provider: read gives the provider's KBs with no write tool; write gives them read-write, still with no KB artifact (artifacts stay tied to a binding). A provider's own providers.<provider>.unbound_access beats it, and it beats the built-in read. Any other value is read. It only matters for a provider whose MCP entry is the workspace bridge (it has a binding); not a security boundary."},
	{Path: "providers", Scope: settingschema.ScopeClient, Type: "map", Default: "{}",
		Example:     "claude:\n  kbs: [kb-a, work-kb]\n  scope: workspace\n  unbound_access: write\n  config_dirs: [$HOME/.claude-work]\n  workspaces:\n    - path: $HOME/projects/work\n      kbs: [work-kb]\nopencode:\n  allow_symlinks: [$HOME/.config/opencode]",
		Description: "The settings of each connected provider, as <provider>: {kbs, scope, unbound_access, config_dirs, access, tools, allow_symlinks, workspaces}. kbs is the KBs the provider receives; a provider with no entry, or an entry without kbs, receives every known KB (edit it with `cartographer client`). scope is where it receives KB artifacts: provider puts them under the home directory and gives every session every KB the provider receives, read-write, wherever it starts (the default; nothing moves until a provider is switched); workspace makes the folder decide: a bound folder gets its KBs with their artifacts, any other folder ($HOME included) gets the provider's KBs read-only unless unbound_access says write, and no artifact either way. A repository a KB declares in its paths.yaml is bound to that KB automatically (a derived binding, recomputed by every sync and not stored in this file). unbound_access (read or write) is the mode of a folder under no binding for this provider and beats defaults.unbound_access. config_dirs lists extra Claude Code config directories (CLAUDE_CONFIG_DIR) whose .claude.json also receives the MCP entry; claude only. access: read with tools: minimal connects the provider read-only with the minimal tool list (written by `connect --access read-min`; remove both keys to clear it). allow_symlinks lists links that writes may follow (off by default, a symlinked config is otherwise refused): each entry is the link itself (~ and $HOME are expanded), a listed directory link covers everything below it, a link nested below a listed one needs its own entry, and the link is kept while the write lands on its target; it is read only from this file in the home directory, never from a project-level file in a repository, and `cartographer client symlinks <provider>` edits it. workspaces is which KBs the provider receives in a given directory, as [{path, kbs}]: the deepest binding wins, a folder under no binding is read-only unless unbound_access says write, and these are the explicit bindings (the derived ones are not stored here; an entry on a declared repository's path replaces the derived binding, kbs: [] opts it out on this machine and bind: false in the KB's paths.yaml for everyone)."},
	{Path: "knowledge_bases", Scope: settingschema.ScopeClient, Type: "map", Values: "<kb> | default → access: read | write; artifacts: true | false; identity: Name <email>; signing_keys: [...]", Default: "{}",
		Example:     "default:\n  identity: User <user@example.com>\nwork-kb:\n  access: read\n  artifacts: false\n  identity: User <user@work.example.com>\n  signing_keys:\n    - 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Description: "The settings of each KB on this machine, applied to every provider, as <kb>: {access, artifacts, identity, signing_keys}; default holds the identity of every KB without an entry of its own. artifacts: false stops sync from provisioning that KB's skills, subagents, hooks and MCP descriptors: the ones already installed are removed at the next sync, and its skills line and subagent sentence leave the instruction block. The KB stays connected: its MCP tools and routing sentence remain. It wins over every scope and binding and never adds a KB to any of them; absent, or true, is the default. It has no effect on a server that does not name its KBs. access: read connects that KB read-only on every provider that receives it, while the provider's other KBs stay writable; write, or absent, is the default. It is a scope filter, not a security boundary (a client that edits its own URL drops it): hard isolation is a server token role (kb:<name>:r). It needs a server with a routed endpoint. Edit both with `cartographer client kb`. identity is the git author of this machine's writes to the KB, Name <email>; without one, the identity git resolves in the session's repository, and an empty value sends no author, so the server names the token. signing_keys are the Ed25519 public keys pinned to verify the signed artifacts the KB serves; pins are configured out of band, never learned from a sync."},
	{Path: "plugins", Scope: settingschema.ScopeClient, Type: "map", Values: "<name> → source, ref, sha, version, installed, approved, enabled, settings", Default: "{}", Hidden: true,
		Example:     "team-forks:\n  source: https://example.com/team/forks.git\n  ref: v1.2\n  installed: <64 hex>\n  approved: <64 hex>\n  enabled: true",
		Description: "The plugins installed on this machine. Written by the plugin commands, not by hand: source is a git remote or an absolute folder, ref the branch, tag or commit asked for, sha the commit it resolved to, installed and approved the digests of the fetched and of the approved tree (approved empty is pending), enabled and settings what the user chose."},
	{Path: "placeholders.search_roots", Scope: settingschema.ScopeClient, Type: "list", Default: `["~/Documents", "~/src", "~/code", "~/projects"]`,
		Description: "Directories scanned, 4 levels deep, for git clones to resolve {{repo:<key>}} placeholders; a root that does not exist is skipped. A key with an entry in placeholders.paths is never searched. The scan also finds the repositories that derived workspace bindings bind.",
		Previous: []string{
			"Directories scanned for git clones to resolve {{repo:<key>}} placeholders.",
			"Directories scanned, 4 levels deep, for git clones to resolve {{repo:<key>}} placeholders; a root that does not exist is skipped. A key with an entry in paths is never searched.",
			"Directories scanned, 4 levels deep, for git clones to resolve {{repo:<key>}} placeholders; a root that does not exist is skipped. A key with an entry in paths is never searched. The scan also finds the repositories that derived workspace bindings bind.",
		}},
	{Path: "placeholders.paths", Scope: settingschema.ScopeClient, Type: "map", Default: "{}",
		Example:     "work-tools: $HOME/projects/work-tools",
		Description: "Manual name-to-directory mapping for {{path:<name>}}, and an override for {{repo:<key>}}. Edit it with `cartographer paths set`."},
	{Path: "placeholders.ignored", Scope: settingschema.ScopeClient, Type: "list", Default: "[]",
		Example:     "- repo:work-tools",
		Description: "Placeholder ids known to be absent on this machine, for example repo:work-tools; sync stops reporting them. Edit it with `cartographer paths ignore`."},
	{Path: "approvals_migrated", Scope: settingschema.ScopeClient, Type: "bool", Default: "false",
		Description: "Set by the first sync, which approves the hooks and executable artifacts already installed with the content the KB serves now. Never cleared, so a revoked approval is not granted again. Do not edit by hand."},
	{Path: "update_check", Scope: settingschema.ScopeClient, Type: "bool", Default: "true",
		Description: "Look up the latest release once a day and tell when a newer one exists. false makes no lookup at all. The client never replaces itself."},
	{Path: "usage_scan", Scope: settingschema.ScopeClient, Type: "bool", Default: "true",
		Description: "Scan session transcripts to tell the server which skills and agents this machine loads. false reads no transcript and reports no usage."},
}}
