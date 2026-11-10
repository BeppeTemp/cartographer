---
topic: architecture
---

# D1884 — The client packages never link the data plane, and a test says so

**Decision.** The client packages (`internal/boundaries` lists them) import, at any depth, none of `kb`, `mcpserver`, `search`, `graphalgo`, `sqlindex` or `lint`. A transitive test (`go list -deps`) pins it, names the first chain that breaks it, and fails when a package the binary imports is in none of its lists. The one existing edge, `provisioning` reading `kb.ReservedMapNames`, is cut by moving `ServicesNamespace` and `ReservedMapNames` to `okf`, a leaf both sides already import.

**Why.** The four components are one binary, and the client side had silently grown a dependency on the data plane through a single constant. A rule that lives only in a sentence erodes; the Surveyor's `imports_test.go` showed a test keeps it. A file-level check is not enough: the edge that mattered was three hops deep.

**Alternatives rejected.** Build tags to compile a client-only binary: no gain worth the cost, D1439's "a server-only build is not worth the cost" stands, this is about the import direction, not the binary. A file-level import check like the Surveyor's: it misses transitive edges. Copying the constant into `provisioning`: the refusal text and the guide would drift.

**Consequences.** `service`, `tray` and `plugin/star` are exempt, each with its reason: `service` imports `config`, the server's own file, which imports `kb`; `tray` and `plugin/star` only reach it through `service.StateDir`/`service.Manager`. The exempt list only shrinks (a package that becomes clean fails the test until it is moved to the client list); they cannot be firewalled until `service.StateDir` moves out of `service`. A new package imported by `cmd/cartographer` must be classified on purpose. This supersedes nothing.
