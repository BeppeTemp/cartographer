---
topic: skills-services-secrets
---

# D196 — The onboarding skills route to references for artifact authoring and the encryption flow

**Status: implemented.** Closes #251.

**Context.** `kb-create` and `kb-import` take an operator from "no KB" to "KB mounted and a client
connected", and stop there. The two things a first onboarding actually struggles with are the two
things neither skill contained.

*Artifact authoring was absent.* Neither skill named `artifact_write`, `artifact_read`,
`artifact_list` or `artifact_delete` even once. `kb-create` offered `skill_list`/`skill_install`,
which copy a **bundled** skill into the KB — a different operation from authoring one. An operator
finishing the skill had a KB with concepts and no idea how to put a skill, a subagent, a hook or an
MCP descriptor into it: not the six accepted paths, not the shape of each, not the optimistic-write
protocol (`if_match` required on update, forbidden on create — an agent that guesses wrong fails its
first two attempts), not the client-side naming rules of D191, and not the manifest → trust →
projection → materialization chain that is what actually makes an artifact appear in a client.

*The encryption flow was one sentence* — "if the KB has SOPS secrets, add its age key as
`<nome>.age`" — which is the deployment half. Every step the operator must perform and Cartographer
never will was missing: the age key, the root `.sops.yaml` creation rules, the first encrypted file.
`kb-import` was worse: its secrets check told the operator that findings are "moved to the SOPS
flow" and never said what that flow was.

These are bundled skills, so this is not a documentation nit: their text is what an agent acts on,
and the operator experiences the gap in the product.

**Decisions.**

- **The skills become routers, not longer documents.** `kb-create` was already 128 lines; inlining
  two procedures would triple it and bury the common path. Each SKILL.md gains a Reference Files
  table mapping task → file, with the instruction to read only the matching one. This needed **no
  code change**: `//go:embed all:bundled` already embeds every file in a skill directory,
  `LoadSkill` reads frontmatter from `SKILL.md` only and is indifferent to siblings, and
  provisioning already transports auxiliary files as raw bytes.
- **The encryption reference is organized around the boundary, not the tool list.** Its spine is a
  table of who does what — operator: age key, `.sops.yaml`, first encrypted file, recipients;
  Cartographer: `secret_set` on an existing file, `secret_resolve`, `service_get`. The gap people
  fall into *is* that boundary, so the document is shaped like it, and `docs/skills-services-secrets.md`
  was corrected to state it as a boundary rather than as a remark inside the `secret_set` paragraph.
- **It opens with "do you need this at all".** A KB with no service credentials needs none of it;
  saying so first makes the section skippable instead of intimidating.
- **Failure modes are indexed by the symptom, not by the cause.** `sops` missing, a key that cannot
  decrypt, `rw` scope missing over HTTP, `secrets_on_non_service`, a pointer that does not exist,
  `secret_set` on a file that does not exist, and a committed file that turned out to be plaintext
  because the `path_regex` did not match — that last one carries the instruction to treat the value
  as compromised, because git history is forever.
- **The artifact reference states the client-side naming rules as author-side requirements.** From
  the author's perspective that is what they are: D191 had landed, so the implemented rules are
  quoted rather than cited as pending.
- **Every command is runnable as printed** (the D192 standard), with placeholders written as
  placeholders. These files are read by an agent that executes them literally.
- **`kb-create` step 2 now leads with `cartographer kb create <name> --remote <url>`**, which
  scaffolds, attaches the remote and pushes in one command, with the `serve --kb --init` + manual
  remote sequence demoted to the fallback for a machine that cannot reach the remote. Flags were
  checked against `cmd/cartographer/kbcmd.go`, not paraphrased.
- **The three defects D192 item 5 owned were verified gone, not re-fixed**: the `.cartographer.yaml`
  edit, the missing `cartographer client bind`, and the naming self-contradiction are all already
  corrected on `main`.
- **A test guards the routing in both directions.** `internal/skillbundle` now asserts that every
  `references/` file is reachable from the embedded FS and non-empty, and that every one of them is
  mentioned by its skill's `SKILL.md`. A reference added to the tree but not to the embed, or one no
  skill routes to, can no longer ship silently.
- **No new MCP tool and no new CLI flag.** This ships documentation inside the binary.

**Invariants kept.** Bundled skills keep their frontmatter contract and are validated on load;
only `version:` changed (`kb-create` 2.4 → 3.0, `kb-import` 1.0 → 1.1) plus the `kb-create`
description, which had to say what the skill now covers or the router would never be loaded.
`skill_install` copies what it always copied. No MCP tool behaviour, destination or config key
changed. The GitOps/Kubernetes procedure stays `kb-create`'s primary topology.

**Consequences.** Bundled-skill content only, no behaviour change. A KB that installed `kb-create`
with `skill_install` keeps its old copy until reinstalled — a stale installed copy is exactly the
confusion this closes, so it is worth a release note.
