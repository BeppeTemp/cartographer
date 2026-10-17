:robot: I have created a release *beep* *boop*
---


## [0.25.0](https://github.com/BeppeTemp/cartographer/compare/v0.24.0...v0.25.0) (2026-10-17)


### Features

* **client:** `cartographer sync` reports the client's own settings, ([2e90fb2](https://github.com/BeppeTemp/cartographer/commit/2e90fb2f0d3433cecd8c5a03601c23b80d862e36))
* **client:** sync writes the Claude MCP entry to every declared config dir ([#773](https://github.com/BeppeTemp/cartographer/issues/773)) ([60bd7af](https://github.com/BeppeTemp/cartographer/commit/60bd7af1172549d237c486a12fbb5ef99ea1a562))
* **cli:** kb unmount retires a KB from the local service ([#777](https://github.com/BeppeTemp/cartographer/issues/777)) ([59928f8](https://github.com/BeppeTemp/cartographer/commit/59928f84d89e72796d3d4cf52533b604eb3d8f4b))
* **config:** one settings view with origin, as cartographer config and an Atlas page ([#769](https://github.com/BeppeTemp/cartographer/issues/769)) ([81643ba](https://github.com/BeppeTemp/cartographer/commit/81643ba27e9f6dce741adbd22b5cd2ae7eb77358))
* **config:** settings schema and self-documenting config files ([#767](https://github.com/BeppeTemp/cartographer/issues/767)) ([5210b98](https://github.com/BeppeTemp/cartographer/commit/5210b98870a40dfe35ca759073c0b05e4f4b0af9))
* **demo:** a moderated guestbook KB on the live MCP demo, read by the public Atlas ([#801](https://github.com/BeppeTemp/cartographer/issues/801)) ([51adac1](https://github.com/BeppeTemp/cartographer/commit/51adac1ca863ab38907966cb8bf626c1a9b97d04))
* **deploy:** public read-only MCP endpoint for the demo KB on Cloud Run ([#752](https://github.com/BeppeTemp/cartographer/issues/752)) ([d371f9f](https://github.com/BeppeTemp/cartographer/commit/d371f9f73c792bb32683df3e03bdf5d679d2ee6e))
* **deploy:** the live MCP demo connects with the URL alone, no token ([#789](https://github.com/BeppeTemp/cartographer/issues/789)) ([c999109](https://github.com/BeppeTemp/cartographer/commit/c9991096ba7cad6a81be4d17e3522c1816797d9a))
* **dev:** defects that escape review become evals for the reviewers ([#871](https://github.com/BeppeTemp/cartographer/issues/871)) ([335c784](https://github.com/BeppeTemp/cartographer/commit/335c784ca0987cc8bcbe7297d5ad22115c1446c9))
* **dev:** full-tier PRs get a security-reviewer pass ([#869](https://github.com/BeppeTemp/cartographer/issues/869)) ([ecc85f8](https://github.com/BeppeTemp/cartographer/commit/ecc85f80483a9c0923d9824e477b5f68f4658938))
* **dev:** implement-issue and live-dev PRs are reviewed by pr-reviewer before the coordinator reads them ([#867](https://github.com/BeppeTemp/cartographer/issues/867)) ([108e011](https://github.com/BeppeTemp/cartographer/commit/108e011e6665d1946edbfa55abefa15212996d6e))
* **dev:** PRs shaped to be cheap to review - review-pr skill, PR template, size cap, approved plans ([#856](https://github.com/BeppeTemp/cartographer/issues/856)) ([e76deb3](https://github.com/BeppeTemp/cartographer/commit/e76deb312632d34c21b6c5ae50846e1d0a93b51b))
* **dev:** reviews converge in three rounds, supply-chain files always get a full review ([#857](https://github.com/BeppeTemp/cartographer/issues/857)) ([babfd90](https://github.com/BeppeTemp/cartographer/commit/babfd90843b6ea8f7870452c81ea324c6075ad80))
* **kb:** KB-carried policy settings in settings.yaml ([#774](https://github.com/BeppeTemp/cartographer/issues/774)) ([0c5f9b0](https://github.com/BeppeTemp/cartographer/commit/0c5f9b012b40e484cbe23dc49c2381f8bdf2b6b4))
* **lint:** kb_status inventories what each lint_ignore hides, since when, and reports stale entries ([#872](https://github.com/BeppeTemp/cartographer/issues/872)) ([90f16ea](https://github.com/BeppeTemp/cartographer/commit/90f16eacb00ea780e59d2a04aa1bbd0fe924b305))
* **mcp:** `map_list` returns the whole `_map.md` contract; the Atlas ([2e90fb2](https://github.com/BeppeTemp/cartographer/commit/2e90fb2f0d3433cecd8c5a03601c23b80d862e36))
* **mcp:** artifact_move moves an artifact to another KB ([#822](https://github.com/BeppeTemp/cartographer/issues/822)) ([f9166e1](https://github.com/BeppeTemp/cartographer/commit/f9166e14938b9aa236422d5656fe9454940b59d1))
* **mcp:** concept_transfer moves concepts with their assets to another KB ([#776](https://github.com/BeppeTemp/cartographer/issues/776)) ([c84662e](https://github.com/BeppeTemp/cartographer/commit/c84662ee7de3225794ba3782d175dbfc2e29589b))
* **mcp:** map_delete force removes loose non-concept files ([#770](https://github.com/BeppeTemp/cartographer/issues/770)) ([23e4c61](https://github.com/BeppeTemp/cartographer/commit/23e4c618fdf9c4812bf5d1e0db7819fa01decb0b))
* **mcp:** routed ?kbs= follows a renamed KB ([#819](https://github.com/BeppeTemp/cartographer/issues/819)) ([677d0ac](https://github.com/BeppeTemp/cartographer/commit/677d0ac469f5f657a51e9ec10da44e6daceabced))
* **mcp:** routed ?kbs= serves the mounted subset and reports unknown KBs ([#771](https://github.com/BeppeTemp/cartographer/issues/771)) ([1f57306](https://github.com/BeppeTemp/cartographer/commit/1f57306a75f62544eaa2868f1e56d358563b017d))
* **mcp:** search offers a related block from the link graph ([#823](https://github.com/BeppeTemp/cartographer/issues/823)) ([4b3ee21](https://github.com/BeppeTemp/cartographer/commit/4b3ee216a6a2a99789d57beaa4bc610387ed5480))
* **provisioning:** the generated instructions say when to use graph_context ([#780](https://github.com/BeppeTemp/cartographer/issues/780)) ([70988d2](https://github.com/BeppeTemp/cartographer/commit/70988d28f0b991f7052593ee09cc0e5da549cdaf))
* **search:** hits carry their size, and a scope that matches nothing says so ([#779](https://github.com/BeppeTemp/cartographer/issues/779)) ([9b1647c](https://github.com/BeppeTemp/cartographer/commit/9b1647c2f852d4444d570f75b06842ad2e5e2033))
* **skills:** kb-doctor 3.1 - tiered delegation, token economy, read-only audit scripts ([#844](https://github.com/BeppeTemp/cartographer/issues/844)) ([39f948d](https://github.com/BeppeTemp/cartographer/commit/39f948d2a3d0516544c077e8d735253f320b87d1))
* **web:** contradictions and git conflicts in Health, the journal and ([2e90fb2](https://github.com/BeppeTemp/cartographer/commit/2e90fb2f0d3433cecd8c5a03601c23b80d862e36))
* **web:** public static Atlas demo on an astronomy KB ([#750](https://github.com/BeppeTemp/cartographer/issues/750)) ([6acdc60](https://github.com/BeppeTemp/cartographer/commit/6acdc604ffe449af23a3cea964148a94c7b6c964))
* **web:** Settings shows the server, the selected Knowledge Base and ([2e90fb2](https://github.com/BeppeTemp/cartographer/commit/2e90fb2f0d3433cecd8c5a03601c23b80d862e36))
* **web:** the Atlas surfaces the server's features in six places ([#818](https://github.com/BeppeTemp/cartographer/issues/818)) ([2e90fb2](https://github.com/BeppeTemp/cartographer/commit/2e90fb2f0d3433cecd8c5a03601c23b80d862e36))
* **web:** the rail is Atlas, Activity, Work, Health, Artifacts and ([2e90fb2](https://github.com/BeppeTemp/cartographer/commit/2e90fb2f0d3433cecd8c5a03601c23b80d862e36))


### Bug Fixes

* **atlas:** switching KB draws the graph without waiting for the ([d8eed89](https://github.com/BeppeTemp/cartographer/commit/d8eed8988ced1d281fcc875e4f91885bd0444625))
* **atlas:** the 3D graph ticks its simulation at a fixed 60 Hz, so a 120–144 Hz display no longer doubles its CPU ([#736](https://github.com/BeppeTemp/cartographer/issues/736)) ([d8eed89](https://github.com/BeppeTemp/cartographer/commit/d8eed8988ced1d281fcc875e4f91885bd0444625))
* **demo:** guestbook messages hang off an about page, so none is an orphan ([#802](https://github.com/BeppeTemp/cartographer/issues/802)) ([1d409a6](https://github.com/BeppeTemp/cartographer/commit/1d409a6c2a6018468afd1f4b46d7affdb53f1b44))
* **deploy:** bootstrap the demo image with Cloud Build, not a local cross-build ([#753](https://github.com/BeppeTemp/cartographer/issues/753)) ([0c97644](https://github.com/BeppeTemp/cartographer/commit/0c9764408f13ab538dd9453f63306d9b4a8abd65))
* **gitx:** run git in the C locale, so outcomes are recognised under a translated git ([#811](https://github.com/BeppeTemp/cartographer/issues/811)) ([66a7b8e](https://github.com/BeppeTemp/cartographer/commit/66a7b8e268e0d316ec94ae4f501db222598b4ac5))
* **hook:** name the lint_ignore form that is honored in the write-findings hint ([#763](https://github.com/BeppeTemp/cartographer/issues/763)) ([6639b1b](https://github.com/BeppeTemp/cartographer/commit/6639b1bfc8f8e46ec258a7c939063ffbddf445f5))
* **import:** --commit no longer waits 30s on the KB lock it already holds ([#810](https://github.com/BeppeTemp/cartographer/issues/810)) ([aa42b1a](https://github.com/BeppeTemp/cartographer/commit/aa42b1a1d078d873d33deba0f4d5bd33e3476a59))
* **kb:** concept_expand leaves dangling asset links as written ([#766](https://github.com/BeppeTemp/cartographer/issues/766)) ([44007de](https://github.com/BeppeTemp/cartographer/commit/44007de2a45246febab325f5c90de863cb9703fc))
* **kb:** kb rename rewrites this machine's client bindings ([#765](https://github.com/BeppeTemp/cartographer/issues/765)) ([69f7c55](https://github.com/BeppeTemp/cartographer/commit/69f7c55cf4d01414b853501cf27fcaa3644058bb))
* **kb:** keep agent tool state out of the external-changes commit ([#764](https://github.com/BeppeTemp/cartographer/issues/764)) ([4a3362b](https://github.com/BeppeTemp/cartographer/commit/4a3362b32e9d4f744578a2ee3ed956460cb95b8e))
* **kb:** the KB's policy is one immutable snapshot, so a settings refresh never races a read ([#816](https://github.com/BeppeTemp/cartographer/issues/816)) ([828e481](https://github.com/BeppeTemp/cartographer/commit/828e48171e83a2ca914256de91b5e2239b06176f))
* **lint:** a map's lint_ignore dismisses kb_review kinds ([#847](https://github.com/BeppeTemp/cartographer/issues/847)) ([6585d89](https://github.com/BeppeTemp/cartographer/commit/6585d896f70d8699e07d3c3928bdc2b27cab6475))
* **lint:** a retired concept in a live map gets no index or orphan finding ([#852](https://github.com/BeppeTemp/cartographer/issues/852)) ([938cb6e](https://github.com/BeppeTemp/cartographer/commit/938cb6ec3a31aa98ed8f36716e74c0634dbb4290))
* **lint:** duplicate_link repair keeps a links heading the template requires ([#850](https://github.com/BeppeTemp/cartographer/issues/850)) ([4934ab2](https://github.com/BeppeTemp/cartographer/commit/4934ab26c536649f85c3b13713f11a12690927d0))
* **lint:** stale_claim waits until the day after review_after ([#846](https://github.com/BeppeTemp/cartographer/issues/846)) ([bbee406](https://github.com/BeppeTemp/cartographer/commit/bbee4065a373a9e167c0f8f36ba05193bb1cb31b))
* **lint:** the whole-KB lint runs its per-concept checks on every core, ([d8eed89](https://github.com/BeppeTemp/cartographer/commit/d8eed8988ced1d281fcc875e4f91885bd0444625))
* **mcp:** concept_move rebases links in an expanded concept's satellites ([#848](https://github.com/BeppeTemp/cartographer/issues/848)) ([b80df23](https://github.com/BeppeTemp/cartographer/commit/b80df237dbb33624a29192c2c12299c5ebd6d1a8))
* **mcp:** concept_new renders the template sample, defaults timestamp ([#849](https://github.com/BeppeTemp/cartographer/issues/849)) ([5657260](https://github.com/BeppeTemp/cartographer/commit/56572605ecc2ecf0bf003629b3faa40d25c5e46d))
* **mcp:** concurrent Atlas reads of a cold KB share one lint, and the ([d8eed89](https://github.com/BeppeTemp/cartographer/commit/d8eed8988ced1d281fcc875e4f91885bd0444625))
* **mcp:** reading or searching a KB no longer invalidates its whole-KB ([d8eed89](https://github.com/BeppeTemp/cartographer/commit/d8eed8988ced1d281fcc875e4f91885bd0444625))
* **mcpserver:** concept_move and concept_transfer authorize every satellite of an expanded concept ([#814](https://github.com/BeppeTemp/cartographer/issues/814)) ([8ef7038](https://github.com/BeppeTemp/cartographer/commit/8ef70386e4f4f6f7d3038aefbf9842b8f48f3c83))
* **mcp:** the write path applies a map's lint_ignore ([#851](https://github.com/BeppeTemp/cartographer/issues/851)) ([83cacf7](https://github.com/BeppeTemp/cartographer/commit/83cacf7ef2093c43f03a4bf816b027828e8586a2))
* **okf:** decode doubled apostrophes in single-quoted frontmatter ([#866](https://github.com/BeppeTemp/cartographer/issues/866)) ([3a8acd7](https://github.com/BeppeTemp/cartographer/commit/3a8acd74fa3673349c77b9765e6031fb77cc4f06))
* **search:** a search hit is no longer recorded as a read ([#778](https://github.com/BeppeTemp/cartographer/issues/778)) ([9a521db](https://github.com/BeppeTemp/cartographer/commit/9a521db6a3d3666c1cc913127392687c7fb61ba6))
* **web:** the Export video dialog keeps keyboard focus inside and ([2e90fb2](https://github.com/BeppeTemp/cartographer/commit/2e90fb2f0d3433cecd8c5a03601c23b80d862e36))
* **webui:** the Atlas formats every date in English, whatever the browser locale ([#805](https://github.com/BeppeTemp/cartographer/issues/805)) ([11a7431](https://github.com/BeppeTemp/cartographer/commit/11a7431941f9e1ba4a52f30a81e1be3e9cddeb60))

---
This PR was generated with [Release Please](https://github.com/googleapis/release-please). See [documentation](https://github.com/googleapis/release-please#release-please).
