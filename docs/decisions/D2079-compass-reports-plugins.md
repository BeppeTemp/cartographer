---
topic: control-plane
---

# D2079 — Compass reports its installed plugins

**Decision.** The client settings report (`clientconfig.SettingsReport`, D818) carries the plugins installed on that
Compass: name, version, state, digests, redacted source, the manifest description, a count of contributions per extension
point and the names (never the values) of the permissions the manifest declares. Meridian keeps them with the report and merges them into `GET /extensions`, next to its own pins, which carry the
same fields. The Atlas Plugins tab groups them by component (Meridian, Compass by machine, Surveyor), shows what each
plugin brings and opens the detail (full digests, source, permission names) on click.

**Why.** The Plugins tab (D1938) promised the plugins of every component, but only Meridian's own pins reached it: an
operator whose plugins are all client-side, the common case, saw "No plugin installed on any component". The report
already travels on every `sync`; one additive field costs less than a route of its own.

**Alternatives rejected.** A new component route for Compass plugins: a second report for data the settings report
already describes. Changing only the empty text: honest, but leaves the tab empty for the case it exists for.

**Consequences.** The field is additive: an older client omits it and its plugins stay unknown, never shown as "none".
The report carries no plugin content, settings, permission values or source URL beyond what `plugin list` prints redacted, so it
widens nothing a reader of the Settings page could not already learn on that machine.
