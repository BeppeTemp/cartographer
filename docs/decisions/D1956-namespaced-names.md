---
topic: architecture
---

# D1956 — recipes and connectors from a plugin are named `<plugin>/<name>`

**Decision.** A recipe or connector a plugin ships is named `<plugin>/<name>` (each part starts and ends with a letter or digit, no `-` at an edge, so the alias has one reading; the whole name is at most 31 characters, so its alias is at most 32 and leaves 30 characters of the 64-character tool-name cap providers enforce for the tool, where a 30-character operator-local name leaves 32);
the unprefixed namespace belongs to the operator's config and the built-ins, and a `/` in a name written under
`recipes:` or `connectors:` is a load error. A Source stores the qualified name as an ordinary string, so the recipe
registry, the connector set and every site that compares a Source's name work on the whole string unchanged:
`recipe.Options.Plugin` registers already-built plugin recipes under their qualified name, and an unknown plugin or
name is `recipe_unknown` (poll) or a skipped unit (connector), as for any unknown name. The server judges neither
(`source_register` only describes the form), so a KB still cannot select code: a name resolves only if the
Surveyor's own config pinned the plugin.

**The `--` alias.** The model is offered a connector's tools as `<alias>__<tool>`, where the alias is the connector
name with `/` written `--`; the model never sees a `/`, and routing still splits at the first `__`, which no name
contains. The alias is not injective: an operator-local `a--b` and a plugin's `a/b` offer the same prefix. The
alternative, forbidding `--` in operator names, would break names that work today; so `connector.CheckAliases` refuses
two connectors with one alias at config load, and `connector.Open` refuses to start any connector of a set that has one, so no call with credentials reaches the wrong server. A tool whose offered name would exceed 64 characters is logged and not offered.

**Consequences.** No unprefixed name changes, with one exception: a tool whose offered name `<alias>__<tool>` would exceed 64 characters is logged and not offered for every connector, operator-local ones included (a 30-character local name with a tool of more than 32 characters was offered before and no longer is). The plugin loader that produces qualified names is a
later change; until it lands only tests build them.
