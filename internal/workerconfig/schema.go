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
	{Path: "models[].context_tokens", Scope: settingschema.ScopeWorker, Type: "int", Values: "tokens, above 0", Default: "32000",
		Description: "The model's context window: a run is told to close at 75% of it and stops before a prompt that would not fit."},
	{Path: "models[].max_output_tokens", Scope: settingschema.ScopeWorker, Type: "int", Values: "tokens, above 0", Default: "4096",
		Description: "The most one answer of the model may hold."},
	{Path: "models[].timeout_seconds", Scope: settingschema.ScopeWorker, Type: "int", Values: "seconds, above 0", Default: "120",
		Description: "Per attempt of a model call, which is not streamed: raise it for a slow local model."},
	{Path: "models[].budget.daily_tokens", Scope: settingschema.ScopeWorker, Type: "int", Values: "tokens, above 0", Default: "0",
		Description: "Tokens, input and output as the API reports them, the profile may spend per day across all the perimeter's KBs."},
	{Path: "models[].budget.run_tokens", Scope: settingschema.ScopeWorker, Type: "int", Values: "tokens, above 0 and at most daily_tokens", Default: "daily_tokens",
		Description: "The most one run spends on the profile, all its tasks together."},

	// connectors is a mapping keyed by connector name: "{name}" stands for any
	// key (the loader in internal/worker reads it so; settingschema's realign has
	// no such segment and the worker file is never realigned). Display rules
	// (D22): env, headers and token_env show the variable name and whether it is
	// set, never a value; a command shows its base name, args never.
	{Path: "connectors", Scope: settingschema.ScopeWorker, Type: "map", Default: "{}",
		Description: "External MCP servers the unit-execution task may read through, by name; a Source names one in its connector field."},
	{Path: "connectors.{name}.transport", Scope: settingschema.ScopeWorker, Type: "string", Values: "stdio | http", Default: `""`,
		Description: "How the worker reaches the server: a child process on stdio, or a streamable HTTP endpoint."},
	{Path: "connectors.{name}.command", Scope: settingschema.ScopeWorker, Type: "string", Default: `""`,
		Description: "The program of a stdio connector: a path with a separator is resolved against the config file's directory, a bare name is looked up on PATH."},
	{Path: "connectors.{name}.args", Scope: settingschema.ScopeWorker, Type: "list", Default: "[]",
		Description: "Arguments of the stdio program, passed verbatim with no shell."},
	{Path: "connectors.{name}.env", Scope: settingschema.ScopeWorker, Type: "map", Default: "{}",
		Description: "Variables of a stdio child, as NAME: the name of the worker's own variable holding the value; the child gets only these plus PATH and HOME."},
	{Path: "connectors.{name}.url", Scope: settingschema.ScopeWorker, Type: "url", Default: `""`,
		Description: "Endpoint of an http connector, without credentials in it."},
	{Path: "connectors.{name}.allow_insecure_http", Scope: settingschema.ScopeWorker, Type: "bool", Default: "false",
		Description: "Accept a plain http:// url to a non-loopback host, which sends the token in clear."},
	{Path: "connectors.{name}.token_env", Scope: settingschema.ScopeWorker, Type: "env", Default: `""`,
		Description: "Name of the environment variable holding the bearer token of an http connector."},
	{Path: "connectors.{name}.headers", Scope: settingschema.ScopeWorker, Type: "map", Default: "{}",
		Description: "Extra headers of an http connector, as Header-Name: the name of the variable holding the value."},
	{Path: "connectors.{name}.tools", Scope: settingschema.ScopeWorker, Type: "list", Default: "[]",
		Description: "The only tools of the server the model is offered and may call, named as the server names them; required, because the server's own list is untrusted."},
	{Path: "connectors.{name}.call_timeout", Scope: settingschema.ScopeWorker, Type: "duration", Default: "60s",
		Description: "Time allowed to start the connector and for each of its calls."},
	{Path: "connectors.{name}.max_result_bytes", Scope: settingschema.ScopeWorker, Type: "int", Values: "bytes, above 0", Default: "262144",
		Description: "Largest tool result kept in memory; the rest is cut."},

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

	{Path: "fetch.allow_private", Scope: settingschema.ScopeWorker, Type: "bool", Default: "false",
		Description: "Let the ingest task's built-in fetch reach private, loopback and link-local addresses; off, a worker inside a network cannot be used as a proxy to it."},

	{Path: "fetch.allow_hosts", Scope: settingschema.ScopeWorker, Type: "list", Default: "[]",
		Description: "Hosts a feed may be read from through the built-in http-pages recipe; empty, no feed uses it. A page of the feed is on the index's own host."},
	{Path: "fetch.allow_insecure_http", Scope: settingschema.ScopeWorker, Type: "bool", Default: "false",
		Description: "Let the http-pages recipe read plain http pages; off, only https is read."},
	{Path: "git.allow_hosts", Scope: settingschema.ScopeWorker, Type: "list", Default: "[]", Example: "[git.example.com, \"*.corp.example.com\"]",
		Description: "Hosts a feed may be read from through the built-in git reader (https, no credential); empty, no feed and no git entry is read."},
	{Path: "recipes", Scope: settingschema.ScopeWorker, Type: "map", Default: "{}",
		Description: "Recipes that list a feed, by name: the instructions text of a built-in, or an executable of your own (kind exec, protocol v1)."},
	{Path: "recipes.{name}.instructions_file", Scope: settingschema.ScopeWorker, Type: "string", Values: "path inside the config directory, at most 16 KiB", Default: `""`,
		Description: "Text that replaces the embedded instructions of a built-in recipe, or the instructions of an exec recipe (required there): they tell the model what a unit for this kind of feed looks like."},
	{Path: "recipes.{name}.connector", Scope: settingschema.ScopeWorker, Type: "string", Default: `""`,
		Description: "Exec recipe: the connector (git, fetch or one under connectors) the unit executor uses to read an entry's locator; omit it when the script returns the content inline."},
	{Path: "recipes.{name}.shape", Scope: settingschema.ScopeWorker, Type: "string", Values: "feed, system", Default: `"feed"`,
		Description: "Exec recipe: the shape of Source it serves: feed (documents with revisions) or system (a live read-only system, listed after a preflight of its identity)."},
	{Path: "recipes.{name}.exec.command", Scope: settingschema.ScopeWorker, Type: "string", Default: `""`,
		Description: "Exec recipe: the program that lists the feed; a path with a separator is resolved against the config file's directory, a bare name is looked up on PATH. Run without a shell, from a temp directory."},
	{Path: "recipes.{name}.exec.args", Scope: settingschema.ScopeWorker, Type: "list", Default: "[]",
		Description: "Exec recipe: arguments of the program, passed verbatim."},
	{Path: "recipes.{name}.exec.env", Scope: settingschema.ScopeWorker, Type: "map", Default: "{}",
		Description: "Exec recipe: variables of the child, as NAME: the name of the worker's own variable holding the value; the child gets only these plus PATH and HOME."},
	{Path: "recipes.{name}.exec.timeout", Scope: settingschema.ScopeWorker, Type: "duration", Values: "above 0, at most 1h", Default: "5m",
		Description: "Exec recipe: time one listing may take before the process group is killed."},
	{Path: "recipes.{name}.exec.max_output_bytes", Scope: settingschema.ScopeWorker, Type: "int", Values: "bytes, at most 64 MiB", Default: "8388608",
		Description: "Exec recipe: largest listing read from stdout; past it the process is killed and the listing refused."},
	{Path: "recipes.{name}.exec.max_content_bytes", Scope: settingschema.ScopeWorker, Type: "int", Values: "bytes, at most 1 MiB", Default: "131072",
		Description: "Exec recipe: largest inline content of one entry."},

	{Path: "tasks", Scope: settingschema.ScopeWorker, Type: "list", Default: "[]",
		Example:     "- kind: ingest\n  model: cloud",
		Description: "The tasks the worker runs, in priority order; each has a kind, a model profile and the options of its kind."},
	{Path: "tasks[].kind", Scope: settingschema.ScopeWorker, Type: "string", Default: `""`,
		Description: "The kind of task, from the registry of the binary."},
	{Path: "tasks[].model", Scope: settingschema.ScopeWorker, Type: "string", Default: `""`,
		Description: "The model profile the task runs on: a models[].name."},
	{Path: "tasks[].max_writes", Scope: settingschema.ScopeWorker, Type: "int", Values: "at least 1", Default: "20",
		Description: "The most writes one doctor session may apply to a KB; past it the session is told to close."},
	// The option keys of each task kind go here, one row per path, declared by
	// the plan that adds the kind. A path shared by two kinds is one row.

	// kind: prompt (D23)
	{Path: "tasks[].name", Scope: settingschema.ScopeWorker, Type: "string", Values: "[a-z0-9-], 1 to 40 characters, unique among the tasks", Default: `""`, Example: "weekly-digest",
		Description: "Prompt task: its name, written into its log entries and commit reasons; a rename restarts its cadence."},
	{Path: "tasks[].instructions_file", Scope: settingschema.ScopeWorker, Type: "string", Values: "relative path inside the config directory, at most 16 KiB of UTF-8", Default: `""`, Example: "prompts/weekly-digest.md",
		Description: "Prompt task: the operator's instructions, read once at start; never taken from a KB."},
	{Path: "tasks[].tools", Scope: settingschema.ScopeWorker, Type: "list", Values: "names from the worker's default allowlist, never a denied tool", Default: "[]",
		Example:     "- changes_since\n- concept_read\n- concept_write",
		Description: "Prompt task: exactly the tools the model may call; nothing is implicit."},
	{Path: "tasks[].every", Scope: settingschema.ScopeWorker, Type: "duration", Values: "Go duration of at least 1h (168h is a week)", Default: `""`, Example: "168h",
		Description: "Prompt task: how long after its last run on a KB it is due again."},
	{Path: "tasks[].kbs", Scope: settingschema.ScopeWorker, Type: "list", Values: "a non-empty subset of the perimeter's kbs", Default: "[]",
		Example:     "- kb-a",
		Description: "Prompt task: the KBs it runs on; omit it for every KB of the perimeter."},
	// Task kind poll (D1262).
	{Path: "tasks[].default_interval", Scope: settingschema.ScopeWorker, Type: "duration", Values: "Go duration at or above min_interval", Default: "24h", Example: "12h",
		Description: "Poll task: how long after its last check a feed is due again, unless the Source sets feed_interval."},
	{Path: "tasks[].min_interval", Scope: settingschema.ScopeWorker, Type: "duration", Values: "Go duration above zero", Default: "1h", Example: "1h",
		Description: "Poll task: the floor of a Source's feed_interval; a feed asking for less is recorded as bad_field."},
	{Path: "tasks[].max_entries", Scope: settingschema.ScopeWorker, Type: "int", Values: "at least 1", Default: "200",
		Description: "Poll task: the most entries one listing of a feed may return; a recipe past it splits the listing or fails."},
	// Task kind synthesis (D1263).
	{Path: "tasks[].synthesis_map", Scope: settingschema.ScopeWorker, Type: "string", Values: "a top-level map name", Default: `""`,
		Description: "The map the synthesis task writes its hypotheses into; empty means the map of the newest Contradiction, and the task never creates a map."},
	{Path: "tasks[].synthesis_max_candidates", Scope: settingschema.ScopeWorker, Type: "int", Values: "at least 1", Default: "5",
		Description: "Candidates the synthesis task examines per run."},
	{Path: "tasks[].synthesis_max_new", Scope: settingschema.ScopeWorker, Type: "int", Values: "at least 1", Default: "3",
		Description: "Hypotheses the synthesis task writes per run; the run stops at once when it is reached."},
	{Path: "tasks[].synthesis_context_tokens", Scope: settingschema.ScopeWorker, Type: "int", Values: "at least 500", Default: "6000",
		Description: "Cap, in tokens, of the page text the synthesis task sends to the model for one candidate."},
	{Path: "tasks[].synthesis_reserve", Scope: settingschema.ScopeWorker, Type: "string", Values: "a fraction above 0 and below 1", Default: `"0.2"`,
		Description: "Fraction of the profile's daily budget the synthesis task leaves untouched: it starts only above it."},
}}
