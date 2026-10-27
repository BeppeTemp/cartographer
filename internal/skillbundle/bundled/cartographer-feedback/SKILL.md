---
name: cartographer-feedback
description: Use when the user asks to report a bug or an idea about Cartographer itself (the server, the client, a bundled skill, the docs), or when you hit product friction and the user agrees to report it. Drafts the report, replaces every real name with a placeholder, shows it to the user and prints a prefilled URL the user opens and submits; it files nothing itself.
version: "1.0"
---
# Cartographer feedback - Skill

## Purpose

An agent that meets a defect or a missing capability in Cartographer would otherwise lose the
observation with the session. This skill turns it, on request, into a ready-to-file report. The
repository is public: nothing from a real setup may appear in the text.

**Invariants.** This skill files nothing and holds no credentials: it needs no `gh`, no token, no
network. The report leaves the machine only via the user's browser. No file, KB page or log is
written, and nothing is kept between sessions.

## 1. When it runs

Only when the user asks, or when you met friction and the user said yes to a one-line offer
("this looks like a Cartographer bug, want me to draft a report?"). Never silently, never as a side
effect of another procedure.

## 2. Classify

- A defect, a regression, or documentation contradicting behavior: **bug**.
- A missing capability or a change of behavior: **idea**.
- "How do I...": not feedback. Tell the user the Discussions `Q&A` category (slug `q-a`) is the
  place, offer to draft there only if they insist, otherwise stop.
- Anything security-sensitive (a leak, a bypass, a credential exposure): do **not** file it
  publicly. Point to `SECURITY.md` in the repository and stop.

## 3. Collect

Ask the user only for what the session does not already show.

- Server version: the `kb_status` tool, field `server_version`. Also read `latest_version` when
  present: if the server is behind, advise updating first and trying again before reporting. If
  there is no `kb_status` tool, write "unknown".
- Client version: `cartographer version` when you have a shell, else "unknown".
- The agent client name and the transport (stdio or HTTP), if known.
- For a bug: minimal numbered reproduction steps, expected versus actual, the tool name and the
  error text. If the reproduction is unknown write "not reproduced"; never invent steps.

## 4. Draft

Bug, following the headings of the bug template: **Version and transport** / **What happened, and
what you expected** / **Minimal reproduction**.

Idea: the problem, who hits it, the proposed behavior, alternatives considered.

The title is short, descriptive, with no real names.

## 5. Substitute, then show

Replace before showing anything:

| Real value | Placeholder |
|---|---|
| KB name | `kb-a`, `work-kb` |
| host, URL, remote | `example.com`, `gitlab.example.com` |
| user name | `user` |
| local path | `$HOME/...` |
| concept ID | an `area/page`-style placeholder |
| token, password, key, secret | removed outright, not even a placeholder; tell the user |
| employer, client, project names | dropped |

In doubt, a name is private. Then **show the full draft and the title to the user** and wait for
edits or approval. The user, not you, decides what leaves. If the user refuses, stop: nothing is kept.

## 6. Give the URL

Percent-encode the title and the body (`%0A` for a newline). No tool is needed: build the string
yourself, or use `python3 -c` or `jq -rn '@uri'` only if a shell is there. Without a shell, encode
by hand at least `%`, `&`, `#`, `+`, space, newline, `?` and `=`.

<!-- Repo slug as in .github/ISSUE_TEMPLATE/config.yml. Unverified: GitHub may ignore `body` when
`template=` names a markdown template; if so, use `labels=bug` here instead of `template=bug.md`
(the body already follows the template headings) and update the test and the docs. -->
- Bug: `https://github.com/beppetemp/cartographer/issues/new?template=bug.md&title=...&body=...`
- Idea: `https://github.com/beppetemp/cartographer/discussions/new?category=ideas&title=...&body=...`

If the encoded URL would exceed **6000** characters, give the URL with the title only (and the
`template` or `category`) and print the body in a fenced code block for the user to paste.

Say plainly that nothing has been sent: the user opens the link and submits it.
