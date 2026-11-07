---
name: cartographer-plugins
description: Install, review, update and write Cartographer plugins; use when the user asks to add a plugin, a fork or in-house client, a plugin command or tool, or to author one.
version: "1.0"
---
# Cartographer plugins

A plugin is a git repository or a folder with a `cartographer-plugin.yaml` manifest. It can add clients,
skills, subagents, hooks, commands, MCP tools and sync handlers. It runs on this machine, with the
permissions its manifest declares, only after the user approved the exact version.

## The rule

**Approval is the user's.** You show the preview and wait; the user answers. You never pass `--yes` or `--approve-plugins` unless the user said, in this conversation, to approve that very plugin.

## Install or update

1. Install without approving: `cartographer plugin install <git-url>[@<ref>]` or `cartographer plugin install <folder>`.
   Without a terminal it stops at `pending approval`; that is the expected outcome.
2. Show what it would do: `cartographer plugin show <name>` (add `--files` to read the files that run). Summarize it
   for the user in plain words: the permissions (KB access, hosts, files, environment variables), the files with
   the execute bit, the scripts, the commands, tools and events it registers, the clients it adds.
3. **Stop.** Ask the user whether to approve this version. On their yes, either ask them to run
   `cartographer plugin approve <name>` in their terminal, or, only when they told you to, run it with the
   consent flag.
4. `cartographer sync` delivers the approved content. `cartographer plugin list` and `cartographer status` show
   the state of each plugin.

An update does not change what runs: `cartographer plugin update <name>` fetches the new version and prints the
diff, and the approved version stays active until the user approves the new one. Read the diff to the user,
permissions added first, and stop again.

`cartographer plugin disable <name>` turns one off, `cartographer plugin remove <name>` retires what it installed.
`cartographer plugin config <name> key=value` sets a setting declared by the manifest; it needs no approval and
never widens a permission. Put no credential in a setting.

A plugin command runs as `cartographer <plugin> <command>`; `cartographer <plugin>` lists them. A plugin tool is
served by the local stdio bridge as `<plugin>_<tool>`, hyphens in the plugin name becoming underscores (the name `plugin validate` and `plugin show` print), for a provider that uses it.

## Write a plugin

1. `cartographer plugin new <name>` scaffolds a folder; `cartographer plugin validate <dir>` lists every problem and
   prints what the scripts register. Run it after every edit.
2. Contribution points are top-level folders: `skills/`, `agents/`, `hooks/`, `providers/`. A script is
   `plugin.star` (Starlark) with optional `lib/*.star`; it registers `command(...)`, `tool(...)` and `on(...)`.
3. Declare the least the plugin needs under `permissions:` in the manifest. A script can use `kb.call`, `http.request`,
   `files.*` and `env.get` only for what is granted, and runs no process. Settings are read through `settings`.
4. Read the preview as the user will: `cartographer plugin install <dir>` then `cartographer plugin show <name>`.
   If you would hesitate to approve it, so will they: narrow the permissions.

The reference, with a worked example, is `docs/plugins.md` in the Cartographer repository.
