---
topic: deployment-release
---

# D83 — Service install robustness: create the data dir, tolerate its absence, stable plist binary path

**Status: implemented (2026-07-23).**

**Context.** `service install` (D73) wrote a config pointing at `~/cartographer-data` but never created that
directory, and `serve`'s `discoverKBPaths` (`cmd/cartographer/serve.go`) called `os.ReadDir` on it unconditionally,
`log.Fatalf`-ing when the dir was missing — a fresh `brew install` produced a launchd `KeepAlive` crash-loop.
Separately, the plist embedded the `filepath.EvalSymlinks`-resolved binary path, i.e. the **versioned** Homebrew
Caskroom path (`/opt/homebrew/Caskroom/cartographer/<ver>/…`): every `brew upgrade` removed that path and broke the
service until `service install` was re-run.

**Decision.**

a) **`Manager.Install`** (`internal/service/manager.go`) now `os.MkdirAll`s the data dir (the value from a
freshly-generated config, or read back from an already-existing one) right after resolving the config path, and
logs `created <dir>` to stderr when it does.

b) **`discoverKBPaths`** (`cmd/cartographer/serve.go`) treats a missing data dir like an empty one: on
`os.IsNotExist` it creates the dir, logs to stderr, and returns an empty slice instead of failing startup. The
existing `log.Fatalf` at the call site still fires for other errors (permissions, not-a-directory).

c) **The plist/unit binary path** (`internal/service/paths.go`, `resolveStableBinPath`) is the as-invoked
`os.Executable()` path, no longer `EvalSymlinks`-resolved — except that a stable Homebrew symlink
(`/opt/homebrew/bin/cartographer` or `/usr/local/bin/cartographer`) is preferred over it when the symlink resolves
to the same file, so the recorded path survives `brew upgrade`. `Manager.Status` uses the same resolution so it
reports the configured path.

**Rationale.** All three are the same invariant from different angles: *install must never produce a non-bootable
state, and serve must treat a missing data dir exactly like an empty one.* This **amends D73's assumption** that
the data dir exists once the config is written — D73 correctly specified the empty-dir behavior (0 KB, `/health`
up) but implicitly assumed someone had created the directory; D83 makes that explicit and enforced at both ends
(install creates it, serve tolerates its absence either way).
