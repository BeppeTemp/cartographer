---
topic: architecture
---

# D1985 — The Surveyor shows in Settings, Activity and Work, each where its question is asked

**Decision.** The Surveyor tab in Settings is always present and carries a Sources block for the picked KB; Activity shows a "Surveyor runs" section only when there are runs; state and alarms stay in the Work panel's strip, which renders when there is an alarm even if no Surveyor is live. All of it reads the `components` read and the Source and feed routes that exist: no route is added.

**Why.** Settings is where a person looks for a Surveyor's configuration, and the KB's Sources are the KB's, not the Surveyor's, so they show whether or not one has reported. Runs are history, so they belong in Activity, but an empty section would be noise on every KB without a Surveyor. An alarm is something to act on, so it sits with the open work. Recency uses the server's `received_at`, never a time a Surveyor declared, and an alarm that names another KB is not shown to someone who sees this one.

**Alternatives rejected.**
- Hiding the Surveyor tab until one reports: the template of every key at its default would vanish with it.
- An always-present Activity section with an empty state: a header with nothing under it on most KBs.
- Alarms on Health: Health shows lint findings and nothing else (D948).
- Listing the Surveyor's recipes and connectors: its report carries scalars only.

**Consequences.** The Sources count stops at the server's list cap of 500 and says so. A read that fails (a 404 for a narrowed principal, an older server) leaves the page as it was, never an alarm.
