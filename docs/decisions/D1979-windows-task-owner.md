---
topic: deployment-release
---

# D1979 — The Windows tasks belong to the user, whoever installs them, and a restart needs only the right to start

**Decision.** The Scheduled Tasks `\Cartographer\Serve`, `\Cartographer\Sync` and
`\Cartographer\Tray` are registered for the installing user whatever the token:
each XML names the user in `<Principal><UserId>` and carries an explicit
`<SecurityDescriptor>` granting that user's SID, SYSTEM and Administrators full
control. An install from an elevated shell is **not refused**: it registers the
same user-owned task and prints one line saying so, which is also how a task an
older install left owned by Administrators is handed back to the user.
`service install` registers first and only then stops the old server and starts
the new one, so a refused registration leaves the running server untouched.
`upgrade-repair` re-registers each installed task (Serve, Sync, Tray) whose
definition lacks the descriptor, without stopping or starting anything, and the
tray install registers before it stops a running tray; if the scheduler refuses (the old
admin-owned task), it prints once the owner and `cartographer service install`
to run from an elevated terminal. `service restart` signals the shutdown event,
waits for the old server to exit, then `Start-ScheduledTask`, and runs
`Enable-ScheduledTask` only when the task is `Disabled`. A change the scheduler
refuses names the task's owner and the command that registers that task again.
This **amends D217**. Separately, a client lock that cannot be opened for lack of
access rights (Windows `ERROR_ACCESS_DENIED`) has its own message and `sync` exits
`3` for it; the session-start hook scripts do not read the exit code, so the
code tells a caller permissions from the rest and nothing more.

**Why.** A task registered from an elevated shell belongs to Administrators: a
non-elevated session of the same user (an agent's terminal, which is what ran
into it) can read it but not Enable, Disable or Stop it, so `restart` and
`upgrade-repair` failed with an access error that named no remedy. Refusing the
elevated install would have closed that door but also the only way to repair a
task already owned by Administrators, and users do run installers elevated. The
lock failure of the same sessions (the sandbox cannot write the home) was
reported as if another operation held the lock, which is false and which no
retry can fix.

**Alternatives rejected.**

- *Refuse an install from an elevated shell.* It cannot migrate an existing
  admin-owned task, and it blocks users who installed that way.
- *Leave the task to inherit its owner and document "do not install elevated".*
  The failure surfaces much later, in another session, and names nothing.
- *Elevate on demand for restart.* A per-user service that needs a UAC prompt to
  restart contradicts D217's premise.
- *Keep the unconditional `Enable` and ask for the right.* It is the one call a
  restart did not need on an enabled task.
- *Retry the lock open.* It is permissions, not contention.
- *Make the session-start hook read exit 3.* A larger change to every generated
  hook script for a case the hook already survives.

**Consequences.** A task installed before this change keeps its owner until an
install registers it again (`upgrade-repair` does it for the serve task, and
asks for one elevated `service install` when the scheduler refuses). The
descriptor must keep SYSTEM and Administrators, or the machine's administrators
could not remove the task. The elevation check and the SID are test seams
(`windowsElevated`, `windowsTaskSID`); the PowerShell error mapping reads exit
code 5 from the script, never the localised message.
