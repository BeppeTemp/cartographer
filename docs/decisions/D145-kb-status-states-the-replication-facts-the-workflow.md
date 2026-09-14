---
topic: concurrency-git
---

# D145 — `kb_status` states the replication facts; the workflow field is named for what it means

**Decision.** The field describing the write workflow is emitted as `git_workflow`
(`local` | `server`) by `kb_status` and `sync_status`; the previous keys `git_profile`
and `profile` remain as deprecated aliases for one minor release. The `Profile` field of
`kb.ServerGitState` and the `kbs[].git_profile` YAML key are unchanged: this is an output
rename only. `kb_status`, the agent-visible tool, additionally reports `has_remote`,
`remote_url` (`origin`, with any credentials redacted), `git_sync`, `push_state`,
`push_last_error` and `unpushed_commits` (`null` when no trustworthy remote-tracking
comparison exists). It stays read-only and network-free: the ahead/behind half that needs
a fetch remains in `sync_status`/`sync_pull`, and `sync_status` stays advanced (D65).

**Rationale.** These fields are read by agents that reason over them and act. `git_profile:
"local"` describes the write workflow (commit-and-push vs the D117 PR boundary), but next
to five empty PR fields it reads as "this KB has no remote" — an agent drew exactly that
conclusion on a fully synced KB and came within one step of recommending a rebuild from
scratch. The only tool carrying replication state was hidden from the default `agent`
profile, so nothing in the agent's reach could correct it. Naming the field for what it
means, and putting the remote facts where the agent is already looking, removes the
misreading without growing `tools/list`. The remote URL is redacted because it flows into
an LLM context and into audit output.
