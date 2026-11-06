---
topic: sync-provisioning
---

# D1736 — a client-side write follows a symlink only when the operator listed it

**Decision.** The refusal of D148 stays the default. `providers.<provider>.allow_symlinks` in the operator's own `.cartographer.yaml` lists links (the link itself, a file or a directory) that writes follow; an unlisted link is refused exactly as before, and the refusal, `doctor`, `disconnect`, `reconnect` and the sync summary name the key as the alternative to replacing the link.

**Why.** Users who keep agent config in a dotfiles repository (GNU Stow, chezmoi) link whole directories or files into the client base dir, and with the refusal alone they cannot use Cartographer with that agent at all. The cost is a second way to write outside the client directory, so it is opt-in per link, never inferred, and read only from the home-directory config (a cloned repository cannot choose which links are followed).

**How it works.**
- One process-wide allow set in `internal/safewrite`, filled once at start from the union of every provider's entries (the key lives under a provider for readability and for `cartographer client symlinks`; safewrite does not know the provider). A missed registration fails closed.
- Matching is lexical on the logical path under the base dir. A listed directory link covers everything below it; a link nested below needs its own entry; every hop of a chain must be listed.
- The link is preserved: the atomic writer resolves a listed leaf link and renames over the target. A followed leaf link is never removed and its target never deleted; a cleanup that would delete the file writes the emptied content through the link.
- Lockfile paths stay logical, never a resolved target.
- A target that is not writable is a per-artifact refusal naming link and target. `--dry-run` reports `would write through symlink X -> Y` and does not probe writability. `doctor` reports a listed link as info and an unlisted one as a warning whose fix names the key.
- `TrustCodexProject` ignores the allow set (`safewrite.CheckPathStrict`): its refusal tells the operator to add the lines by hand.

**Alternatives rejected.**
- A CLI flag: the choice is durable wiring, not a per-run mood.
- Threading the allowed set as a parameter through every call site: larger, and the next new writer would be unprotected by default.
- Per-provider sets: safewrite cannot tell which provider writes a path, and a path the user listed is a path the user listed.
- A warning when the target lies in a git repository: rejected in D148 and unchanged.
- Following every link: D148's reason (a write landing in an unrelated repository with a false provenance footer) stands.

**Consequences.** A new raw `os.Remove` or `os.Rename` after `CheckPath` would replace a followed link, so removals go through `safewrite.Remove`. A user with a listed link needs no migration, and nothing changes for a config without the key.
