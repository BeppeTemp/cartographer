---
name: cartographer-cli
description: Use the Cartographer Knowledge Base from a shell, only when its tools are not available as MCP tools in this session. Calls any tool with `cartographer call`.
version: "1.1"
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
cartographer call --provider <name> --describe <tool>[,<tool>...]
cartographer call --provider <name> --instructions
```

`--instructions` prints the server's instructions for this scope: read it once per session, before
the first call. `--list` prints one tool per line. Run `--describe` only for the tools you are about
to use, never for all of them: it prints the description and the input schema (several names give a
JSON array).

## 4. Call a tool

```
cartographer call --provider <name> <tool> key=value key=value
```

- Values are typed by the tool's input schema: a string property keeps your text as it is
  (`query=2026`, `query=true` stay strings), an array property takes one value as a one-element
  array (`where=status=open`) or a JSON array (`ids=["a","b"]`), a number or boolean property parses
  `limit=5`, `flag=true`. Anything else is a string: `query=hello world`.
- `--json` is sent as it is, with no typing.
- In a POSIX shell single-quote every value that has spaces or special characters.
- A complex or multi-line body goes through stdin as one JSON object, not as `key=value`:

```
cartographer call --provider <name> <tool> --json - <<'JSON'
{"kb": "kb-a", "path": "notes/x", "content": "line 1\nline 2"}
JSON
```

- The same key twice, in pairs or in `--json`, is an error.
- `--output json` prints the raw MCP result instead of the text; it is for scripts, read the text yourself.
- Never put a secret on the command line: it ends up in the process list and the shell history.
  Use the `secret_set` tool through `--json -` (or a line of `--batch -`: stdin is the place), or ask
  the user.

## 4a. Several calls at once

```
cartographer call --provider <name> --batch - <<'JSONL'
{"tool": "search", "arguments": {"kb": "kb-a", "query": "backup policy"}, "id": "s"}
{"tool": "concept_read", "arguments": {"kb": "kb-a", "id": "infra/backups"}, "id": "r"}
JSONL
```

One JSON request per line, one JSON result per line in the same order: `line`, `id` (yours, echoed),
`ok`, then `text` or `error` (`code` and `message`). The requests run one at a time, in order. A
failing line does not stop the batch; the exit code is non-zero if any line failed, so read every
result line.

## 5. Exit codes

- `0`: the result is on stdout.
- `1`: the tool refused or failed, or an argument was invalid; its message is on stderr. Read it and
  act on it.
- With `--batch -`: the worst code among the lines; each line's own code is in its result.
- `2`: the command failed (usage, configuration, no KB in scope, transport, authorization,
  timeout); the message is on stderr after `Error:`. Fix the command or report it to the user; do
  not retry blindly.

## 6. Quick reference

Run `--describe <tool>` for the exact fields. The core read calls:

```
cartographer call --provider <name> search kb=kb-a query='backup policy'
cartographer call --provider <name> graph_context kb=kb-a query='backup policy'
cartographer call --provider <name> concept_read kb=kb-a id=infra/backups section='Restore'
cartographer call --provider <name> concept_read kb=kb-a id=infra/backups outline=true
cartographer call --provider <name> concept_read kb=kb-a id=infra/backups match='retention'
cartographer call --provider <name> work_list kb=kb-a where=status=open
cartographer call --provider <name> concept_list kb=kb-a scope=infra
```

- Prefer `graph_context` to reading page after page: it ranks the linked pages in one call.
- Read the section, the outline or the matches of a page, not the whole page, unless it is a
  procedure, runbook or incident.
- Several independent calls: one `--batch -`, not one command each.
- When the client's instructions list no KB, a call without `kb` lists the KBs in scope.

## 7. Rules

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
