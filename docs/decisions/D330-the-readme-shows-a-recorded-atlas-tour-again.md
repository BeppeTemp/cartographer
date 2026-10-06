---
topic: project-governance
---

# D330 — The README shows a recorded Atlas tour again, filmed by a test

**Decision.** This reopens D198. The README's hero is an animated WebP of the
Atlas (`docs/atlas/hero.webp`): the graph, a search, a concept's links, colour
by Map, a skill in the Artifacts panel, the Observatory. The tour is
`web/e2e/hero.spec.ts`. In the `e2e-web` suite it runs over the fixture at full
speed, and `make hero` runs it over the demo KB at a watchable pace, filmed through the
browser's DevTools screencast at 2x and encoded by `web/scripts/record-hero.sh`. Along with it, the
README becomes a landing page: what Cartographer is for, what you get, a short
Quick start, a ✓/— client matrix and links. Install details, upgrades, removal,
configuration, build and structure live in `docs/getting-started.md`,
`docs/deployment.md`, `docs/sync.md` and `CONTRIBUTING.md`.

**Why.** D198 removed the old demo because nothing noticed when it went stale
and re-recording it needed a toolchain outside the build. Its consequences
left the door open to "a way to go stale loudly". A tour that is itself a test
gives that: a step the animation shows and the UI no longer offers fails CI on
the next web change, and re-recording is one command over a generated,
publishable KB. A static screenshot could not show what makes the product
different. Cartographer is more than a wiki: the same KB carries the skills,
subagents and hooks that configure every agent client, and a moving tour shows
both sides at once. The cost is the part a test cannot check: a cosmetic
change (spacing, colour) leaves the image behind until someone runs
`make hero`, which needs a GPU, ffmpeg and img2webp, so it stays outside CI.
It also adds about 6 MB per re-recording to the clone; the two screenshots it
replaces weighed about 0.8 MB.

**Alternatives rejected.**
- *GIF.* The living graph changes most pixels of every frame: the same tour
  was 11–25 MB as a 256-colour GIF against 4–6 MB as lossy WebP, which
  every current browser animates in an `<img>`.
- *Playwright's built-in video.* It encodes VP8 at about 1 Mbit/s, which
  smears a dense graph and its labels before the WebP pass even starts; the
  first hero shipped that way and looked it. The screencast hands over each
  composited frame losslessly, with its timestamp.
- *MP4 or a video link.* GitHub does not autoplay a repository video in a
  README, and a hero that needs a click is not a hero.
- *Hosting the image outside the repository* (release asset, docs site). It
  keeps the clone lighter, but the image is no longer versioned with the UI it
  shows, and the maintainer preferred it in the repository.
- *Pixel baselines to catch cosmetic drift.* D228 already rejects them: they
  break across runners and add maintenance.
- *Light and dark variants.* Twice the weight and twice the recording for one
  impression; the 3D graph reads best on dark.

**Consequences.** `hero.spec.ts` must keep running in both modes: the beats are
no-ops in the suite, and the recording-only settings (GPU, video, slow typing)
stay behind `HERO_RECORD`. The demo KB (`web/scripts/demo-kb.mjs`) now ships
skills, subagents, a hook and instructions so the tour has artifacts to show.
They use no `rand()`, so the concept graph the benchmark measures is
unchanged. A PR that changes what the tour shows re-records it in the same PR.
