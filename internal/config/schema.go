package config

import (
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/settingschema"
)

// Schema is the key table of the server config.yaml (D747): every YAML key
// Load accepts, with what it does, its values and default, and the environment
// variable and flag that override it. The comments written into the file, the
// generated config.example.yaml and the config view all read it.
//
// It is a table and not struct tags because the server config is a raw/public
// struct pair with env and flag layers on top. TestSchemaMatchesStructs fails
// when a key is added to rawConfig or KBSpec without an entry here, or an entry
// outlives its key: a new setting starts in this table.
//
// Scope kb marks the kbs[] keys that describe how a KB is run rather than the
// machine that runs it; the others are bound to one server. A KB carries the kb
// keys itself in its settings.yaml (D746) and that file wins over kbs[].
var Schema = settingschema.Schema{Keys: []settingschema.Key{
	{Path: "http", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Env: "CARTOGRAPHER_HTTP", Flag: "--http", Restart: true, Example: `":39273"`,
		Description: "Listen address of the HTTP transport, for example :39273. Empty serves MCP over stdio."},
	{Path: "init", Scope: settingschema.ScopeServer, Type: "bool", Default: "false", Flag: "--init", Restart: true, Example: "true",
		Description: "Initialize a KB that does not exist yet instead of failing."},
	{Path: "data", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Env: "CARTOGRAPHER_DATA", Flag: "--data", Restart: true, Example: "/data",
		Description: "Directory whose direct subdirectories are discovered as KBs, and where remote KBs are cloned."},
	{Path: "update_check", Scope: settingschema.ScopeServer, Type: "bool", Default: "true", Env: "CARTOGRAPHER_UPDATE_CHECK", Flag: "--update-check", Restart: true,
		Description: "Look up the latest release once a day and report it in /health and kb_status when it is newer; the Atlas reads the release history from the same GitHub list when someone opens it. Off, the server makes neither call. A dev build never checks."},

	{Path: "auth.mode", Scope: settingschema.ScopeServer, Type: "string", Values: "on | off | auto", Default: "auto", Env: "CARTOGRAPHER_AUTH", Restart: true, Example: "auto",
		Description: "Bearer-token authentication of the HTTP transport. auto turns it on when at least one token is configured."},
	{Path: "auth.tokens", Scope: settingschema.ScopeServer, Type: "list", Default: "[]", Env: "CARTOGRAPHER_TOKENS", Flag: "--tokens", Restart: true, Secret: true,
		Description: "Bearer tokens: a bare string (admin, every KB) or a mapping with token, scopes (kb:<name>:r or kb:<name>:rw), roles, id, author_name and author_email. A token may be written ${NAME} to read it from the environment."},
	{Path: "auth.roles", Scope: settingschema.ScopeServer, Type: "list", Default: "[]", Restart: true,
		Description: "Named permission sets that tokens reference by name; each has a name and rules (kb, access r or rw, and optional maps, journals, types)."},

	{Path: "kbs", Scope: settingschema.ScopeServer, Type: "list", Default: "[]", Env: "CARTOGRAPHER_KB_REMOTES", Restart: true,
		Description: "The KBs to clone and mount: each entry has a remote. A KB that is a folder under data is discovered and needs no entry. KBs from CARTOGRAPHER_KB_REMOTES are added to this list."},
	{Path: "kbs[].remote", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Example: "https://git.example.com/user/kb-a.git",
		Description: "Git remote a KB is cloned from, under data. A KB with a remote syncs with it; one without never contacts anything."},
	{Path: "kbs[].name", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Example: "kb-a",
		Description: "KB name, in place of the one derived from the remote. It drives the endpoint, the token scopes, the clone directory and the token and age-key file conventions."},
	{Path: "kbs[].ssh_key", Scope: settingschema.ScopeServer, Type: "string", Default: `""`,
		Description: "SSH private key for this KB's remote, in place of git.ssh_key."},
	{Path: "kbs[].known_hosts", Scope: settingschema.ScopeServer, Type: "string", Default: `""`,
		Description: "The known_hosts file for this KB's remote, in place of git.known_hosts."},
	{Path: "kbs[].author_name", Scope: settingschema.ScopeServer, Type: "string", Default: `""`,
		Description: "Git author name of this KB's commits, in place of git.author_name."},
	{Path: "kbs[].author_email", Scope: settingschema.ScopeServer, Type: "string", Default: `""`,
		Description: "Git author e-mail of this KB's commits, in place of git.author_email."},
	{Path: "kbs[].committer_name", Scope: settingschema.ScopeServer, Type: "string", Default: `""`,
		Description: "Git committer name of this KB's commits; empty follows the author."},
	{Path: "kbs[].committer_email", Scope: settingschema.ScopeServer, Type: "string", Default: `""`,
		Description: "Git committer e-mail of this KB's commits; empty follows the author."},
	{Path: "kbs[].sops_age_key_file", Scope: settingschema.ScopeServer, Type: "string", Default: `""`,
		Description: "The age key that decrypts this KB's secret references, in place of sops.age_key_dir and sops.age_key_file."},
	{Path: "kbs[].artifact_signing_seed", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Secret: true,
		Description: "A 32-byte hexadecimal Ed25519 seed that signs the provisioning artifacts served from this KB. Take it from a secret mount, for example ${CARTOGRAPHER_ARTIFACT_SIGNING_SEED}."},
	{Path: "kbs[].allow_artifact_write", Scope: settingschema.ScopeKB, Type: "bool", Default: "false",
		Description: "Enable artifact_write and artifact_delete on this KB. A written skill injects instructions that clients will run, so an rw token alone does not imply it."},
	{Path: "kbs[].allow_template_write", Scope: settingschema.ScopeKB, Type: "bool", Default: "true",
		Description: "Let artifact_write write templates/<slug>.md and nothing else when allow_artifact_write is off. Templates are never provisioned to a client."},
	{Path: "kbs[].auto_repair", Scope: settingschema.ScopeKB, Type: "list", Default: "every check that is safe to run unattended",
		Description: "Lint checks whose mechanical fix `kb repair --apply` and the background repair apply without review, by name; there is no all. An empty list means none."},
	{Path: "kbs[].repair_on_write", Scope: settingschema.ScopeKB, Type: "bool", Default: "true when auto_repair is not empty",
		Description: "Apply the auto_repair fixes to the concepts a write tool just wrote, in the same commit. false leaves the repair to the timer."},
	{Path: "kbs[].write_gate", Scope: settingschema.ScopeKB, Type: "string", Values: "off | error | warning", Default: "off",
		Description: "Refuse, and roll back, a write that introduces a finding at or above this severity."},
	{Path: "kbs[].doctor_auto_interval", Scope: settingschema.ScopeKB, Type: "duration", Values: "<n>d, <n> days, or 0 to turn it off", Default: "1d",
		Description: "How often the server applies auto_repair by itself, with no agent session."},
	{Path: "kbs[].doctor_interval", Scope: settingschema.ScopeKB, Type: "duration", Values: "<n>d, <n> days, or 0 to disable", Default: "14d",
		Description: "Days after the last kb-doctor session before the agent proposes the next one to the operator. A session never starts by itself."},
	{Path: "kbs[].doctor_mode", Scope: settingschema.ScopeKB, Type: "string", Values: "unattended | assisted", Default: `""`,
		Description: "Accepted and ignored: a kb-doctor session starts only when the operator asks. It is still validated."},
	{Path: "kbs[].doctor_budget", Scope: settingschema.ScopeKB, Type: "int", Values: "1 or more", Default: "10",
		Description: "Review items one kb-doctor session decides before it closes."},
	{Path: "kbs[].usage_stale_days", Scope: settingschema.ScopeKB, Type: "int", Values: "0 or more; 0 disables", Default: "42",
		Description: "Days a skill or agent may go unused by every client before artifact_unused reports it."},
	{Path: "kbs[].mcp_allowlist", Scope: settingschema.ScopeKB, Type: "list", Default: "[]",
		Description: "Third-party MCP endpoints a KB descriptor may point to, by name, transport (http or stdio) and exact target. An empty list denies every one."},
	{Path: "kbs[].secrets", Scope: settingschema.ScopeKB, Type: "bool", Default: "false",
		Description: "The KB uses secrets. A mount of it without an age key reports secrets as missing key instead of silently disabled. The key itself stays per machine (sops_age_key_file)."},

	{Path: "kb_aliases", Scope: settingschema.ScopeServer, Type: "list", Default: "[]", Restart: true,
		Description: "Old names of renamed KBs: each item has a from (the old name) and a to (the KB now mounted), so a routed ?kbs=<from> still reaches it. `kb rename` writes it. One step only, and a mounted name wins over an alias."},

	{Path: "git.ssh_key", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Restart: true,
		Description: "SSH private key used to reach remote KBs."},
	{Path: "git.known_hosts", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Restart: true,
		Description: "The known_hosts file used to reach remote KBs."},
	{Path: "git.token_dir", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Env: "CARTOGRAPHER_GIT_TOKEN_DIR", Restart: true,
		Description: "Directory with one file per KB, <token_dir>/<name>.token (its trimmed content is the token), used as HTTPS git credentials for that KB's remote."},

	{Path: "audit.log", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Env: "CARTOGRAPHER_AUDIT_LOG", Restart: true,
		Description: "Path of the append-only operation log. Empty turns the audit trail off."},
	{Path: "audit.key_seed", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Env: "CARTOGRAPHER_AUDIT_KEY", Restart: true, Secret: true,
		Description: "Seed that signs the audit trail, so it is tamper-evident. Write it ${NAME} to read it from the environment."},
	{Path: "audit.mode", Scope: settingschema.ScopeServer, Type: "string", Values: "best_effort | required", Default: "best_effort", Restart: true,
		Description: "With best_effort a failed append is logged and the call carries on; with required the call is rejected before the tool runs."},
	{Path: "audit.max_segment_bytes", Scope: settingschema.ScopeServer, Type: "int", Values: "bytes; 0 keeps the built-in size", Default: "0", Restart: true,
		Description: "Size after which the active log segment is rotated."},
	{Path: "audit.archive_dir", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Restart: true,
		Description: "Directory of rotated segments; empty keeps them beside the log."},
	{Path: "audit.retention_days", Scope: settingschema.ScopeServer, Type: "int", Values: "days; 0 keeps everything", Default: "90", Restart: true,
		Description: "Rotated segments older than this are deleted once their checkpoint is written."},

	{Path: "sops.age_key_file", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Env: "CARTOGRAPHER_SOPS_AGE_KEY_FILE", Restart: true,
		Description: "Default age key that decrypts secret references, for every KB without its own."},
	{Path: "sops.age_key_dir", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Env: "CARTOGRAPHER_SOPS_AGE_KEY_DIR", Restart: true,
		Description: "Directory with one age key per KB, <age_key_dir>/<name>.age. Resolution: kbs[].sops_age_key_file, then this, then age_key_file."},

	{Path: "tools.profile", Scope: settingschema.ScopeServer, Type: "string", Values: "agent | full", Default: "agent", Env: "CARTOGRAPHER_TOOLS_PROFILE", Flag: "--tools-profile", Restart: true,
		Description: "Which tools tools/list advertises. agent is the core set; full advertises every tool. Hidden tools stay callable."},

	{Path: "mcp.allowed_origins", Scope: settingschema.ScopeServer, Type: "list", Values: "origins with scheme and port, or *", Default: "[]", Env: "CARTOGRAPHER_MCP_ALLOWED_ORIGINS", Restart: true,
		Description: "Browser origins allowed to reach the MCP endpoint. Empty accepts only an Origin matching the request's own Host; * accepts any. A request with no Origin header is unaffected."},

	{Path: "web.enabled", Scope: settingschema.ScopeServer, Type: "bool", Default: "true", Env: "CARTOGRAPHER_WEB_ENABLED", Flag: "--web-enabled", Restart: true, Example: "true",
		Description: "Serve the read-only Atlas UI at /ui/ and its API at /api/ui/v1 in HTTP mode. false removes both routes."},
}}

