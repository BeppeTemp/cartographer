---
topic: deployment-release
---

# D156 — Service `PATH`, `restart` after `stop`, and a `kb create` that keeps its scaffold

**Status: implemented (2026-08-28).** Amends [D73](D73-local-mode-as-a-native-service-cartographer-service.md) (native local service) and
[D134](D134-kb-create-requires-a-git-remote.md) (`kb create` requires a remote). Closes #177.

**Context.** Three defects on the commands an operator runs on day one, each turning a
correct installation into something that reads as broken. From a field report on a large
migration.

1. `RenderLaunchdPlist` and `RenderSystemdUnit` set no environment at all, so the server
   inherited launchd's minimal `PATH` (or systemd's equally short one). A
   Homebrew-installed `sops` sits outside both, so every secret resolution failed with
   `secret_resolve: sops binary not found in PATH` — accurate and uninformative, from the
   definition `service install` itself had generated.
2. `Stop` used `launchctl bootout`, which **unregisters** the job, while `Restart` used
   `launchctl kickstart -k`, which requires a registered one. So `service stop` followed by
   `service restart` failed with `Could not find service … in domain` and only `start`
   worked.
3. `kb create` committed as the product default `cartographer@localhost` — `kb.Init`
   hardcoded it — which any forge with an author-membership push rule rejects; and on push
   failure `cmdKBCreate` deleted the whole scaffold. The outcome was no KB at all and a
   manual redo (`--no-remote`, amend the author, add the remote, push).

**Decision.**

- One `servicePATH(binPath)` feeds both definitions, so they cannot drift: the binary's own
  directory first (whatever installed Cartographer is the likeliest place to hold its
  companions), then `/opt/homebrew/bin`, `/usr/local/bin`, `/opt/local/bin`, then the
  platform default. **Curated and fixed rather than copied from the installing user's
  shell**, which would bake that user's whole environment into a service definition.
- `stop` keeps the job registered: on darwin it `disable`s it — necessary because the plist
  sets `KeepAlive`, which would otherwise restart the process immediately — and sends
  `SIGTERM`. `start` re-enables. `restart` falls back to `start` when the job is not
  registered, so a job booted out by an older version's `stop` stays restartable across the
  upgrade. `uninstall` keeps `bootout`: removing the definition is what uninstalling means.
  Registration is probed with `launchctl print`'s exit status, never by parsing its
  localized prose.
- `kb create` resolves the author in the same order the server does (service config, then
  git's own identity, then the product default) via a new `kb.InitWithIdentity`; `kb.Init`
  keeps its signature and its previous behaviour. It warns *before* pushing when it falls
  back to the default, which is when the operator can still act.
- **On push failure the scaffold is kept.** The local work is valid; only the push failed.
  The command prints the `commit --amend --author` and `push -u origin` lines and the
  `rm -rf` alternative, naming the data dir a server would otherwise auto-mount. The
  deleted-scaffold guarantee D134 inherited from `kb clone` protected against a
  half-provisioned directory being discovered; a *complete* scaffold does not need that
  protection, and paying for it with "no KB at all" was the worse trade.

**Consequences.** An already-installed service keeps its old definition: the `PATH` fix
needs a `service install` re-run, stated in `docs/deployment.md`. `TestRestart_Darwin_UsesKickstartK`
now expects `print` → `enable` → `kickstart -k`, and
`TestCmdKBCreateRemoteFailureCleansScaffold` became
`TestCmdKBCreateRemoteFailureKeepsScaffold` — both were asserting the old behaviour, which
is the behaviour this entry reverses. The initial commit message also moved from Italian
(`init: KB inizializzata`) to English, the only such string on that path.
