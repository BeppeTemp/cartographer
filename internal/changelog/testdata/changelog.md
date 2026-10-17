# Changelog

### Migration note

**0.8.0 — semantic search and `index_rebuild` are removed.** The `search` MCP tool no longer accepts `mode` or `use_semantic`: a call passing either now fails with an error naming the removal, instead of silently returning keyword results it did not ask for. The `index_rebuild` MCP tool is gone — `reindex` covers it, with `full: true` for the whole rebuild (`cartographer reindex --full` from the CLI). The server's `--ollama` flag is removed too; a stale `search:` block in `config.yaml` and the `CARTOGRAPHER_OLLAMA`/`CARTOGRAPHER_OLLAMA_MODEL` variables are ignored rather than fatal, so an un-updated configuration does not take the server down. Search is FTS5 keyword search only, and an existing SQLite index keeps working: its embedding table is no longer created or read, and no reindex is required.

**0.7.0 — `cartographer kb create <name>` requires an explicit remote decision**: pass `--remote <url>` to attach an empty repository as the KB's `origin` and push the initial commit to it, or `--no-remote` to keep the previous behaviour and create a local-only KB. Without either flag the command exits 2. Scripted invocations must be updated; `--no-remote` restores exactly what they did before.

KB histories with commits authored as `cartographer <cartographer@localhost>` may need a manual author rewrite before a forge with author push rules accepts the first push.

## [0.24.0](https://github.com/BeppeTemp/cartographer/compare/v0.23.0...v0.24.0) (2026-10-12)


### Features

