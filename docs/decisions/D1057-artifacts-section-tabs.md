---
topic: sync-provisioning
---

# D1057 — The Artifacts section is four tabs: agent kit, instructions, templates, secrets

**Decision.** The Atlas's Artifacts section opens on four tabs, each its own
panel in the URL: **Agent kit** (`panel=artifacts`: skills, subagents, hooks,
MCP servers, as tiles with a detail beside them), **Instructions**
(`panel=instructions`: the instructions file shown as its source, in the frame
Settings shows its YAML in), **Templates** (`panel=templates`: the template
tiles) and **Secrets**. An artifact link opens the tab of its kind. The catalog
has no text filter.

**Why.** A KB has one set of instructions and a handful of templates, but they
were two more groups of tiles among the skills: the instructions, the one text
every client receives as it is, had to be opened like a skill to be read, and
the templates read as something agents run. The kinds differ in what a reader
does with them — the kit is browsed and opened, the instructions are read
whole, the templates are a reference — so each gets the page that fits. The
filter box searched a list that the Kind chips already narrow to a few dozen
tiles at most, and it was never used.

**Alternatives rejected.**
- *Instructions rendered as Markdown, at reading measure.* Tried live: a long
  instructions file full of tables read badly at the concept page's measure and
  worse when widened; it is a file the clients receive verbatim, so its source
  is what the reader needs to see.
- *Keep one catalog and collapse the instructions and templates groups.* Still
  one tile to open for the one text that matters most, and templates still
  among the agent's tools.
- *Rename the rail entry too.* "Artifacts" is the word the docs, the MCP tools
  and D864 use for all of them; the tabs name the parts.

**Consequences.** `artifactPanel(kind)` (web `lib/viewstate`) is where a new
kind chooses its tab; a kind nobody routes there lands in the agent kit. The
D864 list of kinds still drives what each tab shows, filtered by the tab's
kinds. An old `panel=artifacts&artifact=template/<name>` link is read as the
Templates tab with that template open, and one to the instructions as the
Instructions tab.
