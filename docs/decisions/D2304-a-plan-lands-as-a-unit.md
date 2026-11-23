---
topic: project-governance
amends: D1706, D1877, D1878, D2040
---

# D2304 — A chain of queued changes lands as one unit, judged by its tip

**Decision.** The lander lands by unit. A queued change's `Lander-After` trailer, or that of the commit a rejected change was queued as (`failed/<slug>^`), links it to the change it names when that one is queued or rejected; changes so linked are one unit, and a change with no link is a unit of one. A unit lands whole, in one train, or not at all: its members in their `Lander-After` order (ties by queue order), the units by their oldest member. A unit is blocked while a member is rejected, held while one carries `Lander-Hold: yes`, and frozen while the freeze is open and one is a `feat`, `perf`, `refactor` or breaking change; otherwise its verdict is the pre-check of its tips, the members no other member follows: red if one is red, else running if one runs, else green. `LAND_MAX` counts changes: a train takes whole green units in order, skipping one that does not fit, and a unit larger than `LAND_MAX` is gated alone; with no green unit, the oldest running unit is gated alone, else the oldest red one. A member rejected at its pick (a conflict, a decision) takes its unit out of the train and alone moves to `failed/`, its reason ending `(chain <first>..<tip>)`; the other members stay queued, blocked until it is queued again. A red train is bisected over the prefixes that end at a unit's boundary; of the first red unit, its last tip in queue order is rejected (`the train gate is red with the chain <first>..<tip> and green without it`), and the green prefix before it lands.

**Why.** A plan's chain is checked and reviewed once, on its tip (D2040), but the lander read every change's pre-check, took or left each change on its own verdict and bisected change by change. A defect fixed where the review asked, in the plan's last change, left the earlier changes red on their own: they waited or were gated alone, and a bisection could reject one in the middle or land a strict prefix of the plan, whose later changes then had to be rebuilt and queued again. Over two weeks of the queue 45 changes were rejected, 19 of them on red gates. The cost: no change of a chain lands before the whole chain is ready, and one rejected change holds the others.

**Alternatives rejected.**
- Keep landing by change and ask every change of a chain to be green on its own: it puts back the check and review per change that D2040 moved to the tip.
- Squash a chain into one commit when it is queued: each change stays its own conventional commit on `main`, with its own decision file and changelog entry (D1706).
- Move every member of the unit to `failed/` when one is rejected: the others would have to be queued again for a defect that is not theirs; the one rejected ref already blocks them, and queueing that change again unblocks them.

**Consequences.**
- No train lands a strict prefix of a unit, and after a rejection inside a unit the lander gates nothing of it until the rejected member is queued again: a queued change's `failed/` ref no longer counts (the lander deletes it once the change lands).
- The pre-check of a member that is not a tip is never read; it still runs on its push.
- A unit of one is a change as before: its pick, its verdict and its rejection sentences are unchanged.
- Amends D1706 (a train takes whole units, and a bisection rejects a unit's tip), D1877 (the verdict is the unit's tips', and a red unit is gated alone), D1878 (the prefixes of a bisection, probed or gated, end at a unit's boundary) and D2040 (a chain checked on its tip lands on that check).
