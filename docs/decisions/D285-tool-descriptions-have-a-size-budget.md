---
topic: control-plane
---

# D285 — Tool descriptions have a size budget, enforced by a test

**Amends D65.**

**Decision.** The `agent`-profile `tools/list` of one unprefixed mount must stay within 22 KiB
serialized, and no tool `description` may exceed 600 characters. `TestServer_ToolDescriptionBudget`
fails the build otherwise and prints the five heaviest tools. Only `description` strings are
shortened: tool names, property names, types, `required` and enums do not change.

**Why.** D65 fixed the `agent` profile at 17 tools (about 1.9k tokens of schemas); it has grown
past 35 and the listing had reached about 41 KB, of which descriptions and property descriptions
were most. Every client pays that text on every round-trip, in both mount modes, so routing (D187,
D288) removes the duplication but not the weight of the single copy. A budget in a test is the only
form that survives the next tool: prose in a convention is read once, a failing build is read every
time.

**Alternatives rejected.**
- *Move more tools to `advancedToolNames`:* a descriptor-bound host cannot call a tool `tools/list`
  does not advertise (D123), so hiding a tool removes it for that host.
- *Trim only the tool descriptions, leave the schemas:* property descriptions were as heavy as the
  tool descriptions, and the fixed structure is already about half of the budget.
- *A convention without a test:* it decays one helpful sentence at a time.

**Consequences.** A description keeps what the tool does, when to use it instead of its neighbour
and every rule needed to call it correctly; rationale, `D<n>` references and response-field listings
go to `docs/control-plane.md` §Tool descriptions and their size budget, which stays the full
reference. A new tool that breaks the budget must be written shorter, or the budget raised in a
decision of its own. Enforced limits (D161) stay interpolated from their constants. The budget is
tight (a few hundred bytes of headroom): adding a tool means shortening a neighbour.
