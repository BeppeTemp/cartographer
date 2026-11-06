---
topic: client-configurator
---

# D1727 — Fetched plugins are a content-addressed cache; the user's file records what is approved

**Decision.** A fetched plugin tree is stored under the user cache directory at `plugins/<name>/<digest>/`, the digest being the one `ReadTree` computes, and the user's `.cartographer.yaml` records, in a `plugins` map written only by the plugin commands, what is installed and what the user approved, each as a digest. One function, `plugin.State`, decides whether a plugin is active: it is enabled, a digest is approved, and the stored tree of that digest reads back intact and still valid. A newer installed digest never changes it. The `plugins` schema row is `Hidden`: it is in the table the struct guard reads and in no comment, view or example file until the feature is turned on.

**Why.** The approval is of bytes, so what runs has to be the bytes approved. Naming the directory by the digest and recomputing it on every read makes an edit of the cache visible (`modified`) instead of silently running, and storing the tree that was read back (not the one fetched) means validation and storage see the same bytes. The cache can be thrown away because the source is recorded; the approval cannot, so it lives in the user's file.

**Alternatives rejected.** Storing under the state directory (an approval would then depend on a directory the user may back up and restore with stale trees; a cache is rebuilt by fetching). Keying by `ref` or commit (a folder has no commit, and a moved tag would change bytes under an approved name). Trusting the stored tree without recomputing (a stray write would run unreviewed). A lock for readers (a read racing a write sees either state or `not fetched`, and the digest check covers the rest). Deleting the whole entry on a corrupt same-digest directory instead of replacing it (the plugin would need a new approval for identical bytes).

**Consequences.** Every consumer of plugins calls `State` and nothing else. Writers (`Put`, `GC`) run under the client lock. A host without the POSIX execute bit refuses to store a tree that has an executable file, because the digest includes the bit and could not be recomputed there. The name rule is repeated in `internal/clientconfig` (which cannot import `internal/plugin`); a test keeps the two equal. Turning the feature on clears `Hidden` on the row and records the description in `internal/settingschema/testdata/descriptions.tsv`.
