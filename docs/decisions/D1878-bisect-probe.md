---
topic: project-governance
---

# D1878 — A red train is bisected with the tests the signoff confirmed, then its green prefix gets one full gate

**Decision.** When the signoff's targeted retry confirms a red run on named Go tests (D1755), it prints a `signoff: probe: <platform> <packages> <race packages>` line, and `scripts/signoff.sh --probe` runs exactly that retry command on any tree. The lander's bisection runs the probe on each prefix instead of a full signoff, rejects the first change whose prefix is red, then gates the prefix before it once in full: green, it lands; red, the train is composed again. A failure the retry did not confirm on named tests (a build, a vet, a smoke, e2e or web failure), or a probe whose machine is unavailable, keeps the bisection with a full signoff per prefix.

**Why.** On 2026-11-09 three red trains of six to eight changes spent 45–53 minutes each in bisection, three full signoffs of about 13 minutes on the build host, while the confirmed failure was one Go test whose packages run in about 70 seconds. With the probe the same bisection is two or three probes and one full gate, about 12 minutes, and the full gate still proves the tree that lands.

**Alternatives rejected.**
- Land the green prefix on the probe alone: the probe runs one package, not the gate; a prefix the probe calls green may break something else.
- Bisect on hosted runners in parallel: every prefix at once is one gate's wall-clock time, but up to seven full gates against the account's 20 concurrent jobs, and the queued changes now get their risky legs pre-checked there already (D1877).
- Probe web failures too: vitest and Playwright names do not map to a command the signoff can rerun alone today; the pre-check runs the frontend unit tests before a change joins a train.

**Consequences.** A train red on a confirmed test costs at most one extra full gate besides the probes. The probe line is part of the signoff's output contract with the lander: `internal/repodocs/land_test.go` pins it through a fake `make`.
