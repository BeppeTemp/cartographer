---
topic: architecture
---

# D1976 — Installed plugins are a read-only Settings tab

**Decision.** The Atlas lists the installed plugins and their approval state per component (Meridian, Compass, Surveyor) in a Plugins tab of Settings, always present and read-only: approval stays a command on the component.

**Why.** Plugins are configuration of the platform, and Settings already holds one tab per component, with the Surveyor tab as the precedent for a tab that exists before anything has reported. A button here would make the browser, which holds a bearer token, a place where code is consented to; the digest is the only consent and it is given where the plugin runs. The route returns the list in full only to a principal that sees the whole server, so for a narrowed token an empty list reads "Visible to the operator only", not "no plugin installed".

**Alternatives rejected.**
- A rail panel: the rail has no free slot of meaning.
- An approve button: it moves consent into the browser.

**Consequences.** The tab stays a view of `plugins` in `GET /api/ui/v1/extensions`; a state the Atlas does not know is shown as it came. A plugin's apps are shown as text in the row's sheet, never as links, so the operator reads where an app goes before opening it from the rail.
