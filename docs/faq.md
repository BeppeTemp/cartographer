# Frequently asked questions

## What happens when two agents write the same page?

It is not a silent last-write-wins. There are two layers.

**On one server, optimistic concurrency.** A write can carry `if_match`, the content
hash of the version the agent read. If the page changed in the meantime the write is
refused with `stale_write`, and the agent re-reads and merges. Nothing is overwritten
quietly.

**Across machines, git conflicts.** Each KB is a git repository and every write is one
commit. When a sync hits a real conflict the rebase stops, the KB is marked `degraded`
and `conflicts_list` shows what is in conflict. `git_conflict_resolve` then requires an
explicit choice per concept (`ours`, `theirs` or `edit`, a merged text), and only then
completes the merge, commit and push. The `cartographer-kb-conflict-resolve` skill walks
an agent through this. Writes are small, separate commits, so a conflict usually touches
one page and any resolution can be reverted.

The tooling guarantees that nothing gets lost, not that every merge is trivial. See
[concurrency.md](concurrency.md).

## Is `cartographer sync` atomic across clients?

No, and it does not claim to be. `sync` downloads and verifies the manifest first, then
updates the MCP entries and the client configuration, then materializes the artifacts
provider by provider, with a preflight and checkpoints in the lockfile.

If a provider fails, what already completed stays, and that provider may leave partial
files. There is no automatic rollback of the whole projection: running `sync` again
reconciles it, and `cartographer status` shows where things stand. See
[sync.md](sync.md).

## Can `cartographer sync` live next to chezmoi or another dotfiles manager?

Yes, as long as the two do not write the same paths. A dotfiles manager owns your
hand-written configuration; Cartographer owns the files it generates, which its lockfile
tracks. If generated files must stay under a dotfiles manager, it will see them as
drift, so ignore those paths on its side.

## Why not just a `CLAUDE.md`, or Notion, or Obsidian?

They are good choices for what they cover. Cartographer starts where they stop.

- **`CLAUDE.md` / `AGENTS.md`** belongs to one repository and is mostly read-only for the
  agent. Knowledge that crosses repositories (infrastructure, incidents, decisions) has no
  natural home there. Keep a small instruction file anyway: it is where the pointer to the
  KB lives.
- **Notion** is reached through connectors that are not built to govern many automated
  writers: there is no per-write commit, schema, link check or lint.
- **Obsidian** is typically one person on one machine. A KB is plain Markdown and YAML in a
  git repository, so you can read it in Obsidian, but the point is that several people and
  agents on different machines and clients share one KB.

What Cartographer adds is governance over many automated writers: agents write only
through MCP tools, and the server enforces schema, link checks, lint, optimistic
concurrency, one commit per write, conflict handling across machines, a tamper-evident
audit log and tokens scoped per KB. It is client-neutral: the same KB hands each agent the
same knowledge, skills and instructions in the client's native format.

If your knowledge fits in one repository and one person, a plain instruction file is
simpler and you should use it.
