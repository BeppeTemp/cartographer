// Package workerconfig holds the key table of the worker config file (D21): the one list of keys the file accepts.
//
// It is a leaf package (settingschema and the standard library only) so the
// server can render a worker's settings without linking the worker. The loader
// that reads the file lives in internal/worker and checks every key against
// this table.
package workerconfig

import "github.com/BeppeTemp/cartographer/internal/settingschema"

// Schema is the key table of the worker config file: the keys of the perimeter,
// its model profiles, its schedule and the task list, then the option keys of
// every task kind (declared here as data, once per kind plan; the execution half
// of a kind stays in internal/worker).
//
// Conventions that keep it usable for a redacted report (D22): a key whose value
// is the name of an environment variable has Type "env", a URL key has Type
// "url", and no key is Secret because the file holds no secret.
var Schema = settingschema.Schema{Keys: []settingschema.Key{
	{Path: "name", Scope: settingschema.ScopeWorker, Type: "string", Values: "[a-z0-9-], 1 to 40 characters", Default: `""`, Example: "docs-night",
		Description: "Name of the perimeter, the identity of the worker in log entries; the replicas of one perimeter share it."},
	{Path: "server_url", Scope: settingschema.ScopeWorker, Type: "url", Default: `""`, Example: "https://wiki.example.com/mcp",
		Description: "MCP endpoint of the server the worker talks to."},
	{Path: "allow_insecure_http", Scope: settingschema.ScopeWorker, Type: "bool", Default: "false",
		Description: "Accept a plain http:// server_url to a non-loopback host, which sends the token in clear."},
	{Path: "token_env", Scope: settingschema.ScopeWorker, Type: "env", Default: `""`, Example: "WORKER_TOKEN",
		Description: "Name of the environment variable holding the perimeter's bearer token, which should reach only the perimeter's KBs."},
	{Path: "kbs", Scope: settingschema.ScopeWorker, Type: "list", Default: "[]",
		Example:     "- kb-a\n- work-kb",
		Description: "The KBs of the perimeter; several KBs are several separate queues served by the same worker."},

	{Path: "models", Scope: settingschema.ScopeWorker, Type: "list", Default: "[]",
		Example:     "- name: cloud\n  provider: anthropic\n  model: claude-sonnet-4-5\n  api_key_env: CLOUD_API_KEY\n  tier: large\n  budget:\n    daily_tokens: 2000000",
		Description: "The model profiles tasks choose from, each with its own token budget."},
	{Path: "models[].name", Scope: settingschema.ScopeWorker, Type: "string", Values: "[a-z0-9-], 1 to 40 characters", Default: `""`,
		Description: "Name of the profile, which tasks[].model refers to; not the provider's model id, which is models[].model."},
	{Path: "models[].provider", Scope: settingschema.ScopeWorker, Type: "string", Values: "anthropic | openai", Default: `""`,
		Description: "The API the profile speaks: the Anthropic Messages API, or OpenAI-compatible chat completions (a local llama.cpp or LiteLLM endpoint)."},
	{Path: "models[].base_url", Scope: settingschema.ScopeWorker, Type: "url", Default: `""`,
		Description: "Endpoint of the model API, without credentials in it; required for openai, optional for anthropic."},
	{Path: "models[].allow_insecure_http", Scope: settingschema.ScopeWorker, Type: "bool", Default: "false",
		Description: "Accept a plain http:// base_url to a non-loopback host, for a local endpoint inside a private network."},
	{Path: "models[].model", Scope: settingschema.ScopeWorker, Type: "string", Default: `""`,
		Description: "The provider's model id, as the API names it."},
	{Path: "models[].api_key_env", Scope: settingschema.ScopeWorker, Type: "env", Default: `""`,
		Description: "Name of the environment variable holding the API key; required for anthropic, optional for a local openai endpoint."},
	{Path: "models[].tier", Scope: settingschema.ScopeWorker, Type: "string", Values: "small | large", Default: "small",
		Description: "What the profile's model can do, which decides the units a worker takes with it."},
	{Path: "models[].budget.daily_tokens", Scope: settingschema.ScopeWorker, Type: "int", Values: "tokens, above 0", Default: "0",
		Description: "Tokens, input and output as the API reports them, the profile may spend per day across all the perimeter's KBs."},
	{Path: "models[].budget.run_tokens", Scope: settingschema.ScopeWorker, Type: "int", Values: "tokens, above 0 and at most daily_tokens", Default: "daily_tokens",
		Description: "The most one run spends on the profile, all its tasks together."},

	{Path: "schedule.window.start", Scope: settingschema.ScopeWorker, Type: "string", Values: "HH:MM", Default: `""`,
		Description: "Start of the daily window in which the worker acts."},
	{Path: "schedule.window.end", Scope: settingschema.ScopeWorker, Type: "string", Values: "HH:MM, excluded; before start means across midnight", Default: `""`,
		Description: "End of the daily window, excluded."},
	{Path: "schedule.window.timezone", Scope: settingschema.ScopeWorker, Type: "string", Values: "IANA name", Default: `""`,
		Description: "Time zone of the window and of the budget day; required, because a container's local time is UTC."},
	{Path: "schedule.idle_minutes", Scope: settingschema.ScopeWorker, Type: "int", Values: "minutes, at least 1", Default: "0",
		Description: "Act on a KB that has been idle this long; either this or the window suffices when both are set."},
	{Path: "schedule.poll_seconds", Scope: settingschema.ScopeWorker, Type: "int", Values: "seconds, at least 10", Default: "60",
		Description: "How often the runner re-evaluates the triggers while it is not running."},

	{Path: "tasks", Scope: settingschema.ScopeWorker, Type: "list", Default: "[]",
		Example:     "- kind: ingest\n  model: cloud",
		Description: "The tasks the worker runs, in priority order; each has a kind, a model profile and the options of its kind."},
	{Path: "tasks[].kind", Scope: settingschema.ScopeWorker, Type: "string", Default: `""`,
		Description: "The kind of task, from the registry of the binary."},
	{Path: "tasks[].model", Scope: settingschema.ScopeWorker, Type: "string", Default: `""`,
		Description: "The model profile the task runs on: a models[].name."},
	// The option keys of each task kind go here, one row per path, declared by
	// the plan that adds the kind. A path shared by two kinds is one row.
}}
