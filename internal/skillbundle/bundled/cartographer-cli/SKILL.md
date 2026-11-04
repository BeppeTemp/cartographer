---
name: cartographer-cli
description: Use the Cartographer Knowledge Base from a shell, only when its tools are not available as MCP tools in this session. Calls any tool with `cartographer call`.
version: "1.0"
---
# Cartographer through the command line

## 1. When it applies

Only when no Cartographer tool (`search`, `concept_read`, `kb_status`, ...) is in your tool list and
you can run shell commands. If the tools are there, use them and ignore this skill.

## 2. The prefix

Every command starts with `cartographer call --provider <name>`. Take `<name>` from the Cartographer
block of the client's instructions; if it is not there, ask the user. Never guess one. The scope is
the provider plus the current directory: run from the project folder. The command writes no file.

## 3. Orient

```
cartographer call --provider <name> --list
cartographer call --provider <name> --describe <tool>
```

`--list` prints one tool per line. Run `--describe` only for the tool you are about to use, never for
all of them: it prints the description and the input schema.

## 4. Call a tool

```
cartographer call --provider <name> <tool> key=value key=value
```

- A value that parses as JSON is typed: `limit=5` is a number, `flag=true` a boolean,
  `ids=["a","b"]` an array. Anything else is a string: `query=hello world`.
- A string that looks like a number or a boolean is passed quoted: `id='"2026"'`.
- In a POSIX shell single-quote every value that has spaces or special characters.
- A complex or multi-line body goes through stdin as one JSON object, not as `key=value`:

```
cartographer call --provider <name> <tool> --json - <<'JSON'
{"kb": "kb-a", "path": "notes/x", "content": "line 1\nline 2"}
JSON
```

- The same key twice, in pairs or in `--json`, is an error.
- `--output json` prints the raw MCP result instead of the text.
- Never put a secret on the command line: it ends up in the process list and the shell history.
  Use the `secret_set` tool through `--json -`, or ask the user.

## 5. Exit codes

- `0`: the result is on stdout.
- `1`: the tool ran and refused or failed; its message is on stderr. Read it and act on it.
- `2`: the command failed (usage, configuration, no KB in scope, transport, authorization,
  timeout); the message is on stderr after `Error:`. Fix the command or report it to the user; do
  not retry blindly.

## 6. Rules

- Pass `kb` explicitly to every tool that takes it, as `kb=<name>`, and never infer it.
- Read procedures, runbooks and incidents whole with `concept_read`, never answer from a `search`
  excerpt or a snippet.
- A refused write is reported to the user, not worked around.
- `kb_guide` returns the KB's own directives and skills: call it when a task touches the KB's own
  rules.
- Status convention: `active` means the page is valid and current, not that work is open. Open work
  is `open`, `in-progress` or `blocked` (with `waiting_on` and `review_after`); finished is `done`.
  When you start an `open` item, set `in-progress` in the same write that records the first progress.
- Every write is a git commit.
