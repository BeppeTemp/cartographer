# AGENTS.md — provisioning and the client side

This package writes into **other people's home directories**. That is the whole
reason it has its own rules: a defect here does not fail a test, it silently
modifies files a user did not ask about.

## What you must not get wrong

- **Never write to a path without `Lstat`ing every component first.**
  `os.WriteFile` on a symlinked path opens the *target* with `O_WRONLY|O_TRUNC`:
  it does not replace the link, so a symlinked client directory diverts the write
  into an unrelated git checkout, stamped with a provenance footer naming
  Cartographer as its source (D148). The guard is `internal/safewrite`
  (`safepath.go` here keeps `ensureSafeDir` and `writeFileNoFollow` as wrappers):
  a client-side write, mkdir or remove goes through it with the client base dir,
  as do the ones in `internal/configurator` and `internal/blocktext`.
- **A hand-curated user file is never rewritten, only its managed block is.**
  `internal/blocktext` does marker-delimited substring replacement and nothing
  format-aware, on purpose: comments, ordering and every other byte survive
  (D58). If you find yourself parsing the user's TOML or JSON to edit it, stop.
- **A file the user shares with us is written atomically and only when it changed** (`safewrite.WriteFileAtomicNoFollow`: temp file, fsync, rename, mode kept, no write for identical bytes): the instructions block, `blocktext`, the lockfile and the client JSON configs. `os.WriteFile` truncates first, and a sync runs inside a session-start hook with a timeout (#1502). A file we own whole keeps `writeFileNoFollow`.
- **One sanctioned exception: `TrustCodexProject` (D1074).** It appends a
  `[projects."<dir>"]` / `trust_level = "trusted"` table to the user's Codex
  `config.toml`, outside any managed block, only for `workspace bind codex --trust`
  (an explicit flag), only when the file says nothing about the project and does
  not declare `projects` in another form, and never through a link. It is textual
  (the old bytes stay an exact prefix). Anything beyond that, another key, caller
  or provider, is a bug.
- **Merging into a client's MCP config is non-destructive**: entries belonging to
  other servers are preserved (D23). A full rewrite of the file is a bug even when
  the result looks right on your machine.
- **A provider's capabilities are declared, not inferred.**
  `internal/configurator/registry.go` holds one descriptor per provider, and in
  `destinationMatrix` (`provisioning.go`) the unsupported cells are explicit with a
  stated reason, so `Unsupported` means "no approval unblocks this" (D50). Do not
  make a destination up for a provider that does not document one.
- **A capability is declared in `capabilities.go` and in the ledger together.** `absent` follows from the destination matrix and `hookEventReach`; `verified` needs a probe recorded in `docs/harnesses.md`. A PR that flips a destination or a hook reach changes the `capabilities` block of that provider there too, or `TestCapabilityTableMatchesLedger` fails.
- **This matrix is the repository's answer about a client, including in prose.**
  `destinationMatrix` and `workspacescope.go` (D193) are what `docs/sync.md` and
  `docs/interoperability.md` restate for a human, and a restatement drifts (D207):
  if you re-audit a client, change the matrix and those pages in the same PR.
- **A marker block that is not exactly one begin line then one end line is refused, never edited** (`blocktext.ReplaceBlock`, `Corrupted`): no append, no cut, a per-artifact refusal (`IsRefusal`) pointing to `doctor`. The instructions block and the `blocktext` writers follow it; the provenance stamp, `internal/kb/genindex.go` and the `info/exclude` writer do not yet. A new marker-block writer uses `blocktext`, not its own `strings.Index` (#1502).
- **A KB's tree is read through no symlink** (`kblinks.go`): the manifest builder and `ReadArtifactFiles` refuse a link as the file, above it or as the kind's directory, and leave the artifact out with an issue; git stores links and the tree comes from a remote. A new reader of a KB's files does the same with `firstKBLink` or `d.Type()`. The client-side hash of what was materialized does not.
- **A Cline dispatcher is a file in the user's Documents folder, owned by a marker and never by a lock entry** (`clinehooks.go`, D1663). It is written, rewritten (mode kept: the exec bit is the user's per-hook toggle) or removed only while it carries `clineDispatcherMarker`; a file at that path without it is skipped and reported, never read past its head or merged. `clineDocumentsDir` is the only place that may leave the client base dir, and nothing else may be written there; no directory in it is ever removed (`Cline/Hooks` is the extension's), and a link refused by safewrite is a per-event warning, never an error. Every safewrite call for it takes the Documents folder as root (a linked Documents is exempt, a link below it is refused), never the base dir.
- **Prune is as dangerous as write.** It removes what the lockfile says we own;
  anything outside it is someone else's. Deleting an empty directory has
  boundaries for the same reason (D63).
- **The usage scan (`usage.go`, D326) reads transcripts, so it reads tool calls only.**
  A user message, assistant text or reasoning item is never decoded into anything it
  keeps, and a skill's name in prose is not a use: only a tool call carrying a path the
  lockfile materialised, or an explicit activation event. `TestScanUsage_*IgnoresUserMessages`
  pin it. It is read-only on client directories, and a transcript shape it does not know
  is skipped, never fatal.
- **A synthetic hook is written by `ensureSyntheticHook` and nowhere else.**
  `cartographer-bootstrap` (D60) and `cartographer-write-findings` (D353) share
  the no-follow writers, registration and lock bookkeeping; a new one is a
  `syntheticHook` value plus its name in `reservedHookNames`, which keeps a KB
  hook of that name out and the entry out of `ComputeDiff`'s orphans. Its script
  never fails a session: a hook's non-zero exit is feedback or a block.
- **Client-owned text of the instructions block needs a rewrite trigger of its own.**
  The shared operational section (D1182) is in no artifact `ContentHash`, so editing it fires
  no `Added`/`Updated`; it is folded into the hash recorded as `PathsSectionHash`
  (`pathsSectionHash`). New client-written text in the block does the same, with a
  convergence test (`TestShared_Convergence`). Text that depends on what only a sync
  holds (the client environment section: approvals, resolver notes) cannot join that
  hash, because `status` recomputes it without them and would report drift forever: its
  trigger is the whole body compared with the block on disk (`blockBodyIs`, which also
  covers the `@AGENTS.md` import; `TestEnvironmentSection_ConvergesWhenSkippedChanges`).
- **An artifact rewritten client-side is rewritten after `VerifiedManifest` and rehashed**: a signature covers
  `ContentHash` and `Apply` trusts the `Signed` flag without re-verifying (`WithoutSkillsLine`, D1408).
- **Names and file paths from a server are validated before they are compared or joined**: `validArtifactName` (also behind `ownableName`) and `ValidArtifactFilePath`/`CheckArtifactFilePaths` (a refusal, `IsRefusal`: one artifact skipped). Anything keyed on a spelling (the `SKILL.md` approval and stamp checks) relies on the path being canonical; a new place that takes a name or path off the wire uses them.
- **A new `Artifact` of derived content must resolve approval and provenance to its origin** (`Artifact.Origin`, used by `ApprovalName`, `ArtifactRequiresApproval`, `artifactAuthorized` and `provenanceBlock`): the derived bytes carry none of the keys that gate the origin, so a derived skill judged on itself installs a gated agent ungated (`TestDegradedSkillInheritsAgentApproval`).
- **Read a destination cell through `cellFor`, never the matrix map**: a client variant's cells are runtime data (`variantcells.go`); a new loop over the matrices skips them, and a variant's root is a prune boundary only through `isVariantBoundary`. A variant resolves containment before any write and writes through safewrite like every provider.
- The lockfile is the client-side record of applied state. Changing its shape is a
  migration, and an empty new field must keep meaning what the files written
  before it meant (see `base_dir`, empty for a global projection, D193).
- **A derived workspace binding (D1019) is applied with no command run in the
  repository, so it never edits a tracked file.** The instructions block goes
  through `ApplyOptions.InstructionsRel` (Claude Code's `CLAUDE.local.md`) or is
  dropped, Claude Code hooks register through `ApplyOptions.HookSettingsRel`
  (`settings.local.json`, D1198); anything new a projection writes into a shared
  file needs the same answer for a derived binding.

## Scope note

The repository's own `.claude/skills` and `.claude/agents` are real directories (D992). Because of D148 this package will refuse to materialize
into them, which is the correct outcome: those are repo-scoped skills, versioned
here, and not something a KB sync should own.
