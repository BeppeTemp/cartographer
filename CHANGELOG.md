# Changelog

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
> (`docs/decisions.md` §D80).
