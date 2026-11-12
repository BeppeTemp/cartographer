---
topic: architecture
---

# D2006 — `plugin test` judges a recipe with the production parser, and a recorded replay stands in for the network

**Decision.** `cartographer plugin test <dir>` is a client-CLI command that needs no server, token or model. A Starlark recipe needs no network either; an exec recipe is the folder's own program, run as the user without the approval an install asks for, so the command prints each command before it runs. It
builds each recipe of the folder with the Surveyor's own plugin loader (`surveyor.PluginPoints`) and runs it through the
production recipe types against `tests/<name>.yaml`, then through a fixed list of conformance checks
(`recipetest.Checks`, documented by name in `docs/plugins.md`; a test compares the two). A Starlark recipe's
`http.request` is answered by `starcore.Replay`: recorded exchanges carried on `CallSpec`, that run the checks a live call
makes before the network (method, scheme, credentials in the url, the manifest's `net`, headers, caps, the redirect rules
for a recorded 3xx) and then answer from the recording instead of resolving or dialing. A request with no recording fails naming method
and url. Only `plugin test` sets it.

**Why.** An author must learn that a recipe is wrong before a user does, and the only definition of a valid listing is
the production parser, so the kit calls it rather than re-implementing it: a recipe passes when a Surveyor would accept
what it prints. A recipe that talks to a wiki needs the wiki; recordings make the test repeatable and offline. The cost is
that a replayed call exercises the host's checks but not the dial, so the refusal of a private address and the
`net_private` grant are not covered by it.

**Alternatives rejected.**
- An injected `http.RoundTripper` on the host (the plan's `Host.HTTP`): the runtime's test seams are reachable only from
  test files (`internal/boundaries` pins it), and an arbitrary transport in production would let any caller of the
  runtime route a plugin's requests anywhere. Replay is data, never code: it can only hand back canned answers.
- Faking the whole `http` module in the kit: the host's checks would then be the kit's copy, and a recipe could pass here
  and be denied on a Surveyor.
- A live dry run as the harness: `surveyor source test` already is one, and it needs the real system, a credential and a
  Surveyor config; it cannot be a repeatable fixture.
- Re-implementing the parser in the kit: two definitions of a valid listing drift. The entries check and the list half of
  the system check therefore read the parser's `protocol_error` and do not judge the entries again.

**Consequences.** Nothing the Surveyor runs may set `CallSpec.Replay` or `StarConfig.Tune`, or import `recipetest`:
`TestOnlyThePluginTestKitSetsReplay`, `TestOnlyThePluginTestKitSetsStarConfigTune` and
`TestOnlyThePluginTestCommandImportsRecipetest` in `internal/boundaries` pin them. A scenario's files are regular files of
`tests/` (a symlink is refused) and one scenario reads at most 4 MiB. A check added to `recipetest.Checks` is documented in `docs/plugins.md` in the same change. An exec
script is tested by running it for real, so a scenario's `env` values are set in the process for the run and restored
after. The scenarios live in a top-level `tests/` folder, which the tree reader ignores: they are neither stored nor part
of the digest.
