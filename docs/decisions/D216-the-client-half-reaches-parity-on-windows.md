---
topic: sync-provisioning
---

# D216 — The client half reaches parity on Windows, and the executable flag becomes lockfile state

**Decision.** The client works on Windows, and where the two platforms genuinely
differ the difference is a build-tagged file pair or a declared per-OS entry, never
a `runtime.GOOS` branch in shared code. Six changes carry it:

- **The executable flag is recorded, not re-observed.** `ManagedFile` gains
  `executable_paths`, the artifact-relative paths a sync wrote executable. Where the
  filesystem has no execute bit (`internal/execbit`), the on-disk re-hash takes the
  flag from there instead of from the mode. `ContentHash` is untouched.
- **The bootstrap script is per platform**: `bootstrap.sh` on unix, a `bootstrap.cmd`
  with the same three guarantees on Windows, and the `os.Chmod` that grants nothing
  there is guarded rather than left to return `nil`.
- **A hook command survives a space and a backslash.** The leading token is quoted on
  the way out and un-quoted on the way in — so quoting is idempotent — and "relative"
  means "contains either separator".
- **A stdio MCP command may name a Windows path**: parentheses are allowed for an
  absolute command only, so `C:\Program Files (x86)\srv\srv.exe` is expressible on
  every host.
- **`~\` expands**, from one implementation instead of three, and a configured search
  root that is missing or unreadable is reported as a warning naming it.
- **The D148 destination guard refuses everything that is not a plain file or a plain
  directory**, and `internal/sops`' path walk is brought in line.
- **Agent detection reads per-OS application directories and env-anchored config
  directories**, so `%APPDATA%\opencode` is expressible.

**Why.** D215 made the module compile and D219 made its tests pass on Windows;
neither made the *client* work there. The failures were not evenly bad. Some were
loud (`connect` refusing every stdio MCP server), one was worse than loud: an
artifact reporting drift that no repair can clear, on every `doctor` run, forever.
That does not annoy an operator, it teaches them to stop reading `doctor` — and once
they have, the finding that matters is invisible too. Everything here is either that
class of defect or a silent no-op pretending to be a feature.

The one real cost is a new lockfile field. It is optional and empty means what it
always meant, so nothing migrates; but it is state, and state about intent rather
than about bytes, which is a kind this lockfile did not carry before.

**Alternatives rejected.**

- *Normalise the executable flag inside the hash.* The obvious fix, and it destroys
  the property D75 WP3 and D138 exist to give: an artifact with no placeholder must
  hash on the client exactly as the server published it. Change the digest and every
  installation re-materializes at once. A frozen fixture now fails if anyone tries.
- *Derive the flag from kind and path for every kind, as for hooks.* Exact for a hook
  and pure guesswork for a skill: nothing about `scripts/check.sh` says whether the
  KB shipped it executable. Guessing "any `.sh` is executable" would invent a rule the
  KB never agreed to.
- *Report a Windows mode difference as drift and let `--repair-hashes` adopt it.*
  Adoption records what is on disk as the baseline, so the artifact stops being
  comparable against the server (D157 says so itself). Trading a false finding for a
  blind spot is not a fix.
- *Keep the parenthesis ban and ask KB authors to avoid `Program Files (x86)`.* That
  is most of the software installed on Windows. The ban bought nothing anyway: an
  absolute command reaches `exec.Command` with a separate argv and never a shell.
  It stays in force for a bare name, where the string is short and something else
  decides what it resolves to.
- *A `WindowsAppDir` field beside `DarwinAppDir`.* Two fields today, four when the
  next platform arrives, and the `goos ==` comparison stays in the detection code.
  A `map[goos]string` puts the platform in the data, where the registry already
  keeps every other declared capability (D137).
- *Guess Kiro's and Antigravity's Windows install directories.* Neither could be
  confirmed, and an invented location is a false positive — the same class of defect
  the field exists to remove. `exec.LookPath` already honours `PATHEXT`, so the CLI
  on `PATH` is the detection that matters and the app directory is a bonus. The map
  has room for the entries the day someone verifies them.
- *Fail `sync` on a search root that does not exist.* A machine-local config may
  legitimately name a root that exists on another machine. A warning names the root;
  an error would stop the sync for something optional.
- *Rename `ErrSymlinkDestination` now that it refuses more than symlinks.* Its
  identity is what every caller matches with `errors.Is`, and the incident it records
  was about a symlink. The name stays, the doc comment says the refusal is broader,
  and the message adapts to what was actually found.
- *Widen only `internal/provisioning`, leaving `internal/sops` on `ModeSymlink`.* Same
  shape, same exposure, two answers to one question — a divergence that gets found
  the hard way.

**Consequences.**

- **`ContentHash` and `ContentHashFiles` are frozen.** A test pins the digest of a
  fixture containing an executable file and says why in its failure message. Changing
  the artifact hash is a migration, not a fix.
- **On unix the filesystem stays the authority for the executable bit.** An external
  `chmod` is real drift there and reporting it is the point. The asymmetry lives in
  one function, `ManagedFile.executableOnDisk`, with the reason next to it.
- **`execBitSupported` and `lstat` are test seams**, package variables initialised
  from `internal/execbit` and `os`. `internal/execbit` remains the one place that
  decides whether the host has an execute bit; the seam only lets a test reach the
  other platform's branch, which is the branch a wrong answer makes permanently
  wrong. Do not consult it to *decide* anything.
- **`bootstrapContentHash` is now platform-dependent by construction**, computed at
  init from the platform's script. No test may pin its literal value, and the
  external test package reads the script's name through `export_test.go` rather than
  writing `bootstrap.sh`.
- **An absolute stdio or hook command whose path contains a space must be quoted in
  the KB.** Unquoted, `C:\Program Files\x.exe --flag` is indistinguishable from the
  command `C:\Program` with arguments; the ambiguity is stated where
  `resolveHookCommand` is defined rather than resolved by guessing.
- **`windowsCleanPath` was not cleaning anything.** Its separator swap was written
  `\\` in a Go raw string, which matches a *doubled* backslash and so matched
  nothing: `C:\srv\..\srv.exe` was reported clean and accepted. Fixed here, with a
  table that would have caught it. This narrows what is accepted — an unclean
  backslash path is now refused, as the rule always intended.
- **Tilde expansion has one implementation**, `repoindex.ExpandHome`. There were
  three (`repoindex`, `internal/provisioning`, `cmd/cartographer/resolve.go`) and each
  had to be found separately to learn that `~\` was never expanded. A new caller
  imports it; it does not copy it.
- **Windows junction behaviour is still unverified**, and the guard is written so that
  it does not matter: a reparse point is refused whether Go reports it as
  `ModeSymlink` or as `ModeIrregular`. If someone verifies it on a Windows host, the
  guard needs no change — only the comment saying it is unverified goes away.
- **Agent detection may only gain true positives.** A test pins every descriptor's
  binaries, config directories and darwin application directory, so a future edit
  that would stop detecting a client fails instead.
- **`Descriptor.MCPConfigPath` has exactly one legitimate reader of its raw slash
  form**, this package's own consistency test; every other caller already goes
  through `ConfigPath()`. That was audited, not assumed, and the field's comment now
  says so.
