---
topic: deployment-release
---

# D1482 — The local server has a tray icon, in its own process

**Decision.** `cartographer tray` is a separate process, started at the user's logon, that polls the local `/health` and shows a map-pin icon in the Windows notification area or the macOS menu bar, with a small menu (status line, Open Atlas, Restart service, Open log, Quit). It decodes `/health` with `client.MCPClient.Health`, resolves the server address from the installed service definition on every tick, and acts only through `service.Manager` (the path of `service restart`) and the system "open". The four states (running, starting, stopped, not installed) are told by the icon's **shape**, never by colour; "a newer release exists" is a status-line suffix only. Icons are generated in Go as alpha masks (a black PNG template on macOS; on Windows the same mask drawn white or black from the taskbar theme), not checked-in binaries. Autostart is a third task, with its own opt-out and a `service tray` knob (later work packages of the same plan). The work lands directly on `main` in ordered PRs.

**Why.** A Windows user could see nothing that said the server was running and had no way to reach it when it hung; the Scheduled Task and the launchd agent have no visible surface. The icon must stay visible, shown stopped, when the server is down and be able to restart it, which a goroutine inside `serve` cannot do. A macOS template image is tinted by the system, so colour cannot carry the state on that platform; shape works on both, and one rule is cheaper than two. Generated icons leave no asset to review and no generate step. The cost is a second long-lived process per user session and a hand-drawn glyph that needs a human look on real hardware.

**Alternatives rejected.**
- A tray goroutine in `serve`: dies with the server, which is exactly when it is needed.
- Colour-coded icons: impossible for a macOS template image, so the two platforms would diverge.
- Reusing `service.ProbeHealth`: it drops `ready`, `kbs` and `latest_version`, and turns bootstrapping into an error; the client method decodes the wire format in one place.
- Checked-in PNG/ICO assets: binary files in a public repo with no review path.
- Lane `exp/<theme>`: the feature must ship in the next release, so this is a maintainer exception to `CONTRIBUTING.md` §Experiment branches; each PR is a normal `feat/<slug>` PR with the full gate.

**Consequences.** The tray must never write server or KB state, and judges liveness from the `/health` answer, not from localised text. With nothing installed and nothing answering, the state is "not installed" rather than "stopped", since the Restart item has nothing to start; a server that answers is "running" even with no service installed (someone ran it by hand), and the snapshot says whether a service exists. Platform UIs (Windows with `x/sys/windows`, macOS with purego and no cgo) and the autostart wiring follow in their own PRs; until then `tray` only reports that no UI exists, and nothing starts it. A future change must keep UI-toolkit calls on the thread that owns the toolkit and cross threads through the update channel.
