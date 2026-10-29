# Writing a unit a small model can follow

An executor reads one `IngestUnit` and the Source and nothing else: no plan, no other unit, no
memory of the design. Everything it needs is in the unit.

## Fields

- `source`: the Source concept ID. One unit, one source; a large source is split into units by section,
  each naming its range in `extract`.
- `targets`: the exact concept IDs to patch or create. Mark each as **existing** or **new** in the body;
  the executor creates only what the unit lists as new and touches nothing else.
- `extract`: one line, what to take and what to leave ("the install steps and the supported platforms;
  not the changelog").
- `verify`: one line, a check the executor can do alone ("every step cited in the Source has a pointer;
  `gate_check` is clean").
- `executor`: `any` when a small model can do it from the brief; `large` when it needs judgement
  across pages; `human` when it needs access or a decision no agent has.
- `status`: `draft` until the operator approves, then `open`.

## Body: the brief

1. One sentence: what this unit achieves.
2. Per target: its ID, existing or new, and the headings it goes under.
3. Placement already decided: the owner of each subject involved, so the executor never searches for it.
4. What to do with a conflict: open a `Contradiction`, leave the page as it is.
5. The lease and what "done" means, in the plan's words.

## Do

- Name IDs, headings and fields literally; a small model does not infer them.
- Keep the whole unit under about 40 lines.
- Give units that share a target an explicit order in the body.

## Do not

- Point at the plan for a rule the unit needs: repeat it.
- Leave ownership open ("put it where it fits").
- Put a credential, a raw machine path or a real person's name in a unit.
