---
topic: deployment-release
---

# D1439 — Four deployment components, one binary, and a scenario matrix as the map

**Decision.** Cartographer is documented as four **deployment components**: Server, Surveyor, Atlas and Client. They are deployment units, not four binaries: every native channel keeps one binary per platform and the components are its subcommands (D37 and D227 stand). The Server is the only stateful component, with one writer per KB. The Surveyor has no persistent volume and no inbound port. The Atlas is embedded in the Server, its `/ui/` and `/api/ui/v1` belong to the Server, and it is never deployed alone. The Client is not a workload. The same KB on two servers is synchronization, never scaling: no replicas, no load balancer, no failover. `docs/deployment.md` §Deployment scenarios is the map of every way of deploying, each row `documented` or `assembled by hand`; every other page cites a scenario by name, never by number.

**Why.** Measured on `main`: `internal/mcpserver` imports `internal/client`, `internal/configurator` or `internal/provisioning` in ten non-test files, so a server-only binary would still carry the client code; the stripped binary is about 24 MB, so splitting saves nothing worth the cost; `.goreleaser.yaml`, `install.sh`, `install.ps1`, the Cask and the goreleaser guard assume one asset per platform; and a local install of several binaries would skew versions on one machine. The documentation had no place that said so, and two statements read as contradictory (D73 lets several servers mount one KB, `docs/deployment.md` forbade several writers as replicas): both stay true under the rule above.

**Alternatives rejected.**
- Four binaries: reverses D37 and D227, `go install` loses the Atlas (it cannot run npm, the reason D227 exists), the mcpserver/client/provisioning coupling remains, five release paths assume one asset, nothing is saved, and local installs skew.
- A standalone Atlas deployment and a `web.mode` key: the only gain is UI updates without a server restart; single sign-on already works through path routing (D308); the same-origin rules and the CSP would have to change.
- A `--role` flag on `cartographer service`: the Surveyor's unit is written by hand.
- A client image: the binary is copied into the agent's image or installed.
- Compose as a local mode: D73 keeps containers out of the local deploy; compose is a shared-VM recipe.
- Replicas of one KB: git conflict recovery is a safety net, not a write-scaling protocol.

**Consequences.** A new way of deploying is a new row of the matrix, with its status, and a row without a section of its own is `assembled by hand` and links the issue that will document it. The Surveyor image and the Helm chart are built against these components. A change to the image's entrypoint or non-root user updates the recipes that assume them (`docs/index.md` §Documentation maintenance rules).
