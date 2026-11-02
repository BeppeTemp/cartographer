---
topic: client-configurator
---

# D1472 — `setup` plans create or mount from the remote's content

**Decision.** `setup --remote` chooses create or mount from what the remote holds, not only from whether `ls-remote` shows refs. With refs on exactly one branch, it fetches that branch two commits deep, without blobs, into a temporary directory and applies `gitx.ForgeInitialized`; a forge's initial commit is planned as create and the plan line says it is adopted ([D710](D710-kb-create-adopts-a-forge-initial-commit.md)). Any other remote with refs is mounted.

**Why.** `kb create --remote` adopts a forge's initial commit (D710) while `kb clone` refuses it, so a refs-only plan sent a project ticked with "initialize with a README" into a refusal that `kb create` would have handled. The classification stays the one rule D710 defines, applied in one place. It costs one extra network round trip, only when the remote has a single branch.

**Alternatives rejected.** Letting `kb clone` fall through to create: the plan, shown before anything changes, would name the wrong action. A new classification in setup: two rules for the same fact would drift. Fetching whole history: a KB can be large and the probe reads only commit count and top-level paths.

**Consequences.** The probe needs the remote's fetch to work, so a server that cannot serve the branch fails setup before anything changes, like an unreachable one. A server without partial-clone support ignores the filter and sends the blobs of two commits.