* a write whose push failed (not a conflict) reports committed, ([fc6e1c2](https://github.com/BeppeTemp/cartographer/commit/fc6e1c2839cedc2deeb5762331fe01fa0b4bad1b))
* Cartographer never starts a kb-doctor session by itself ([#704](https://github.com/BeppeTemp/cartographer/issues/704)) ([5f6e7cb](https://github.com/BeppeTemp/cartographer/commit/5f6e7cbc7fbf7a9ea23ecba2fc2acb9baf851e1f))
* Cartographer never starts a kb-doctor session by itself. The scheduled headless doctor (`doctor schedule|unschedule|status|run`, D369, installed by default since D690) is removed together with its server declaration route (`/api/doctor-schedule`) and its client opt-out field. `connect`, `sync` (the path a Homebrew upgrade reaches) and `upgrade-repair` remove the job 0.22/0.23 installed, printing one line when there was one; `doctor run|schedule|…` now fail as unknown arguments instead of running every check. The nudge always asks the agent to propose a session and wait for the operator (the old `assisted` mode); `doctor_mode` is still validated but ignored, and `kb_status` reports it as `assisted` so a skill synced before 0.24 does not fall back to unattended; the default budget is 10 and the default interval 14 days. The kb-doctor skill (3.0) follows: a session starts only on the operator's request, runs assisted or, when handed over, delegated, and asks a missing fact in the session, recording a gap only when nobody can answer it yet. ([5f6e7cb](https://github.com/BeppeTemp/cartographer/commit/5f6e7cbc7fbf7a9ea23ecba2fc2acb9baf851e1f))
* **cli:** kb create --remote adopts a forge's initial commit ([#732](https://github.com/BeppeTemp/cartographer/issues/732)) ([c80b8b3](https://github.com/BeppeTemp/cartographer/commit/c80b8b3f18c418220c0b9313611adccdd38b3ca6))
* **cli:** kb create/clone --artifact-write mounts the KB with artifact writes on ([#729](https://github.com/BeppeTemp/cartographer/issues/729)) ([11bde3c](https://github.com/BeppeTemp/cartographer/commit/11bde3c18476270f1f11c726761c78d3ae4354e9))
* **dev:** live sessions with make dev, the live-dev skill and the live-implementer subagent ([#703](https://github.com/BeppeTemp/cartographer/issues/703)) ([cf211d0](https://github.com/BeppeTemp/cartographer/commit/cf211d0822a8ae3b4a1aedfcc95ccd9e66653e9b))
* **kb_status:** report behind count, last successful push and last sync-in ([#726](https://github.com/BeppeTemp/cartographer/issues/726)) ([f55b0f7](https://github.com/BeppeTemp/cartographer/commit/f55b0f7cd4706e1692faac98fe6821e87beebc4c))
* **lint:** report map_oversize on the write that pushes a map past its threshold ([#733](https://github.com/BeppeTemp/cartographer/issues/733)) ([95a0ca9](https://github.com/BeppeTemp/cartographer/commit/95a0ca94b53ec6c4a629988f32ff6d0a65ed5cbb))
* map_create and map_update return warnings for a title kb_review ([fc6e1c2](https://github.com/BeppeTemp/cartographer/commit/fc6e1c2839cedc2deeb5762331fe01fa0b4bad1b))
* **mcp:** concept_batch and concept_new can add a curated index entry on creation ([#731](https://github.com/BeppeTemp/cartographer/issues/731)) ([0f7f1ab](https://github.com/BeppeTemp/cartographer/commit/0f7f1ab9c472444c1d75551da447d31828c3bad0))
* **mcp:** concept_new accepts list values for template variables ([#727](https://github.com/BeppeTemp/cartographer/issues/727)) ([98c4a94](https://github.com/BeppeTemp/cartographer/commit/98c4a94b644e1111763ed87bc176cae2020d2eda))
* **mcp:** record the calling agent client as an Agent commit trailer ([#735](https://github.com/BeppeTemp/cartographer/issues/735)) ([b32ecf9](https://github.com/BeppeTemp/cartographer/commit/b32ecf978be58db97486fdebfbff4816a95e51bb))
* **mcp:** tell open agent sessions when a KB they cannot see is mounted ([#734](https://github.com/BeppeTemp/cartographer/issues/734)) ([27dab01](https://github.com/BeppeTemp/cartographer/commit/27dab0190e819a1a0f65abf552d9922f5af723f6))
* **sync:** workspace bind writes a Cartographer MCP entry restricted to the workspace's KBs ([#728](https://github.com/BeppeTemp/cartographer/issues/728)) ([a8b734e](https://github.com/BeppeTemp/cartographer/commit/a8b734e0e69bb3608940b86f5e71e7aeec9d6a1c))
* the background repair runs at every server start, after a pull that moved HEAD and on its interval, instead of waiting for an interval after the last logged run: an upgrade that adds a check no longer leaves its fixes queued for a day. ([5f6e7cb](https://github.com/BeppeTemp/cartographer/commit/5f6e7cbc7fbf7a9ea23ecba2fc2acb9baf851e1f))
* **webui:** `liquid glass` in the search palette turns the Atlas to ([c06fa99](https://github.com/BeppeTemp/cartographer/commit/c06fa9979a4e1c379a5024ca5aa466042ad71968))
* **webui:** `tako mode` in the search palette dresses the Atlas as a ([c06fa99](https://github.com/BeppeTemp/cartographer/commit/c06fa9979a4e1c379a5024ca5aa466042ad71968))
* **webui:** a Secrets panel lists the KB's `secrets/*.sops.yaml` files ([1eaf66f](https://github.com/BeppeTemp/cartographer/commit/1eaf66f9df5c228b4961b444d5c9decbae4fec2d))
* **webui:** error states are designed cards with a retry, Activity and ([1eaf66f](https://github.com/BeppeTemp/cartographer/commit/1eaf66f9df5c228b4961b444d5c9decbae4fec2d))
* **webui:** first load shows the shell's skeleton instead of grey bars; ([1eaf66f](https://github.com/BeppeTemp/cartographer/commit/1eaf66f9df5c228b4961b444d5c9decbae4fec2d))
* **webui:** Secrets panel with in-browser sops decryption, and Atlas polish ([#708](https://github.com/BeppeTemp/cartographer/issues/708)) ([1eaf66f](https://github.com/BeppeTemp/cartographer/commit/1eaf66f9df5c228b4961b444d5c9decbae4fec2d))
* **webui:** the Atlas Health panel shows what Cartographer found and, on a second tab, what it checks. *Issues*: the kinds of issue found and every occurrence; a page field (one square per page, clustered by Map, amber for a problem, blue for a suggestion, split on the diagonal for both; hovering a row lights its pages in that row's colour; a Map's name filters the page); then *Problems* and *Suggestions*, one row per kind with a weight bar, opened to *What it is*, *To fix* and one line per case whose pages unfold on click. The suggestions include the `kb_review` proposals, read through a new read-only `GET /api/ui/v1/kbs/{kb}/maintenance/review`. *What it checks*: how checking works in three steps, the result on this KB, the trend, and every rule by folded category with what it looks for. The rail marks Health with a dot instead of a findings count. ([5f6e7cb](https://github.com/BeppeTemp/cartographer/commit/5f6e7cbc7fbf7a9ea23ecba2fc2acb9baf851e1f))
* **webui:** the brand in the top bar returns to the Atlas on *All*, ([1eaf66f](https://github.com/BeppeTemp/cartographer/commit/1eaf66f9df5c228b4961b444d5c9decbae4fec2d))
* **webui:** the graph keeps its picture across moves — coming back to ([1eaf66f](https://github.com/BeppeTemp/cartographer/commit/1eaf66f9df5c228b4961b444d5c9decbae4fec2d))
* **webui:** two easter eggs in the search palette ([#709](https://github.com/BeppeTemp/cartographer/issues/709)) ([c06fa99](https://github.com/BeppeTemp/cartographer/commit/c06fa9979a4e1c379a5024ca5aa466042ad71968))
* write responses warn early about titles, push state and doctor debt ([#721](https://github.com/BeppeTemp/cartographer/issues/721)) ([fc6e1c2](https://github.com/BeppeTemp/cartographer/commit/fc6e1c2839cedc2deeb5762331fe01fa0b4bad1b))


### Bug Fixes

* cartographer client bind asks the server for the mounted KBs before ([fc6e1c2](https://github.com/BeppeTemp/cartographer/commit/fc6e1c2839cedc2deeb5762331fe01fa0b4bad1b))
* doctor_suggested ignores info findings until the first doctor ([fc6e1c2](https://github.com/BeppeTemp/cartographer/commit/fc6e1c2839cedc2deeb5762331fe01fa0b4bad1b))
* index links, Sources and value-contract proposals behave on large imported KBs ([#720](https://github.com/BeppeTemp/cartographer/issues/720)) ([cb79e46](https://github.com/BeppeTemp/cartographer/commit/cb79e4654aa76c250be0dd6eb4348dcc967e6dbe))
* **lint:** unknown_type is info outside maps that adopted types ([#722](https://github.com/BeppeTemp/cartographer/issues/722)) ([75ec4df](https://github.com/BeppeTemp/cartographer/commit/75ec4df64904fafb0153ab16229f205154a40ce2))
* **sync:** write the newest OpenCode plugin shape when the version cannot be read ([#730](https://github.com/BeppeTemp/cartographer/issues/730)) ([1770f54](https://github.com/BeppeTemp/cartographer/commit/1770f5428a748c8b30e0af0aad0f87d4eb54826d))
* **webui:** a full-text search with no hit (`results: null`) no longer ([1eaf66f](https://github.com/BeppeTemp/cartographer/commit/1eaf66f9df5c228b4961b444d5c9decbae4fec2d))
* **webui:** the camera no longer stops and sets off again at the end of ([1eaf66f](https://github.com/BeppeTemp/cartographer/commit/1eaf66f9df5c228b4961b444d5c9decbae4fec2d))
* **webui:** the video export ends with an error instead of waiting ([1eaf66f](https://github.com/BeppeTemp/cartographer/commit/1eaf66f9df5c228b4961b444d5c9decbae4fec2d))

## [0.23.0](https://github.com/BeppeTemp/cartographer/compare/v0.22.2...v0.23.0) (2026-10-10)


### Features

* **cli:** connect schedules the headless doctor by default (D690) ([#693](https://github.com/BeppeTemp/cartographer/issues/693)) ([f373058](https://github.com/BeppeTemp/cartographer/commit/f373058356143c419f0cb171baf490809ab402cd))
* **webui:** growth video rendered offline at 60 fps, in the background (D691) ([#694](https://github.com/BeppeTemp/cartographer/issues/694)) ([071b27e](https://github.com/BeppeTemp/cartographer/commit/071b27eccaee7a6c55a4526894053fc62a465e0f))


### Bug Fixes

* **cli:** unattended sync and scheduled doctor read the client token from a private file (D699) ([#701](https://github.com/BeppeTemp/cartographer/issues/701)) ([3c7a400](https://github.com/BeppeTemp/cartographer/commit/3c7a400f26eaa3a7a29793e3ca344fd07c4f46a3))
* **webui:** the rail's Maps open the Atlas; Health filters by Map in the page ([#698](https://github.com/BeppeTemp/cartographer/issues/698)) ([79b7600](https://github.com/BeppeTemp/cartographer/commit/79b7600b7ded952d60035a514d93c537f757ae4a))

