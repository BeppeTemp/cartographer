---
topic: deployment-release
---

# D174 — `service status` reports observable state, not a verdict on nothing

**Decision.** `cartographer service status` keeps inspecting the **local native
service only**, and says what it observed rather than rendering everything as a
pair of booleans. `service.Status` gains `Lifecycle`
(`not_installed`/`not_loaded`/`loaded`), `HealthChecked` and `HealthSkipReason`;
the health line is printed only for a service that exists; a client pointed at a
different server is one line of context. `doctor` names the one combination that
cannot work.

**Context.** The starting report was a contradiction: a server answering `/health`
while `service status` said `healthy: false`. There is no contradiction — the two
speak about different things and both answer correctly, `/health` about the remote
server in `server_url` and `service status` about the local launchd/systemd job.
What was genuinely wrong is narrower, and all of it is a presentation defect.

- **No health verdict on what does not exist.** The `healthy` line was printed
  unconditionally, so a machine with no local service showed `healthy: false
  (http )`. Read quickly that is an outage; it is an absence. With
  `installed: false` the output now says there is no local service and how to
  install one.
- **`Healthy` conflated "unhealthy" with "not checked".** One boolean carried
  both meanings whenever the config was missing, unreadable, or configured for
  stdio. The probe now reports whether it ran, and why not — the root of the
  misreading was the missing distinction, not the probe.
- **An empty `http:` is stdio, not "apply the default".** `serve` selects the
  stdio path exactly when `cfg.HTTP` is empty. Substituting
  `defaults.DefaultListenAddress` in `Status` would report a health check against
  an address nothing listens on: a fabricated failure.
- **`Running` promised more than the probe delivers.** `launchctl print`
  succeeding proves the job is registered with launchd, not that a process is
  alive. The JSON field keeps its name (it is a contract) and its printed label
  became `loaded`, with `lifecycle` as the field to read.
- **`service status` is not widened to the remote server.** Answering about both
  would make it ambiguous; the remedy for the misreading is to *say* which one it
  inspects. A local service alongside a client pointed at a shared remote server
  is legitimate — context, never a warning.
- **One `doctor` finding, for the case that is actually broken.** `server_url` is
  loopback, nothing answers, and no local service is installed: the client points
  at a server that does not exist. The generic remedy, `service status`, would
  only repeat `installed: false`. The mirror case — a local service installed
  while the client points elsewhere — is deliberately not a finding.

**Consequences.** Output and JSON fields only. Existing fields and the
systemctl-like exit codes (`0`/`3`/`4`) keep their meaning, so `install.sh update`
is unaffected. A script reading `healthy` should read `health_checked` next to it;
one that wants the state should read `lifecycle`.
