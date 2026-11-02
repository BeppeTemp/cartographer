---
topic: client-configurator
---

# D1495 — The tray wears the brand mark and shows the agents and the KBs

**Decision.** The tray icon is the brand mark (Coordinate, D232), generated as a monochrome alpha mask as before. Below 32 px it uses the micro variant, without the inner arc. The state is still carried by shape:
- running is the mark;
- starting is the mark without its point;
- stopped is the mark crossed by a slash;
- not installed is the mark dimmed.

The menu is an information block followed by the actions. The block has:
- the state, at the right of "Cartographer";
- the version and the address, under it;
- an update row, when one exists;
- an Agents section, one row per connected client with its sync state at the right;
- a Knowledge Bases submenu whose rows open the Atlas on that KB.

On macOS the information rows are menu items with a view and coloured states. Windows shows the same rows as plain text.

**Why.** A generic pin did not say which program it was. A one-line status read as grey text, because AppKit draws a disabled item grey, and it said nothing about the two things a user checks: whether the agent clients are in sync, and which KBs are served.

The KBs are a submenu because the list has no bound: inline rows would make the menu grow with every KB, and a count in the header made it wide.

A view-backed row is neither greyed (disabled) nor highlighted on hover (enabled). The cost is hand layout in the purego code: the insets, a badge that keeps to the right edge, and the menu font's size read at build time.

The agent rows reuse the `cartographer status` snapshot, so the tray and the CLI cannot disagree. The cost is reading files and asking the server, so the tray refreshes them every 12 polls and when the server comes back, not on every tick.

**Alternatives rejected.**
- *A coloured brand mark in the menu, or coloured status dots and text glyphs (●, ✓, ⌂):* tried on the instance and rejected by the maintainer as noisy. Colour stays on the state words.
- *SF Symbols on the actions:* the Human Interface Guidelines keep menu icons for where they clarify, and macOS 27 hides symbol images in menus by default.
- *An authenticated `sync_status` per KB in the menu:* the tray would need a token, and today it reads only the unauthenticated `/health`.
- *Owner-drawn Windows menus to get colours:* a large amount of Win32 code for a cosmetic gain. The plain text carries the same information.

**Consequences.**
- `Snapshot` stays comparable: the KB names are one newline-joined string, and the agents are a fixed array of `MaxAgents` rows.
- A KB row's action is `ActionOpenKB + index` into the newest snapshot. If the KB list changes while the menu is open, the index can point to another KB. An index out of range is ignored.
- `OpenURL` accepts the Atlas URL with at most one `kb` query value, escaped by `KBURL`. Nothing else from `/health` reaches the system "open".
- Information rows have command id 0 on Windows, so clicking one dismisses the menu.
