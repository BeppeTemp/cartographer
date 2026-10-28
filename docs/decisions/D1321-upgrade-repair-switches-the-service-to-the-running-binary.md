---
topic: deployment-release
---

# D1321 — upgrade-repair switches the service definition to the running binary

**Decision.** `upgrade-repair` compares the program the service definition runs with its own executable on every path. When they differ it re-renders the definition for the running binary (`Manager.SwitchBinary`, which is `Install` on the config the definition names), reloads the job and prints the binaries it switched from and to. It refuses, with a non-zero exit and the manual `cartographer service install` command, when the running binary is under the temp directory, in a go-build cache or at a version-pinned Caskroom path. After any macOS `bootout` (install, sync timer, uninstall) it waits up to 30 s, the server's drain, until `launchctl print` fails before it bootstraps or removes the plist. The lazy repair before `sync` never switches.

**Why.** A `SIGTERM` makes the supervisor restart the program the definition already names, so a service installed from the Cask and an update done with `install.sh` into another directory came back as the old version and the wait for `/health` timed out (#1321). `bootout` returns while the server still drains, so the `bootstrap` that followed failed with exit status 5 (#1322). It costs a rewrite of the whole definition: hand edits to it are lost, which the command says.

**Alternatives rejected.**
- Print which binary the service runs and stop: leaves the operator to run the commands by hand on every update from a second install location.
- Patch only the program in the existing definition: a second renderer to keep in step with install, for an edit nobody promises to preserve.
- Switch to any running binary: a `go run` or test build would become the service, gone at the next cleanup.
- Retry the `bootstrap` on error 5: hides a job that never unloads behind a slow failure with no message.

**Consequences.** Anything a definition needs must be in what `Install` renders. `Status.ServiceBinPath` is the definition's program and `Status.BinPath` stays the running executable. A unit path with a space is quoted per systemd.syntax and the readers parse quoted words (#1336).
