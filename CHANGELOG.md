# Changelog

### Migration note

KB histories with commits authored as `cartographer <cartographer@localhost>` may need a manual author rewrite before a forge with author push rules accepts the first push.

## [0.7.0](https://github.com/BeppeTemp/cartographer/compare/v0.6.1...v0.7.0) (2026-08-26)


### ⚠ BREAKING CHANGES

* **cli:** require a git remote for `kb create` ([#131](https://github.com/BeppeTemp/cartographer/issues/131))

### Features

* **cli:** require a git remote for `kb create` ([#131](https://github.com/BeppeTemp/cartographer/issues/131)) ([69f21cc](https://github.com/BeppeTemp/cartographer/commit/69f21ccc45744845cd0e7c6662043f020ace9aad))

## [0.6.1](https://github.com/BeppeTemp/cartographer/compare/v0.6.0...v0.6.1) (2026-08-17)


### Bug Fixes

* **mcpserver:** select the protocol era by header value, not presence ([#127](https://github.com/BeppeTemp/cartographer/issues/127)) ([d295c8e](https://github.com/BeppeTemp/cartographer/commit/d295c8e608db437c999ad7fa33c623bf4c29cffb))

## [0.6.0](https://github.com/BeppeTemp/cartographer/compare/v0.5.0...v0.6.0) (2026-08-17)


### Features

* **kb_status:** roll up concepts by status ([#124](https://github.com/BeppeTemp/cartographer/issues/124)) ([65057e9](https://github.com/BeppeTemp/cartographer/commit/65057e9a8784cc4c80fad43be4c37dbbf1759bfe))
* **mcp:** report protocol era and client identity of connected clients (D129) ([#123](https://github.com/BeppeTemp/cartographer/issues/123)) ([a59387a](https://github.com/BeppeTemp/cartographer/commit/a59387a24c66cbded66c93079af916cda8640abd))
* **mcp:** serve the 2026-07-28 revision alongside the handshake era (D128) ([#120](https://github.com/BeppeTemp/cartographer/issues/120)) ([d73e0bf](https://github.com/BeppeTemp/cartographer/commit/d73e0bf8f6f29057495bb70fe25e644a6580ddd7))


### Bug Fixes

* **mcpserver:** audit authorization denials and drop the unwired handler layer ([#125](https://github.com/BeppeTemp/cartographer/issues/125)) ([19f8b60](https://github.com/BeppeTemp/cartographer/commit/19f8b6048387414cd000958c0438e9686152b4c5))

## [0.5.0](https://github.com/BeppeTemp/cartographer/compare/v0.4.0...v0.5.0) (2026-08-04)


### Features

* **control-plane:** curate root and Map indexes safely through MCP ([#111](https://github.com/BeppeTemp/cartographer/issues/111)) ([3dcdfbe](https://github.com/BeppeTemp/cartographer/commit/3dcdfbe107631808f1c20ead238ab16038fae613))
* **control-plane:** expose read-only governance tools to descriptor-bound agents ([#112](https://github.com/BeppeTemp/cartographer/issues/112)) ([ddb4f88](https://github.com/BeppeTemp/cartographer/commit/ddb4f88b19335945fd03b48f3cab631b12fe6d0b))
* **data-plane:** add atomic multi-concept mutation batches via concept_batch (D125) ([#114](https://github.com/BeppeTemp/cartographer/issues/114)) ([b4227fe](https://github.com/BeppeTemp/cartographer/commit/b4227fe559bfdb7d0c44246c84f548623b1fb9b2))
* **data-plane:** distinguish client-local paths from operational paths in machine_path lint (D124) ([#113](https://github.com/BeppeTemp/cartographer/issues/113)) ([1cbfeee](https://github.com/BeppeTemp/cartographer/commit/1cbfeee4eadbee6b34cbceba57e0bf6388db9007))


### Bug Fixes

* **configurator:** preserve Codex-owned tables written inside a managed block ([#108](https://github.com/BeppeTemp/cartographer/issues/108)) ([f903daa](https://github.com/BeppeTemp/cartographer/commit/f903daab3df78b5134cf517d4f445b7ed875db3a))
* **provisioning:** adopt Codex hook registrations with inline commands ([#110](https://github.com/BeppeTemp/cartographer/issues/110)) ([d26f60a](https://github.com/BeppeTemp/cartographer/commit/d26f60aead594bc5881481f6f6e6823f3c0d8378))

## [0.4.0](https://github.com/BeppeTemp/cartographer/compare/v0.3.1...v0.4.0) (2026-07-31)


### Features

* add backlinks and frontmatter facets ([#80](https://github.com/BeppeTemp/cartographer/issues/80)) ([e5f1fd9](https://github.com/BeppeTemp/cartographer/commit/e5f1fd9412190ed0a2ba6a8fa8e31b6543a43e45)), closes [#71](https://github.com/BeppeTemp/cartographer/issues/71)
* add concept assets ([#78](https://github.com/BeppeTemp/cartographer/issues/78)) ([4061f47](https://github.com/BeppeTemp/cartographer/commit/4061f47d0a900e52616d45fe3574a0fbceb8df07))
* add declarative lint contracts ([#79](https://github.com/BeppeTemp/cartographer/issues/79)) ([88f3532](https://github.com/BeppeTemp/cartographer/commit/88f3532166f93736b93120377deaf5750529b876))
* add KB concept templates ([#81](https://github.com/BeppeTemp/cartographer/issues/81)) ([44e5511](https://github.com/BeppeTemp/cartographer/commit/44e5511ce8bfc3d22933f16eec8dd874fdc2e18b))
* **audit:** complete the operational audit and compliance controls ([#96](https://github.com/BeppeTemp/cartographer/issues/96)) ([472d816](https://github.com/BeppeTemp/cartographer/commit/472d81670db3364c8f12eb2c91a38c5fe30105f4))
* **auth:** add fine-grained RBAC and permission-aware retrieval ([#95](https://github.com/BeppeTemp/cartographer/issues/95)) ([ecc774c](https://github.com/BeppeTemp/cartographer/commit/ecc774c61b7dbad2441df4686bc54d7ee62e5429))
* gate MCP artifacts with hash-bound approvals ([#90](https://github.com/BeppeTemp/cartographer/issues/90)) ([7747341](https://github.com/BeppeTemp/cartographer/commit/774734177b9d24a8d7b4f58b11a77bf3c8889f42))
* **git:** add the server Git profile with working-branch pull requests ([#94](https://github.com/BeppeTemp/cartographer/issues/94)) ([ddc6c09](https://github.com/BeppeTemp/cartographer/commit/ddc6c0939d5f592598709a18402185e1e3ba0f4a))
* preserve executable and binary provisioning artifacts ([#77](https://github.com/BeppeTemp/cartographer/issues/77)) ([acdc751](https://github.com/BeppeTemp/cartographer/commit/acdc751717f656ca2859e549a362492be0f1f335))
* **service:** make native local upgrades restart and repair agents transparently ([#98](https://github.com/BeppeTemp/cartographer/issues/98)) ([d8eff03](https://github.com/BeppeTemp/cartographer/commit/d8eff03a40c0e48d138476222f09dfeb1129ab6c))
* support nested and writable SOPS secrets ([#76](https://github.com/BeppeTemp/cartographer/issues/76)) ([fc7d1dc](https://github.com/BeppeTemp/cartographer/commit/fc7d1dcad81cc7c25c6f5c14b1142470df0fdc86))
* **sync:** provision stdio MCP servers and environment references ([#93](https://github.com/BeppeTemp/cartographer/issues/93)) ([35f9acb](https://github.com/BeppeTemp/cartographer/commit/35f9acb0e84c8ec119944d120c971a0a337aacae))
* unify CLI and TUI user experience ([#86](https://github.com/BeppeTemp/cartographer/issues/86)) ([a999b99](https://github.com/BeppeTemp/cartographer/commit/a999b99f8850e06ba07375353d783329720abd74))
* verify provisioning artifacts cryptographically ([#89](https://github.com/BeppeTemp/cartographer/issues/89)) ([f498155](https://github.com/BeppeTemp/cartographer/commit/f4981550d8febfb17c0e1ae343dab566148222f3))


### Bug Fixes

* **client:** discover the per-KB tool prefix instead of deriving it ([#97](https://github.com/BeppeTemp/cartographer/issues/97)) ([72726aa](https://github.com/BeppeTemp/cartographer/commit/72726aa9ea40ef2736df740bba893d59a0046797))
* **client:** propagate MCP trust states to status and TUI output ([#92](https://github.com/BeppeTemp/cartographer/issues/92)) ([61994c4](https://github.com/BeppeTemp/cartographer/commit/61994c4c5a0fcbe0291bdac70dd983af5c9db197))
* move local defaults off port 8080 ([#85](https://github.com/BeppeTemp/cartographer/issues/85)) ([cc967f5](https://github.com/BeppeTemp/cartographer/commit/cc967f5f08839f5f29f6e03d15e72f9fb710ac15)), closes [#82](https://github.com/BeppeTemp/cartographer/issues/82)
* surface git push failures and resolve commit identity ([#74](https://github.com/BeppeTemp/cartographer/issues/74)) ([466201e](https://github.com/BeppeTemp/cartographer/commit/466201e8a1576435f4ebe2f35357b700f05173bd))

## [0.3.1](https://github.com/BeppeTemp/cartographer/compare/v0.3.0...v0.3.1) (2026-07-27)


### Bug Fixes

* **mcp:** opt-in per-KB tool-name prefix for flat-namespace clients ([#63](https://github.com/BeppeTemp/cartographer/issues/63)) ([ffffb21](https://github.com/BeppeTemp/cartographer/commit/ffffb21c547e927592ecd10fcbaf8d9b6a1951e4))

## [0.3.0](https://github.com/BeppeTemp/cartographer/compare/v0.2.0...v0.3.0) (2026-07-26)


### Features

* **docs:** publish docs/ to GitHub Pages with MkDocs Material ([#58](https://github.com/BeppeTemp/cartographer/issues/58)) ([fa9fc11](https://github.com/BeppeTemp/cartographer/commit/fa9fc1160630ac05d574b1975124bb729514be81))


### Bug Fixes

* **configurator:** adopt orphaned tables in Codex config.toml ([#61](https://github.com/BeppeTemp/cartographer/issues/61)) ([28677c3](https://github.com/BeppeTemp/cartographer/commit/28677c3f7c9e22457c67aa697aeb4266b261ef96)), closes [#50](https://github.com/BeppeTemp/cartographer/issues/50)

## [0.2.0](https://github.com/BeppeTemp/cartographer/compare/v0.1.2...v0.2.0) (2026-07-24)


### Features

* **client:** surface server and client versions with upgrade drift hint ([#49](https://github.com/BeppeTemp/cartographer/issues/49)) ([ba338f3](https://github.com/BeppeTemp/cartographer/commit/ba338f3845984a89fc91d3e6a4fe142ade6b03eb)), closes [#34](https://github.com/BeppeTemp/cartographer/issues/34)
* **cli:** kb create and first-KB guidance ([#39](https://github.com/BeppeTemp/cartographer/issues/39)) ([cdfa2c1](https://github.com/BeppeTemp/cartographer/commit/cdfa2c113a6b31a3e22fc612d38fa7cb2a1b486e))
* **connect:** agent subset selection, 0-KB diagnostics, absolute paths ([#41](https://github.com/BeppeTemp/cartographer/issues/41)) ([89cc4bc](https://github.com/BeppeTemp/cartographer/commit/89cc4bccf904642527474f29a172deb8e4107a32)), closes [#18](https://github.com/BeppeTemp/cartographer/issues/18)
* **connect:** per-KB MCP entries for multi-KB servers ([#44](https://github.com/BeppeTemp/cartographer/issues/44)) ([92d18f5](https://github.com/BeppeTemp/cartographer/commit/92d18f53bb2272d602416bef126fcfdcb336d7ec)), closes [#25](https://github.com/BeppeTemp/cartographer/issues/25)
* **http:** readiness signal and per-KB path routing ([#30](https://github.com/BeppeTemp/cartographer/issues/30)) ([926617c](https://github.com/BeppeTemp/cartographer/commit/926617c055447a1bfc7096106fb1ed75157bcdcf))
* **import:** commit flag, map scaffold, dir-as-concept ([#45](https://github.com/BeppeTemp/cartographer/issues/45)) ([0b05aee](https://github.com/BeppeTemp/cartographer/commit/0b05aeeac513479f2e4136bd62cfc158266a73d6)), closes [#23](https://github.com/BeppeTemp/cartographer/issues/23)
* **index:** content-hash reconciliation and reindex tool ([#43](https://github.com/BeppeTemp/cartographer/issues/43)) ([9163c06](https://github.com/BeppeTemp/cartographer/commit/9163c068f1307d3cfb6c500e2d1b1098bc02a55d)), closes [#22](https://github.com/BeppeTemp/cartographer/issues/22)
* **mcp:** changes_since digest tool ([#48](https://github.com/BeppeTemp/cartographer/issues/48)) ([9736ede](https://github.com/BeppeTemp/cartographer/commit/9736ede3641725fbca71f3a0f6f0f8dc27e23d3f)), closes [#27](https://github.com/BeppeTemp/cartographer/issues/27)
* **mcp:** frontmatter unset in concept_patch, map_delete tool ([#38](https://github.com/BeppeTemp/cartographer/issues/38)) ([bece5c7](https://github.com/BeppeTemp/cartographer/commit/bece5c76a27d99a7e9b745b4f03a00479e6e5624))
* **onboarding:** agent-driven install — kb clone, runbook, prompt template ([#46](https://github.com/BeppeTemp/cartographer/issues/46)) ([7449232](https://github.com/BeppeTemp/cartographer/commit/744923206654a99a788dd29c1b9bfa15c5203c09)), closes [#36](https://github.com/BeppeTemp/cartographer/issues/36)
* **skills:** bundled cartographer-ops skill ([#42](https://github.com/BeppeTemp/cartographer/issues/42)) ([b37604a](https://github.com/BeppeTemp/cartographer/commit/b37604ad5e440d6b7256c8297ac16dbcd847caa1)), closes [#35](https://github.com/BeppeTemp/cartographer/issues/35)


### Bug Fixes

* **okf:** ignore headings inside code fences ([#28](https://github.com/BeppeTemp/cartographer/issues/28)) ([a9a809a](https://github.com/BeppeTemp/cartographer/commit/a9a809a57863146696359822643843fcb316d4d5))
* **search:** multi-term FTS matching and mode schema coherence ([#40](https://github.com/BeppeTemp/cartographer/issues/40)) ([1781656](https://github.com/BeppeTemp/cartographer/commit/178165610d5de9f7af8b0cc2f0dd348bd8fba845))
* **service:** create data dir on install, tolerate missing data dir, stable plist path ([#29](https://github.com/BeppeTemp/cartographer/issues/29)) ([66311f6](https://github.com/BeppeTemp/cartographer/commit/66311f65d7929a03f8cf32923611d15e11906533))
* **sync:** pull remote changes on the read path ([#47](https://github.com/BeppeTemp/cartographer/issues/47)) ([33e67a0](https://github.com/BeppeTemp/cartographer/commit/33e67a03931490cae938fa77d31eaca0cc4df181)), closes [#26](https://github.com/BeppeTemp/cartographer/issues/26)

## [0.1.2](https://github.com/BeppeTemp/cartographer/compare/v0.1.1...v0.1.2) (2026-07-18)


### Bug Fixes

* match MCP registry server name casing to the GitHub account ([81183e4](https://github.com/BeppeTemp/cartographer/commit/81183e454b9dcf6755fc9ae4b22b37cba4efe0a4))

## [0.1.1](https://github.com/BeppeTemp/cartographer/compare/v0.1.0...v0.1.1) (2026-07-18)


### Bug Fixes

* shorten MCP registry description to the 100-char limit ([59dcc5c](https://github.com/BeppeTemp/cartographer/commit/59dcc5c9e7ce1a9db73ddc9f6a3037469757a922))

## 0.1.0 (2026-07-18)


### Features

* initial public release ([9c0fb45](https://github.com/BeppeTemp/cartographer/commit/9c0fb45fa884c2b371308348a05afe533f5fb64a))
* publish to the MCP registry on release ([#5](https://github.com/BeppeTemp/cartographer/issues/5)) ([f4ecb19](https://github.com/BeppeTemp/cartographer/commit/f4ecb199e4c7f9c11354f3224940469476058151))


### Bug Fixes

* pin release-please initial version to 0.1.0 ([491e60a](https://github.com/BeppeTemp/cartographer/commit/491e60ae86c622e9282626de18b3e34e62db64e4))

> Public versioning starts at v0.1.0. Earlier version numbers (the v2.x line)
> belonged to the internal pre-open-source development history and were retired
> when versioning was reset to reflect the project's beta status
> (`docs/decisions/project-governance.md` §D80).
