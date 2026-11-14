---
topic: project-governance
---

# D2068 — The deployment and plugins films draw concepts, not terminal output

**Decision.** The `deployment` and `plugins` films tell their story the way the launch film does: boxes for the
machines and the components (each component always with its name and role), lines that draw themselves between
them, a caption per scene and at most one real command on the bottom line. They share `film/src/parts.ts`. The
transcript machinery of D2041 (`film/transcripts/`, `make film-transcripts`, e2e `31_film_transcripts`) is
removed, since no film shows terminal output any more.

**Why.** The terminal films were accurate but hard to read: a viewer had to parse command output to learn what a
deployment profile or a plugin is, and the scenes were mostly empty card. A concept film says in one look what runs
where and what a plugin may do. It costs the guarantee that every line on screen is the binary's: what the films
claim is now held to the docs by hand, except the deployment scenarios, which `PROFILES` still pins to the rows of
`docs/deployment.md` §Deployment scenarios.

**Alternatives rejected.**
- Keep the terminal and add diagrams beside it: two things to read at once, and the transcript still drives the
  length of the film.
- Keep the transcripts as a test of the commands the chips show: a chip shows one command name, which a transcript
  of its full output does not check any better than a reader does.

**Consequences.** A change of CLI output no longer touches the films. A change to the deployment scenarios table,
to the contribution points of `docs/plugins.md` or to the plugin approval flow needs a look at the films (`film/README.md`
says which section each film follows). A future film that shows terminal output brings its own capture back.
