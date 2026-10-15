---
topic: project-governance
---

# D807 — A launch film on the Atlas engine leads the README

**Decision.** The README opens with a one-minute silent film instead of the Atlas tour: four teammates, each
with their own coding agents, share one wiki; Monday one agent writes a fix down, Tuesday another agent on
another client finds it, every day the others add to it and a broken link is refused. It is motion graphics
written in `film/`, drawn by the Atlas's real graph engine (`LivingScene` offscreen, D691) and rendered frame
by frame like the hero (D790): 1920x1080 at 60 fps to MP4 for social posts, 1120 px at 30 fps to WebP for the
README. `film/publish.sh` force-pushes the WebP as the single commit of the orphan branch `readme-film`. The
Atlas tour stays, under "What you get".

**Why.** The tour shows the Atlas, not what Cartographer is for: nothing on the page showed several people and
several clients sharing what their agents learn, which is the whole pitch. A recording of real sessions was
considered first, but the story needs two people, two machines and a day between them, which a screen capture
cannot show in a minute; staged motion graphics can, and drawing the graph with the real engine keeps it honest
about what the Atlas looks like. The film costs a manual re-render and publish when the story or the look
changes, and it is reviewed by nobody in a PR, as the hero is.

**Alternatives rejected.**
- *A screen recording of real Claude Code and Codex sessions.* It cannot show a team or a day passing, and the
  agents' output is not reproducible between takes.
- *The graph drawn by the brand kit's studio prototype.* Cleaner, but it would show a graph the product does not draw.
- *The film on `readme-assets` beside the hero.* `publish-hero.sh` force-pushes a tree holding only the hero,
  so every release that changes `web/` would delete the film.
- *The WebP committed to `main`.* ~14 MB per version in every clone, the cost D790 removed for the hero.
- *A dark-theme variant.* The paper look is the film's identity and reads as a card on a dark page; a second
  render would double the work and say nothing more.
- *A soundtrack.* A synthesised one was built and dropped: the README plays the WebP muted anyway, and the
  social cut does not need it.
- *60 fps in the WebP.* Browsers do not honour animated-image frame delays much under 20 ms.
- *Recording the film in CI on every release, like the hero.* Worth doing, but the story changes by hand
  and rarely; deferred to a follow-up.

**Consequences.** The film lives outside `web/`, so editing it does not touch the Atlas bundle's provenance
hash; it imports the engine from `web/src` and calls one private method of `scene.ts` (`animateTo`, commented
where it is used), so a refactor of the camera there can break the film's render without failing a test. The
README depends on a second branch outside `main`'s history, `readme-film`.