// ExampleHeader is the comment at the top of the generated config.example.yaml.
const ExampleHeader = `Example configuration for "cartographer serve --config config.yaml".
Generated from the settings schema (internal/config/schema.go) by "make settings-example": edit the schema, not this file.
Every key is optional. Precedence, highest first: flag, environment variable, this file, default.`

// KBOnlyKeys are the settings a KB carries in its settings.yaml and that the
// server file does not have at all (D903): a decision of the team that owns the
// KB, the same for every server that mounts it, so there is no kbs[] form of it
// to disagree. They are rendered into the comments of a KB's settings.yaml with
// the kb scope of Schema (KBSettingsSchema); the kb package accepts and checks
// them (TestKBSettingsKeysMatchSchema pins the two tables together).
var KBOnlyKeys = []settingschema.Key{
	{Path: "branch", Scope: settingschema.ScopeKB, Type: "string", Default: `""`,
		Description: "Branch Cartographer commits and pushes to, in place of the remote's default one; the first push creates it. Empty or absent: the remote's default branch. Only the settings.yaml committed on the remote's default branch counts: a change made on any other branch has no effect until it reaches the default one."},
}

// Retired is the registry of the server config.yaml keys that were removed
// (D903): each is ignored with a warning that names what to use, and dropped
// when the file is realigned. Plans that remove more keys add them here.
var Retired = settingschema.Registry{Removed: []settingschema.Removed{
	{Path: "git.autocommit", Plan: 903, Env: "CARTOGRAPHER_GIT_AUTOCOMMIT", Flag: "--git-autocommit", Note: "every write is one commit, whatever the configuration"},
	{Path: "git.sync", Plan: 903, Env: "CARTOGRAPHER_GIT_SYNC", Flag: "--git-sync", Note: "a KB with a remote syncs, a KB without one never contacts anything"},
	{Path: "git.in_window", Plan: 903, Env: "CARTOGRAPHER_SYNC_IN_WINDOW", Note: "it is a constant, 30s"},
	{Path: "git.out_debounce", Plan: 903, Env: "CARTOGRAPHER_SYNC_OUT_DEBOUNCE", Note: "it is a constant, 3s; on Cloud Run every push is made before the write returns"},
	{Path: "git.profile", Plan: 903, Env: "CARTOGRAPHER_GIT_PROFILE", Refuse: "server", Note: kb.ServerProfileRemoved},
	{Path: "git.base_branch", Plan: 903, Note: kb.ServerProfileRemoved},
	{Path: "git.working_branch", Plan: 903, Note: kb.ServerProfileRemoved},
	{Path: "git.forge", Plan: 903, Note: kb.ServerProfileRemoved},
	{Path: "git.github_owner", Plan: 903, Note: kb.ServerProfileRemoved},
	{Path: "git.github_repository", Plan: 903, Note: kb.ServerProfileRemoved},
	{Path: "git.github_api_url", Plan: 903, Note: kb.ServerProfileRemoved},
	{Path: "git.github_token_env", Plan: 903, Note: kb.ServerProfileRemoved},
	{Path: "kbs[].path", Plan: 903, Note: "a KB without a remote is a folder under data, which is discovered; for a one-off mount use the --kb flag"},
	{Path: "kbs[].git_profile", Plan: 903, Refuse: "server", Note: kb.ServerProfileRemoved},
	{Path: "kbs[].git_base_branch", Plan: 903, Note: kb.ServerProfileRemoved},
	{Path: "kbs[].git_working_branch", Plan: 903, Note: kb.ServerProfileRemoved},
	{Path: "kbs[].git_forge", Plan: 903, Note: kb.ServerProfileRemoved},
	{Path: "kbs[].github_owner", Plan: 903, Note: kb.ServerProfileRemoved},
	{Path: "kbs[].github_repository", Plan: 903, Note: kb.ServerProfileRemoved},
	{Path: "kbs[].github_api_url", Plan: 903, Note: kb.ServerProfileRemoved},
	{Path: "kbs[].github_token_env", Plan: 903, Note: kb.ServerProfileRemoved},
	{Path: "kbs[].git_branch", Plan: 903, Replacement: "branch in the KB's settings.yaml", Note: "committed on the remote's default branch", RefuseAny: true},
	{Path: "git.author_name", Plan: 904, Note: kb.AuthorSettingRemoved},
	{Path: "git.author_email", Plan: 904, Note: kb.AuthorSettingRemoved},
	{Path: "git.committer_name", Plan: 904, Note: kb.AuthorSettingRemoved},
	{Path: "git.committer_email", Plan: 904, Note: kb.AuthorSettingRemoved},
}}
