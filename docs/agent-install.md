# Agent-driven installation

Use this runbook whenever the user asks you to install Cartographer — a repository link and a
conversation is the normal starting point, and it is enough. It has one conversation and then
commands: interview the user **once**, install the binary, then let `cartographer setup` do the
rest from their answers. Execute every command in order and report the expected result before
continuing.

## 0. Interview the user — one message, before any command

Ask these together, in a single message, so the user answers once. Explain each in a line; do not
ask about anything that has a default (port, data directory, server name).

1. **Where the first knowledge base lives** — required. A KB is a git repository and its remote is
   what backs it up and syncs it. Either an **empty repository they own** (GitHub, Gitea, any git
   host), in which setup creates the KB, or a repository that **already holds a Cartographer KB**,
   which setup mounts. Only if they explicitly accept a throwaway local-only KB (not backed up,
   never synced) do you proceed without one — and say that limitation back to them.
2. **Which agent clients to connect.** Default: the one you are running in — name it (`claude`,
   `codex`, `opencode`, `kiro`, `antigravity`, `hermes`, `crush`). Offer to add the others they
   use on this machine.
3. **Where the KB should be visible.** By default a connected KB is readable from **every**
   directory on the machine. If they keep separate perimeters (work and personal repositories, a
   client's code), offer to confine it to one repository instead — free now, a migration later
   (D193). Default: everywhere.

If the user already gave some of these in their request, ask only for the rest. Do not start step 1
without an answer to question 1.

## 1. Install the binary

Detect the platform:

```bash
uname -s
```

Expected output: `Darwin` on macOS, `Linux`, or one of the Windows forms a POSIX shell reports
there (`MINGW64_NT-…`, `MSYS_NT-…`, `CYGWIN_NT-…`).

**If the command does not exist, you are on Windows** in PowerShell or `cmd`, where there is no
`uname` — that failure is the answer, not an error to report. On any platform other than macOS,
Linux and Windows, stop and report it.

**On Windows, install with `install.ps1`**, from PowerShell (in `cmd`, prefix it with
`powershell -NoProfile -Command`). `install.sh` refuses there on purpose (D252):

```powershell
irm https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.ps1 | iex
```

Expected output: `checksum OK`, then `installed: <version> -> <path>\cartographer.exe`. Then
continue from *Confirm the binary is available* below; everything after step 1 is
platform-neutral. Do not download or extract the release zip by hand: the script verifies the
checksum and puts the binary on the user `PATH`, and a hand procedure is how either step gets
skipped.

On macOS, first check for Homebrew:

```bash
command -v brew
```

Expected output: the path to `brew`. If it is present, install Cartographer:

```bash
brew install beppetemp/tap/cartographer
```

Expected output: Homebrew reports that `cartographer` was installed. If `brew` is absent, install
the current release instead:

```bash
curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | sh
```

Expected output: the installer reports the destination of the `cartographer` binary.

Confirm the binary is available:

```bash
cartographer version
```

Expected output: a Cartographer version.

## 2. Preview the plan

Turn the answers into flags and ask setup for its plan without changing anything:

```bash
cartographer setup --remote <git-remote-url> --agents <agents> --dry-run --no-input
```

Add `--workspace <repository path>` if the user chose to confine the KB (question 3); use
`--no-remote --name <name>` instead of `--remote` only for an accepted local-only KB.

Expected output: `Setup plan:` and four numbered lines — server, KB, agents, verify — then
`dry run — nothing was changed`. Check the KB line against what the user told you: `create … and
push it to <url> (empty repository)` or `mount … (the repository already has content)`. If it says
*mount* for a repository they called empty, or *create* for one they said holds their KB, stop and
tell them: the remote is not what they think it is, and nothing has been changed.

This step also proves the remote is reachable with this machine's git credentials: a failure here
is reported before anything is installed (see *Failures*).

## 3. Run it

The same command with `--yes` instead of `--dry-run`; the user already answered, so it does not ask
again:

```bash
cartographer setup --remote <git-remote-url> --agents <agents> --yes --no-input
```

Expected output: four sections — `==> server`, `==> knowledge base`, `==> agents`, `==> verify` —
ending with `server ready` and `Cartographer is set up.`, plus the Atlas URL. If it stops, the line
above `Setup stopped at …` is the cause: fix it and rerun the same command — finished steps are
skipped, so a rerun is always safe.

## 4. Verify the instructions reach the model

`cartographer status` should report in-sync with exit code 0. Then confirm the instructions actually
reach the model, not just the disk. `cartographer status` and
`cartographer doctor` now check the provider's own precedence chain (D189), but the provider's own
tooling is the ground truth — for Codex:

```bash
codex debug prompt-input
```

Expected output: a `cartographer:kb:*` section. If it is absent while `status` reports the
instructions installed, report it: a provider precedence rule Cartographer does not model yet.

`setup` (through `connect`) provisioned the bundled skills, including `cartographer-ops`. Use that skill for ongoing
operations, diagnosis, upgrades, and synchronization after installation. From there the bundled
`kb-create` and `kb-import` skills take over: `kb-create/references/artifacts.md` for authoring the
KB's skills, subagents, hooks and MCP descriptors, and `kb-create/references/secrets.md` for the
SOPS encryption flow.

## 5. Tell the user to restart their agent session

This is a step you cannot perform: the session that must restart is the one you are running in.
State it to the user explicitly, as the last thing you say:

> Restart your agent session now. The MCP tools and the provisioned skills are loaded at session
> start, so until you do, Cartographer is installed but invisible to me.

Omitting this is the single most common way a correct installation is reported as broken.

## Failures

| Observed symptom | Next action |
|---|---|
| `command -v brew` has no output | Run the `install.sh` command in step 1 (on Windows, the `install.ps1` command instead). |
| `uname` is not a recognized command | You are on Windows in PowerShell or `cmd`. Use the `install.ps1` step; do not treat it as a broken environment. |
| `install.sh` says Windows is installed with `install.ps1` | Correct: run the `install.ps1` command it prints, from PowerShell. |
| `install.ps1` fails with `running scripts is disabled on this system` | That is the file form under a restrictive execution policy; the `irm … \| iex` form in step 1 is not subject to it. Rerun that exact command rather than changing the policy. |
| `cartographer` is not recognized right after `install.ps1` | The command ran in a different process than the shell you are typing in. Open a new PowerShell window — the directory is on the user `PATH` — or invoke `%LOCALAPPDATA%\Cartographer\bin\cartographer.exe` directly. |
| The user's agent shows no Cartographer MCP tools after a successful `setup` | The session was not restarted. Repeat step 5 — this is not a failed install. |
| `cartographer status` exits non-zero immediately after setup | The service may still be starting: wait a few seconds and retry once before diagnosing. |
| `setup` stops at the server step | Usually port 39273 is taken by another process: free it, or inspect `cartographer service status`, then rerun. |
| `setup` says it `cannot reach <url> with this machine's git credentials — nothing was changed` | The line under it names the cause (unknown host, SSH key rejected, repository not found, credentials rejected, host key not in `known_hosts`). Configure ambient credentials — an SSH agent for SSH remotes, a git credential helper for HTTPS — or, for a host key, have the user run `ssh <host>` once to review and accept it (setup never accepts one for them, D173). Then rerun step 2. |
| `setup` says `no KB is mounted yet` | `--remote` was missing: go back to question 1 of the interview. |
| `setup` says `choose which ones the agents receive with --kb` | This machine already mounts two or more KBs and the agents have no binding yet (D190). Ask the user which KBs these agents should receive, then rerun with `--kb <name>` (repeatable) or `--kb all`. |
| `setup` says `this client is connected to <url>, not to a local server` | This machine already uses a remote Cartographer server; setup does not re-point it. To add agents to that server use `cartographer connect --agents <agents>`. |
| `setup` says `git is not installed` | Install git (on macOS `xcode-select --install` or Homebrew; on Windows the installer from git-scm.com), open a new shell, rerun. |
| `setup` says `no agent client detected` | Name the agent explicitly with `--agents`; if it is really absent, it has to be installed first. |
| `setup` stops at the KB step with `not an OKF KB` | The repository holds content that is not a Cartographer KB. Use the `kb-import` skill to import it into an OKF KB, push it, then rerun. |
| `setup` stops at the KB step with `Init: initial commit` | Git refused the KB's first commit; the git message under it names why (a commit hook, commit signing with no usable key; `Committer identity unknown` on a Cartographer older than D265 — set `git config --global user.name`/`user.email`). Fix it, remove the partial scaffold the hint names, rerun. |
| `setup` stops at the KB step with a push failure | The repository was not empty after all, or the forge rejected the commit's author: the output names the fix. `src refspec main does not match any` is neither — the KB has no initial commit (see the row above; the hint prints the commit command). The scaffold is kept (D156). |
