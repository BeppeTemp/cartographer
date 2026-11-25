---
topic: control-plane
---

# D2377 — `dashboard_list` and `dashboard_get` are advanced collection reads, and a missing dashboard is an empty one

**Decision.** `dashboard_list` and `dashboard_get` are read-only, advanced tools of the collection resource class. `dashboard_get` returns the dashboard resolved for the caller; a name with no file answers an empty dashboard with hash `""` instead of an error, and an invalid name is an error. A caller that does not read the whole KB (`WholeVisible` false) gets only what the resolver computed for it plus structural keys (name, widget id, type, size, look from closed lists): every author-typed string and every filter or grouping definition is omitted, not blanked, and `dashboard_list` gives it names only.

**Why.** An agent builds a dashboard from nothing with a patch guarded by the hash it read: an empty answer with hash `""` is that first read, with no special case. Entries, groups and counts already come from sources narrowed to what the caller reads (D226), so the tools need no whole-KB grant; titles, notes and filters are free text of the whole KB, not of a collection, and a filter can name a map the caller cannot see, so a narrowed caller reads none of them. It costs a narrowed caller the dashboard's own labels.

**Alternatives rejected.** Whole-KB class: it would refuse every narrowed token a dashboard whose widgets resolve to what that token reads. A not-found error for a missing name: the agent would need a second path to start a dashboard. Core tools: the agent profile has no fixed-cost headroom for them.

**Consequences.** The write tools join `advancedToolNames` with their own change, because a registry test refuses a name no tool carries. A new field of a widget is omitted for a narrowed caller until `narrowDashboard` lists it as structural.
