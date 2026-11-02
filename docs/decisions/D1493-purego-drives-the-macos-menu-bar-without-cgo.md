---
topic: deployment-release
---

# D1493 — purego drives the macOS menu bar without cgo

**Decision.** The macOS tray UI calls AppKit (`NSApplication` in accessory mode, `NSStatusItem`, `NSMenu`, a registered delegate class for the four actions) through `github.com/ebitengine/purego` and its `objc` package, with `CGO_ENABLED=0`. The dependency is imported only from `internal/tray/tray_darwin.go`, so Windows and Linux binaries do not link it. Updates reach the main thread through a mutex-guarded slot and `performSelectorOnMainThread:`; Quit and SIGTERM stop the run loop with `stop:` and a posted application-defined event, so the process returns through `runTray` instead of `terminate:` calling `exit`.

**Why.** The release build is `CGO_ENABLED=0` (`.goreleaser.yaml`), which keeps cross-compiling and the supply chain simple, and a menu-bar icon needs Cocoa. purego is the one pure-Go way to message Objective-C. The cost is a runtime-by-name binding: a wrong selector or argument type fails when the code runs, not when it compiles, so the darwin UI is checked by `test/macos/tray.sh` and by hand on a real Mac, not by the Linux gate.

**Module facts** (checked on v0.11.2). Licence Apache-2.0; pure Go on darwin (it ships its own `fakecgo`); its only dependency is `golang.org/x/sys`, already in the graph; maintained by the Ebitengine author and used by Ebitengine itself. It ships no upstream `NOTICE` file, so `/NOTICE` is unchanged (Apache-2.0 §4 asks for the licence text, which the module carries and the release SBOM lists). It runs native code at the process's privilege, so a bad version could do anything the user can; the mitigations are `go.sum` pinning, Dependabot's gomod ecosystem, `make vulncheck` (`security.yml`) and the SBOM generated at release. There is no licence gate to extend.

**Alternatives rejected.**
- cgo with Objective-C: breaks `CGO_ENABLED=0` cross builds and needs a macOS toolchain on every release path.
- A Swift or AppKit helper binary: a second artifact to sign, notarise, ship and keep in step with the CLI.
- `getlantern/systray`-style libraries: cgo on macOS, and a menu model of their own on top of the one `tray.MenuItems` already gives both platforms.

**Consequences.** `make vet` has a `GOOS=darwin` leg so the file compiles on the Linux gate. Cocoa needs the process's first thread: `init` in `tray_darwin.go` locks the main goroutine to it and `cmdTray` calls `UI.Run` from `main`; AppKit is never touched from the poll goroutine. Setup and each update run in an autorelease pool. When AppKit hands back no status item or button the command exits non-zero; what an ssh or Background-session launch does was only observed once (a Background-session job kept running with no error and no crash), so the UI promises nothing there beyond that check. A new selector goes in `selectorActions`, which a test pins. The work lands directly on `main`, the same maintainer exception to `CONTRIBUTING.md` §Experiment branches as D1482.
