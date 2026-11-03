---
topic: client-configurator
---

# D1514 — An unloadable client config is never replaced by defaults

**Decision.** `connect`, `disconnect` and the dashboard connect form start from the defaults only when `.cartographer.yaml` does not exist. Any other `Load` error stops the command before any write, naming the file and the error. `clientconfig.Save` refuses to replace an existing file it cannot parse. `doctor` and `status` print the real error when the file exists.

**Why.** The file holds what no default can rebuild: pinned signing keys, identities, MCP approvals, workspaces, paths. Treating every error as "first connect" turned a one-key typo into a silent wipe with exit code 0 and no backup. The cost is that a damaged file blocks `connect` until the operator fixes or removes it.

**Alternatives rejected.** Keep a `.bak` and carry on: the command would still run from defaults and drop the pins from the live file. Repair the file automatically: guessing which keys are intended is the same data loss in another form.

**Consequences.** Every new writer of the client config loads through `loadConfigForWrite` (cmd) or checks `errors.Is(err, os.ErrNotExist)`, never `err != nil`. The same rule applies to a reader that decides to write later.
