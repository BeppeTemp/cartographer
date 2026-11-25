---
topic: sync-provisioning
---

# D2401 — generated client state is never written through a link below the config root

**Decision.** The three writers of generated client state under the user config directory (`SaveDerived` for `workspace-derived.json`, `WriteToken` for the token file, `SaveCache` for `repos.json`) write through `internal/safewrite` (`MkdirAllNoFollow`, `WriteFileAtomicNoFollow`) rooted at the user config directory, the parent of `cartographer/`: a symlinked `cartographer` directory or a symlinked leaf is refused with an error naming the link, and a symlinked `~/.config` keeps working.

**Why.** `MkdirAll` and `CreateTemp` follow a symlinked directory and `os.WriteFile` follows a leaf, so a link planted in the client's config path redirected a write outside it (D148 closed the same hole for provisioned artifacts). The root is the config directory and not the home because dotfile managers link `~/.config`; the cost is that a user who links only `~/.config/cartographer` now gets a refusal instead of a silent write through it.

**Alternatives rejected.** Rooting at the home: still correct but refuses nothing between home and `~/.config`, and a linked `~/.config` is legitimate. Honouring `allow_symlinks` (D1736): this state is machine-generated and has no per-provider owner, so there is nothing to list. Covering `Save` of `.cartographer.yaml` too: its leaf link is how dotfile managers keep the file (`TestSaveFollowsASymlinkedConfig`), unchanged on purpose.

**Consequences.** A new writer of generated state under `~/.config/cartographer/` uses the same two calls. `repos.json` is now written atomically. `WriteToken` takes the directory it is given as its own root (the home or a chosen config directory, which may be a link) and refuses a symlinked token file.
