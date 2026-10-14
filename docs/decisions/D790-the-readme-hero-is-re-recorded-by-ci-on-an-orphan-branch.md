---
topic: project-governance
---

# D790 — The README hero is re-recorded by CI on an orphan branch

**Decision.** This reopens the "outside CI" consequence of D330. The README's hero is no longer a file of `main`:
the `hero` workflow (`hero.yml`, called by `release.yml`) records the Atlas of each release that changes `web/` and
force-pushes it as `hero.webp`, the single commit of the orphan branch `readme-assets`; the README points at
`raw.githubusercontent.com/BeppeTemp/cartographer/readme-assets/hero.webp`. The recording is deterministic and
headless: `hero.spec.ts` installs Playwright's clock paused before the page loads and produces each frame by
advancing it by `1000/HERO_FPS` ms (30 by default), stepping the CSS transitions and animations by the same amount,
and taking a screenshot at a device scale factor of 2. `make hero` runs the same path anywhere, into a gitignored
`hero.webp`.

**Why.** The hero should always show the Atlas of the latest release, with nobody in the loop. D330's recording
filmed the compositor in real time in a headed window on a GPU, which no runner has, and a real-time capture on a
software renderer is choppy (headless Chromium hands the screencast under 20 frames/s). Stepping a virtual clock
makes the picture independent of how fast the machine paints: a slow renderer only makes the capture take longer.
The file is 16-25 MB, so committing a new one per UI release would grow every clone by that much each time; on an
orphan branch that is force-pushed, the old ones become unreachable. The cost is that the README depends on a
branch outside `main`'s history and on a recording nobody reviews in a PR.

**Alternatives rejected.**
- *Keep `docs/atlas/hero.webp` on `main`, re-recorded by hand.* It needs a GPU machine and a person, goes stale
  between runs, and costs ~16 MB of every clone per recording (the pack was already 71 MB with five versions).
- *Record the DevTools screencast on the runner.* Headless it delivers under 20 frames/s and SwiftShader is slower
  still; a headed window under a virtual display has no GPU either. The capture would be choppy.
- *GitHub Pages or a release asset.* camo proxies Pages images and answers 404 for a 17 MB file;
  `releases/latest/download/...` is 404 because every release is a prerelease. `raw.githubusercontent.com` is not
  proxied and renders at any size (verified with `gh api markdown`).
- *Push the hero to `main` from the workflow.* `main` is protected, and its history is what we are keeping light.
- *Record on every release, or compare with the previous tag.* The first costs a runner for nothing when the UI did
  not change; the second depends on which tag came before and on every release having run. A `Web-Tree:` trailer
  on the branch's commit says which `web/` the hero was recorded from, so a release records only when its own
  tree differs.
- *`page.clock.fastForward` to halve the renders.* It fires due timers at most once, which is not how the page
  runs; `runFor` is the faithful step.

**Consequences.** The suite run of `hero.spec.ts` (no `HERO_RECORD`) is unchanged: the fixture, full speed, the same
assertions, so a step of the tour that no longer exists still fails CI (D330's "stale loudly"). In a recording
every wait goes through one helper that steps frames until its condition holds and fails with the step's name after
30 virtual seconds. Two traps live next to the code that hits them: the fake clock does not drive CSS transitions
(`stepAnimations`), and headless WebGL needs `--enable-unsafe-swiftshader`, which the Playwright config already
passes and the recording keeps. The workflow holds `contents: write` on `GITHUB_TOKEN` and nothing else, never
pushes to `main`, is not awaited by anything, and always records when dispatched by hand (the tag must contain this
machinery, since it checks out the tag's UI, tour and scripts). `publish-hero.sh` builds the commit with plumbing
in the clone's own object store, not in a scratch repository: the checkout's credentials belong to that clone's git
dir. The branch exists before the README switches to it (it was bootstrapped from the PR branch with
`publish-hero.sh hero.webp main@<sha>`), so the README never points at a missing image; the first release that
changes `web/` replaces it.

Measured on an Apple-silicon Mac: the tour is 517 frames (17.2 s of film at 30 fps), captured in about 5 minutes,
0.4-0.6 s a frame including the warm-up, and two runs on the same build give the same frame count. The encoded
WebP is about 25 MB at quality 92 (`HERO_QUALITY`: 85 gives about 17 MB, 80 about 14 MB), against D330's 16 MB at
the same settings: the frames are lossless screenshots, not a JPEG screencast, so more detail survives to the
encoder. The first run on `ubuntu-latest` has to confirm the budget: the job's timeout is 60 minutes, and if it is
exceeded `HERO_FPS` in `hero.yml` drops to the highest of 24 or 20 that fits, the measured per-frame time going
here.
