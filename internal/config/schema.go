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
	{Path: "data", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Env: "CARTOGRAPHER_DATA", Flag: "--data", Restart: true, Example: "/data",
		Description: "Directory whose direct subdirectories are discovered as KBs, and where remote KBs are cloned. The name credentials is the server's own and is never discovered."},
	{Path: "credentials_dir", Scope: settingschema.ScopeServer, Type: "string", Default: "<data>/credentials", Env: "CARTOGRAPHER_CREDENTIALS_DIR", Restart: true, Example: "/etc/cartographer/credentials",
		Description: "Directory with every secret file, looked up by KB name: <kb>.ssh, <kb>.token and <kb>.age (each with a default.* for the KBs without their own), <kb>.signing (the artifact signing seed, no default) and known_hosts (one file for every remote). On Kubernetes it is one mounted Secret (defaultMode 0440 with the pod's fsGroup). A file readable by other users, or by the group when it is the server's own, is warned about once at start."},
	{Path: "update_check", Scope: settingschema.ScopeServer, Type: "bool", Default: "true", Env: "CARTOGRAPHER_UPDATE_CHECK", Flag: "--update-check", Restart: true,
		Description: "Look up the latest release once a day and report it in /health and kb_status when it is newer; the Atlas reads the release history from the same GitHub list when someone opens it. Off, the server makes neither call. A dev build never checks."},

	{Path: "auth.mode", Scope: settingschema.ScopeServer, Type: "string", Values: "on | off | auto", Default: "auto", Env: "CARTOGRAPHER_AUTH", Restart: true, Example: "auto",
		Description: "Bearer-token authentication of the HTTP transport. auto turns it on when at least one token is configured."},
	{Path: "auth.tokens", Scope: settingschema.ScopeServer, Type: "list", Default: "[]", Env: "CARTOGRAPHER_TOKENS", Flag: "--tokens", Restart: true, Secret: true,
		Description: "Bearer tokens: a bare string (admin, every KB) or a mapping with token, scopes (kb:<name>:r or kb:<name>:rw), roles and id. A token may be written ${NAME} to read it from the environment."},
	{Path: "auth.roles", Scope: settingschema.ScopeServer, Type: "list", Default: "[]", Restart: true,
		Description: "Named permission sets that tokens reference by name; each has a name and rules (kb, access r or rw, and optional maps, journals, types)."},

	{Path: "kbs", Scope: settingschema.ScopeServer, Type: "list", Default: "[]", Env: "CARTOGRAPHER_KB_REMOTES", Restart: true,
		Description: "The KBs to clone and mount: each entry has a remote. A KB that is a folder under data is discovered and needs no entry. KBs from CARTOGRAPHER_KB_REMOTES are added to this list."},
	{Path: "kbs[].remote", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Example: "https://git.example.com/user/kb-a.git",
		Description: "Git remote a KB is cloned from, under data. A KB with a remote syncs with it; one without never contacts anything."},
	{Path: "kbs[].name", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Example: "kb-a",
		Description: "KB name, in place of the one derived from the remote. It drives the endpoint, the token scopes, the clone directory and the names of the KB's files in credentials_dir."},

	{Path: "audit.log", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Env: "CARTOGRAPHER_AUDIT_LOG", Restart: true,
		Description: "Path of the append-only operation log, which is always on and signed with audit.key from credentials_dir (generated at the first start). Empty keeps it at <data>/audit/audit.log."},
	{Path: "audit.mode", Scope: settingschema.ScopeServer, Type: "string", Values: "best_effort | required", Default: "best_effort", Restart: true,
		Description: "With best_effort a failed append is logged and the call carries on, and a log or key the server cannot open is a warning at start; with required the call is rejected before the tool runs, and the server does not start without its signed log."},
	{Path: "audit.max_segment_bytes", Scope: settingschema.ScopeServer, Type: "int", Values: "bytes; 0 keeps the default", Default: "67108864", Restart: true,
		Description: "Size after which the active log segment is rotated (64 MiB by default), so retention has segments to delete."},
	{Path: "audit.archive_dir", Scope: settingschema.ScopeServer, Type: "string", Default: `""`, Restart: true,
		Description: "Directory of rotated segments; empty keeps them beside the log."},
	{Path: "audit.retention_days", Scope: settingschema.ScopeServer, Type: "int", Values: "days; 0 keeps everything", Default: "90", Restart: true,
		Description: "Rotated segments older than this are deleted once their checkpoint is written."},

	{Path: "web.enabled", Scope: settingschema.ScopeServer, Type: "bool", Default: "true", Env: "CARTOGRAPHER_WEB_ENABLED", Flag: "--web-enabled", Restart: true, Example: "true",
		Description: "Serve the read-only Atlas UI at /ui/ and its API at /api/ui/v1 in HTTP mode. false removes both routes."},
}}

// ExampleHeader is the comment at the top of the generated config.example.yaml.
const ExampleHeader = `Example configuration for "cartographer serve --config config.yaml".
Generated from the settings schema (internal/config/schema.go) by "make settings-example": edit the schema, not this file.
Every key is optional. Precedence, highest first: flag, environment variable, this file, default.`

