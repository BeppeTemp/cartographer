---
topic: control-plane
---

# D947 — The lint has no severity: every finding is a problem

**Decision.** A lint finding is a problem. It carries no severity, and nothing a reader receives names one: not the
MCP responses (`lint`, `gate_check`, `kb_status`, the write responses, the write gate's refusal), not the HTTP UI API,
not the Atlas. `severity_min`, `counts_by_severity`, `by_severity`, `findings_omitted` (what a floor hid) and
`finding_severities` are gone. `gate_check.pass` needs zero findings in the scope it checks (the changed concepts, a
scope, or the whole KB), no validation error and a passing commit gate. Findings are listed in the registry's category
order, then by check, path and message (D324): an order to start from, never a judgement of gravity.

A check stays in the lint only if it has a **definite fix**: a state of the KB that makes the finding disappear and is
always better than the current one. A state a reasonable maintainer could look at and decide "this is fine as it is"
is an **observation**: it leaves the lint and becomes a `kb_review` item of kind `observation` (the doctor session,
where a person decides), or it is deleted when it is not worth even that. `lint.ObservationSpec` lists them; no lint
entry point, write response, gate or background repair returns one, and a test fails if one is emitted as a finding.

- **Observations** (a `kb_review` item of kind `observation`, each with a page state that closes it, D907):
  `cut_concept`, `island`, `map_misfit`, `concept_oversize`, `map_oversize`, `facet_sprawl`, `missing_value_contract`.
- **Deleted**, because something else already says it: `map_without_templates` (the `template_proposal` kind proposes
  the templates a map's pages already follow), `artifact_unused` (a usage signal ages with no page to fix; the Artifacts
  panel and `kb_status.usage` already show when an artifact was last used), and the `unknown_type` finding in a map that
  has not adopted types (D688 made it `info`; `template_proposal` proposes the template).
- **Everything else stays**, including the seed list's `title_quality`: it has an unattended fix for the decorative
  characters, and its other cases (a title over 100 characters, a lifecycle word, a date-prefixed slug) each have a
  state that is always better and that the write gate already refuses.

`MapOversizeCrossings` (D716), the write-time report of a map crossing 50 pages, goes with its check: a write is not told
of an observation. `kb_review`'s `Weight` for a lint judgement is the check's category order, not a severity rank.

**The Atlas.** Health shows only lint problems: one list, no *Problems*/*Suggestions* split, a page field whose legend
reads "has problems" / "fine". The `kb_review` items (templates to adopt, statuses to correct, facts copied into many
pages, work scattered in checklists, …) and the observations are decisions, not problems, so they leave Health and
belong to the Work page as items "to decide". That Work mechanism (`work_list` gaining a reason) is built on
`feat/live-atlas`, not on `main`: this change removes the review items from Health and builds nothing on the Work side,
and the coordinator wires them there on that branch.

**Why.** The lint cannot be escaped (D907): a finding is debt, and the job is to keep a KB in the best state it can be
in. Three severities told the reader that some findings can wait, and the code acted on it: a KB with hundreds of
warnings passed the session-end gate, `severity_min` existed to hide findings, the doctor nudge counted warnings only,
and the Health page filed a third of the list under "Suggestions". The write gate already ignored severity (it refuses
every avoidable finding): this generalizes it. Making the old `info` checks problems unconditionally would have kept
a KB from ever reaching zero, because some of them (a hub the graph hangs on, an oversized map) are observations with no
single right answer; hence the definite-fix criterion.

**Alternatives rejected.**
- Keep the labels but make the gate fail on all of them: the label would still say that some findings can wait.
- Make every old `info` check a problem: a KB with a legitimate 40 KB reference page or a 60-page map could never reach
  zero, and the gate would train people to split pages to satisfy a number.
- Keep the observations in `lint` behind a flag: a switch is an escape (D907).
- Delete the observations: `concept_oversize`, `map_oversize`, `island`, `cut_concept`, `map_misfit` and the two contract
  proposals each have a state a person can write, and the doctor is where that decision belongs.
- A separate `observation` kind per check: seven kinds for one idea; one kind with the check in `check` keeps the list and
  the closing-state trap test small.
- Drop `severity` from `cartographer doctor` too: its `error`/`warning`/`info` levels drive its exit code and a monitoring
  contract (`cartographer.doctor/v1`), and its findings diagnose a client's setup, not a KB (see Consequences).

**Consequences.**
- Breaking: `gate_check` fails on any finding; a KB with old debt fails the whole-KB gate until repaired, as intended.
  `conformance.findings` in `kb_status` is a number (the conformance checks' findings), no longer a map by severity;
  the doctor is suggested for any finding or review item once the interval has passed, `info` included.
- `lint` and the Atlas lint endpoint list every finding; an Atlas that sent `severity_min` gets all of them. The lint
  history samples keep `total` and `by_handler`.
- A new check is a problem or an observation on purpose: `docs/data-plane.md` §Observations and `CONTRIBUTING.md`
  §Adding a lint check say how, and `TestEveryCheckIsLintOrObservation` pins the split.
- The `skill.Validate` levels (`error`/`warning`) and the `cartographer doctor` levels are not lint severities. They keep
  their meaning: the first decides whether a skill is refused or reported as `skill_warning`, the second the CLI's exit
  code. The plan listed `cmd/cartographer/doctor.go` among the lint responses; its findings are not lint findings, so it
  is left alone and flagged for the maintainer.
- Supersedes the severity parts of D159, D186 (the `gate_check` floor), D290 and D299 (what counts toward the doctor
  nudge), D324 (the order), D336, D350, D365 (Problems/Suggestions), D370 (the sample's severity split), D688 (the `info`
  `unknown_type`), D716 (the crossing report) and D907 (the avoidability table's "at any severity"), without editing them.
