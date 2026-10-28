---
topic: client-configurator
---

# D1266 — Client subcommands take the positional anywhere among the flags; an extra one is a usage error

**Context.** `flag.Parse` stops at the first non-flag token, so `connect --kb x claude` dropped
`claude`, and an extra positional was silently ignored, connecting every provider (#1266).

**Decision.** `parseFlagsAnywhere` pulls positionals out wherever they stand and keeps parsing;
after a literal `--` everything left is positional. `bindPositionals` assigns them in order; a
positional with no destination is a usage error naming it, exit 2.

**Discarded alternatives.** Silently ignoring the extra positional: it connected every provider.
