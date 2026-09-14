---
topic: deployment-release
---

# D95 — Upgrade transparency through version-skew hints

**Status: implemented (2026-07-24); partially superseded by [D121](D121-native-local-upgrades-repair-themselves.md)** —
for native local upgrades the hint now names `cartographer upgrade-repair`, and
the "never restart from a cask hook" reasoning below no longer holds. Skew
reporting for remote servers and Kubernetes is unchanged.

**Decision.** `cartographer status` reports the client and server versions before its provisioning-artifact result. A non-`dev` version mismatch is advisory and leaves the existing status exit codes unchanged; for a loopback server with an installed native service, the warning includes the explicit `cartographer service restart` command. Servers that predate the health version field remain compatible and simply produce no skew warning.

**Rationale.** A Homebrew upgrade replaces the binary at the stable path, but cannot safely replace a process already executing it. Automatically restarting from a cask hook could interrupt an in-flight write and bypass the operator's drain decision, while an explicit, contextual hint makes the necessary restart visible. The same report makes a client ahead of a Kubernetes image rollout observable without inventing a separate drift state.
