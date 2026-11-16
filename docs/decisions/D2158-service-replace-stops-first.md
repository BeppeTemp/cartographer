---
topic: deployment-release
---

# D2158 — A Windows install that replaces the serve task stops the running server first, and puts the old task back when the replacement is refused

**Decision.** When the definition `service install` renders differs from the
installed one (or cannot be read to compare), or an elevated install must
repair the registered descriptor, the running server is stopped through the
graceful stop of `service stop` **before** the task is registered over, then
the new task is registered and started. A server that survives the stop fails
the install with the old task untouched. When the scheduler refuses the new
registration, the old task is started again; after a descriptor repair, which
deletes the task first, the old definition still on disk is registered again,
rendered afresh from the binary and the config the file names (never its raw bytes, which a process of the same user may have rewritten and which are registered under the elevated token, as `refreshTaskOwner` does), and if that fails the error says the task is gone and names
`cartographer service install`. An unchanged definition keeps the previous
order (register, then stop and start).

**Why.** [D1979](D1979-windows-task-owner.md) registers first so that a refused
registration leaves the running server alone. But once the scheduler holds a
new registration the process the old one started belongs to no task: it is no
longer `Running` for the new task, nothing stops it, and it keeps its port and
data directory (#2144). Stopping first is the only order in which the stop can
find the process through its task. #1961's guarantee, that a refusal never
leaves the service down, is kept by the restart on refusal instead of by the
order.

**Alternatives rejected.** Register first and stop through the old task's name
afterwards: the registration replaces the task, so there is no old task to ask.
Kill the process by port: the install would stop a program that is not ours.
Stop first with no restart on refusal: reverses #1961, a refusal for lack of
rights would leave the server down.

**Consequences.** A refused replacement costs a stop and a start of the
server. The restart is best effort; a refusal that also denies the start leaves
the server stopped and the error naming the refused registration. A change to
the install order has to keep both properties: no process without a task, no
refusal that leaves the service down.

`setup` and `doctor` treat a listener that accepts the connection and never
answers `/health` (`ListenerUnresponsive`) like a foreign one: setup stops
before changing anything and doctor warns, each saying that the program
"accepted the connection but did not answer /health". Only `service status`
and the tray, which poll a server that may merely be busy, do not call it
foreign.