// KBKeys are the settings a KB carries in its settings.yaml (D746), and only
// there (D906): how the KB behaves, whoever serves it, the same for every server
// that mounts it, so there is no kbs[] form of them to disagree. They are
// rendered into the comments of a KB's settings.yaml (KBSettingsSchema); the kb
// package accepts and checks them (TestKBSettingsKeysMatchSchema pins the two
// tables together).
var KBKeys = []settingschema.Key{
	{Path: "usage_stale_days", Scope: settingschema.ScopeKB, Type: "int", Values: "7 or more", Default: "42",
		Description: "Days a skill or agent may go unused by every client before kb_status.usage counts it as stale. A value under 7 is raised to 7 with a warning; there is no value that turns the signal off."},
	{Path: "branch", Scope: settingschema.ScopeKB, Type: "string", Default: `""`,
		Description: "Branch Cartographer commits and pushes to, in place of the remote's default one; the first push creates it. Empty or absent: the remote's default branch. Only the settings.yaml committed on the remote's default branch counts: a change made on any other branch has no effect until it reaches the default one."},
}

// kbKeyHome is the reason given for a KB-scope key met in kbs[] (D906).
const kbKeyHome = "a KB's behaviour is carried by the KB, the same on every server that mounts it; the server file keeps only name and remote"

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
	{Path: "kbs[].author_name", Plan: 904, Note: kb.AuthorSettingRemoved},
	{Path: "kbs[].author_email", Plan: 904, Note: kb.AuthorSettingRemoved},
	{Path: "kbs[].committer_name", Plan: 904, Note: kb.AuthorSettingRemoved},
	{Path: "kbs[].committer_email", Plan: 904, Note: kb.AuthorSettingRemoved},
	{Path: "auth.tokens[].author_name", Plan: 904, Note: kb.AuthorSettingRemoved + "; the token's id is a trailer of the commit"},
	{Path: "auth.tokens[].author_email", Plan: 904, Note: kb.AuthorSettingRemoved + "; the token's id is a trailer of the commit"},
	{Path: "git.ssh_key", Plan: 905, Replacement: "default.ssh in credentials_dir", Note: "a key for one KB is <kb>.ssh"},
	{Path: "kbs[].ssh_key", Plan: 905, Replacement: "<kb>.ssh in credentials_dir"},
	{Path: "git.known_hosts", Plan: 905, Replacement: "known_hosts in credentials_dir", Note: "one file for every remote"},
	{Path: "kbs[].known_hosts", Plan: 905, Replacement: "known_hosts in credentials_dir", Note: "one file for every remote"},
	{Path: "git.token_dir", Plan: 905, Env: "CARTOGRAPHER_GIT_TOKEN_DIR", Replacement: "<kb>.token or default.token in credentials_dir"},
	{Path: "sops.age_key_file", Plan: 905, Env: "CARTOGRAPHER_SOPS_AGE_KEY_FILE", Replacement: "default.age in credentials_dir"},
	{Path: "sops.age_key_dir", Plan: 905, Env: "CARTOGRAPHER_SOPS_AGE_KEY_DIR", Replacement: "<kb>.age in credentials_dir"},
	{Path: "kbs[].sops_age_key_file", Plan: 905, Replacement: "<kb>.age in credentials_dir"},
	{Path: "kbs[].artifact_signing_seed", Plan: 905, Replacement: "<kb>.signing in credentials_dir", Note: "the file holds the hexadecimal seed; there is no default.signing"},
	{Path: "audit.key_seed", Plan: 905, Env: "CARTOGRAPHER_AUDIT_KEY", Replacement: "audit.key in credentials_dir", Note: "a missing one is generated at the first start"},
	{Path: "kbs[].allow_artifact_write", Plan: 906, Note: kb.ArtifactWriteAlwaysOn},
	{Path: "kbs[].allow_template_write", Plan: 906, Note: kb.ArtifactWriteAlwaysOn},
	{Path: "kbs[].mcp_allowlist", Plan: 906, Note: kb.MCPAllowlistRemoved},
	{Path: "kbs[].secrets", Plan: 906, Note: kb.SecretsDerived},
	{Path: "kbs[].auto_repair", Plan: 907, Note: kb.DoctorKeysRemoved},
	{Path: "kbs[].repair_on_write", Plan: 907, Note: kb.DoctorKeysRemoved},
	{Path: "kbs[].write_gate", Plan: 907, Note: kb.WriteGateRemoved},
	{Path: "kbs[].doctor_auto_interval", Plan: 907, Note: kb.DoctorKeysRemoved},
	{Path: "kbs[].doctor_interval", Plan: 907, Note: kb.DoctorKeysRemoved},
	{Path: "kbs[].doctor_mode", Plan: 907, Note: kb.DoctorKeysRemoved},
	{Path: "kbs[].doctor_budget", Plan: 907, Note: kb.DoctorKeysRemoved},
	{Path: "kbs[].usage_stale_days", Plan: 906, Replacement: "usage_stale_days in the KB's settings.yaml", Note: kbKeyHome},
	{Path: "init", Plan: 906, Flag: "--init", Note: "a KB that does not exist is an error: create it with `cartographer kb create`"},
	{Path: "tools.profile", Plan: 906, Env: "CARTOGRAPHER_TOOLS_PROFILE", Flag: "--tools-profile", FlagValue: true, Note: "tools/list always shows the core set, and the other tools stay callable by name"},
	{Path: "mcp.allowed_origins", Plan: 906, Env: "CARTOGRAPHER_MCP_ALLOWED_ORIGINS", Note: "a request with an Origin header is accepted only when the origin's host and port are the request's Host; one without an Origin header is accepted"},
	{Path: "kb_aliases", Plan: 906, Note: "the old names of renamed KBs live in .cartographer/state.json under data, written by `kb rename`; the realignment moves the list there"},
}}
