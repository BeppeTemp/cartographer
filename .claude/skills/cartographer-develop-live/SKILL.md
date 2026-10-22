---
name: cartographer-develop-live
description: Use when the outcome is found by trying rather than specified up front (Atlas UI, UX, CLI output, a behaviour to tune), or when the user asks to work live, with a live instance, or without a plan issue — changes Cartographer while the user tries it on a running instance, iterates on their feedback and lands one PR. Changes that can be specified first go through `cartographer-propose-plan` and `cartographer-implement-plan`.
---

# cartographer-develop-live — a running instance, the user's feedback, one PR

The other way to land a change. `cartographer-propose-plan` → `cartographer-implement-plan` separates the
design from the implementation and hands it over in an issue; a live session
keeps them together, because the user decides by seeing the result, and always
ends in one PR that lands in the release changelog (§The record). The rules
of the repository do not change: `main` is protected, one PR, `make gate`,
docs and decision in the same PR (`AGENTS.md`, `CONTRIBUTING.md` §Pull requests).

Choose it when the result can only be judged on the instance. If the change
can be written down completely before it is built, write a plan instead.

## Role

Before anything else, find your role (D955; `CONTRIBUTING.md` §Working with an agent client):

```bash
gh repo view "$(scripts/upstream-repo.sh)" --json viewerPermission --jq .viewerPermission
```

`ADMIN`, `MAINTAIN` or `WRITE` is **maintainer mode**; `TRIAGE`, `READ`, or any
failure to read it, is **contributor mode**. State the mode to the user in one line.

In **contributor mode** the session stops at an open PR (§3 step 5 up to `gh pr ready`):
no `gh pr merge`, no force-push you were not asked for; the branch is pushed to
`origin`, your fork, and worktrees branch from `upstream/main` (`make worktree-add`).
In maintainer mode nothing changes.

## Roles

- **Coordinator** — the session talking with the user. It owns the conversation,
  the worktree, the live instance, every git operation and the decision record.
  It turns feedback into changes and reads every diff before the user sees it.
- **`cartographer-live-implementer`** — a coding subagent on a cheaper model, pinned in its
  definition (`.claude/agents/`), spawned with `Agent`,
  `subagent_type: cartographer-live-implementer`, `run_in_background: true` (act on the completion notification),
  for the closing pass only (§3). It works in the coordinator's worktree and
  stops: it never commits, never runs `make dev`, never touches a file outside
  its mandate.
- **`cartographer-contract-reviewer`** — read-only, on a cheaper model, spawned once at closing
  (§3) on the draft PR, at most three rounds (`cartographer-review-pr` §5).
- **`cartographer-source-reader`** — read-only, on a cheaper model, spawned when a check of the
  session's PR is red: it reads the failed log and returns the failed jobs, test
  names and first error lines, so the log never enters the conversation.
