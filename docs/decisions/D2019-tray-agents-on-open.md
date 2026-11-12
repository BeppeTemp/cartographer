---
topic: client-configurator
---

# D2019 — The tray also reads the agents when the menu is about to open

**Decision.** Besides at start, every 5 minutes and when the server comes back (D1495), the tray reads the agent clients when the user is about to look at them: on macOS when the menu opens, on Windows when the pointer reaches the icon and on the click. The UI only reports "the menu is opening"; the poll loop decides. A read starts at once unless one began less than 10 seconds ago or is running. A request it cannot serve is remembered (one, however many follow) and its read starts when the 10-second window has ended and no read is running, at the next poll tick at the latest. A request that arrives while a read runs is not answered by that read, whose rows may predate what the user just did; one that arrives while a read waits behind a running one (a tick queued it) is answered by the waiting read, which begins after it, and is not remembered. A read that finishes is shown at once, and in a macOS menu that is open only when the Agents rows changed.

**Why.** The Agents rows are `cartographer status`'s answer, and that answer changes on a `sync`. With a read every 5 minutes, a user who ran `sync` and opened the menu saw "Out of sync" for up to 5 minutes while `status` said in sync (#1871). The menu is the moment the rows are looked at, so that is when they must be fresh.

The cost is real and is the one D1495 named, paid more often: a read reads the client config, the lockfile and the agents' files, makes authenticated manifest pulls to the client's server, and may refresh the repository-path cache. The debounce bounds what requests cost: a request starts at most one read every 10 seconds (the 5-minute and server-came-back reads do not wait for the window), so a user who hovers the icon all day costs one extra read per 10 seconds at worst, and one who looks at the menu a few times a day costs a few. The read still runs off the poll loop, so the icon and Quit never wait for it. A request inside the window is delayed to its end, not dropped: dropping it left the stale row exactly in the case #1871 describes (open the menu, run `sync`, reopen within the window).

**Alternatives rejected.**
- *A shorter fixed interval:* it pays the pulls all day for a view the user looks at a few times, and still leaves a stale window.
- *Reading on every menu opening, without a window:* a hover on Windows sends a message per pixel; each would be an authenticated pull.
- *Dropping a request inside the window:* the case above stays stale for 5 minutes.
- *Letting the running read answer a request that came after it began:* its rows may have been computed before the user's `sync`.
- *Refilling an open macOS menu on every new snapshot:* removing the items closes a KB submenu the user has open when a tick changes the state or the KB list. Only the Agents rows, the reason for the live refill, do it; other changes show at the next opening.

**Consequences.**
- `tray.Deps.AgentRequests` and `tray.ActionMenuOpened` are the contract between a UI and the poll loop; a new UI reports the pseudo action, never reads the agents itself.
- The 5-minute cadence, what a row says and the colours are unchanged.
- A debounced request is served at a tick: up to one poll interval (5 s) after the window ends.
- The macOS menu behaviour and the Windows mouse message are only vetted by compilation (`GOOS=darwin`, `GOOS=windows`), not by a test that drives a real menu.