- **Explore** (or the client's context-gathering subagent) — answers "where is X"
  for an area the coordinator does not know, so the files are not read into the
  conversation.

While iterating, the coordinator makes every change itself, with no subagent:
a live session is many small changes, and each spawn pays again for the context
the coordinator already holds (the mandate, the files it re-reads) while the
user waits to see the result.

## 1 — Start

1. `git pull --ff-only` on `main`, then `make worktree-add SLUG=<slug>` and work
   from `.worktrees/<slug>`.
2. `make dev` from the worktree, in the background. `DATA=<data dir>` serves a
   copy of real KBs (remotes removed, git sync off, deleted on exit); without it
   the synthetic test KBs `make dev` generates (D818). Another live session on the machine → pick other ports:
   `API_PORT=39281 UI_PORT=5174`.
3. Give the user the UI address: `http://localhost:<UI_PORT>/ui/` — `localhost`,
   not `127.0.0.1`, which Vite does not listen on. Go changes restart the
   server within seconds; `web/` changes reload in the browser.

## 2 — Iterate

For each piece of feedback:

1. Derive the `file:line` pointers and make the change yourself.
2. Read `git diff` before the user looks.
3. Tell the user what to look at, in one line. A build failure keeps the previous
   server running: the `make dev` log says so.
4. When the user accepts a step, commit it on `feat/<slug>` (`wip:` messages are
   fine, the merge squashes them): every accepted step is a point to go back to.

**No PR while iterating.** The commits stay local on `feat/<slug>`; nothing is
pushed and no PR is opened until the user says the session is over (§3): a PR
opened mid-session is reviewed, rebuilt by CI and read by others while its scope
is still moving. Keep the choices made, and why, in your notes for the decision
file; cite no `D<n>` in code or docs before the PR has its number.

### Mandate (self-contained: the subagent never sees the conversation)

- Worktree: `<absolute path>`; every command runs there (`git -C`, `make -C`,
  `cd <path>/web`). Another agent may be editing other files in it: touch only
  the files listed here.
- The change: the behaviour wanted, in the user's terms plus the exact semantics;
  the `file:line` pointers; the files you may edit.
- Tests: which to add, and the targeted command to run — `go test ./internal/<pkg>/ -run <Name>`
  or `cd web && npx vitest run <file>`. Not `make gate`: it runs at closing.
- Do not commit, push, start a server or edit a file outside the list.
- Report, at most 8 lines: files changed; tests run and their outcome; every
  assumption you made where the mandate was silent; every trap you hit.

## 3 — Close

1. Agree the scope with the user: what is in this PR, what becomes a Discussion (idea) or a bug issue.
   Only then open the PR, as a draft: `git push -u origin feat/<slug>` + `gh pr create --draft`.
   Its number is the decision number (D672): `make decisions-new N=<pr> SLUG=<slug> TOPIC=<topic>`,
   never guessed from the issue and PR lists (another session can take it first, D703).
2. Closing mandate to one `cartographer-live-implementer` (or yourself): the docs named in
   `docs/index.md` §Documentation maintenance rules for what changed, the tests
   still missing, `make web` if `web/` changed (the bundle is committed), then
   `make gate` until green. The coordinator writes the decision file: it holds
   the why.
3. Review: `cartographer-review-pr` run on the draft PR — `scripts/pr-triage.sh` on the branch,
   the reviewers of the tier on the worktree, rounds per `cartographer-review-pr` §5, fixes by you or a
   `cartographer-live-implementer`. You have already read every step's diff (§2), so this pass
   is the independent read. The user tried the behaviour; nobody else has read
   the code. A review that returns no findings after no work has not happened: check that
   it read the diff before you count it.
4. Before the merge, as in `cartographer-implement-plan` §4: `git fetch <upstream>` (the `upstream` remote when it exists, else `origin`), rebase on
   `<upstream>/main` in the worktree (a conflict in `internal/webui/dist/` → take
   `main`'s side, then `make web`; any other non-append conflict → stop and ask),
   the full `make gate` on the rebased tree, `git push --force-with-lease`.
5. Write the record (below), then `gh pr ready`. Contributor mode stops here: the maintainer merges. Maintainer mode merges only on the user's
   decision, and not in the release freeze: from Saturday 00:00 Europe/Rome until Monday's train has run (`TZ=Europe/Rome date +%u%H%M`: weekday 6-7, or Monday before 0830; on Monday after that, until `gh run list --workflow release-train.yml --event schedule --created "$(date -u +%F)" --json status` shows two runs, both `completed`: the cron often starts late, and a merge before the train skips it) a PR whose title type is `feat`, `perf` or `refactor`, or that is breaking (`!:` or a `BREAKING CHANGE` footer), is not merged and gets no auto-merge: say it waits until after Monday's release. Other types are unaffected. Otherwise `gh pr merge --squash --delete-branch --auto` merges once the
   required checks are green; confirm with `gh pr view <pr> --json state`. A
   required check that fails: spawn `cartographer-source-reader` with the PR number and fix from its
   digest, not from the log.
6. Stop `make dev`; after the merge, `make worktree-rm SLUG=<slug>` from the main
   working copy, then `git pull --ff-only`.

## The record — every live session ends in the changelog

A live session leaves no plan issue, so the PR is the whole record of what was
done, and it is what the release changelog is built from. The repository squashes
with the PR title as the commit subject and the PR body as its message;
release-please reads both.

- **Title**: a conventional commit that names the user-visible change, `feat:` or
  `fix:` when a user would notice it. `docs:`, `chore:`, `refactor:` and `test:` do not
  appear in the changelog: use them only when that is true of the whole PR.
- **Several user-visible changes** in one session: one extra conventional-commit
  line per change in the PR body, each at the start of its own paragraph — release-please
  turns each into a changelog entry. Each is **one unwrapped line** (release-please keeps
  only the first physical line of an entry, so a hard-wrapped one is cut mid-sentence;
  `pr-title` fails the PR) and its scope is one of the canonical scopes
  (`CONTRIBUTING.md` §Pull requests), which the changelog groups by. Changes that are
  unrelated go in separate PRs. Conversely, no prose line in the body may start with
  `feat:`/`fix:` by accident.
- **Body**: what changed and why, step by step as it was accepted; what the user
  verified on the instance; what was tried and dropped, when a reader would
  re-propose it; the decision file; the follow-ups opened as Discussions or bug issues.
  The `wip:` commits are squashed away: the body is what remains.

## Traps

- A `D<n>` cited in code or docs before `docs/decisions/D<n>-*.md` exists fails
  `TestEveryDecisionCitedResolves`: cite it only once the draft PR exists (§3 step 1) and the file with it.
- A test that passes in CI and fails on your machine is reading your real home
  (a token file, a client config): isolate it with `setHome(t, t.TempDir())`.
- Removing a feature that has shipped is a migration: what it installed on users'
  machines (a scheduled job, a file) must be removed by the new version.
- Keep no file of your own under `web/`: it enters the bundle's provenance hash
  and `web-check` fails in CI (`docs/testing.md` §Frontend).
