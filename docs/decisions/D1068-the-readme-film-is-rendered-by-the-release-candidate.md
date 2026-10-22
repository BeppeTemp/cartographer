---
topic: deployment-release
---

# D1068 — The README film is rendered by the release candidate, and a failed render blocks the train

**Decision.** This amends D807, which left "recording the film in CI" as a follow-up. `film.yml` renders the
launch film on `ubuntu-latest` (software WebGL, no GPU) and force-pushes it as the single commit of the orphan
branch `readme-film`, with the push model of the hero (D790): `GITHUB_TOKEN` only, nothing pushed to `main`.
`preview.yml`, the Saturday release candidate, calls it in a `film` job that runs in parallel with the image build.
The film renders only when the hash of its inputs (`film/inputs.sh`: `film/`, `web/src/lib/graph3d/`, the palette,
the tokens, the brand CSS and logo, and `web/package-lock.json`) differs from the `Film-Tree:` trailer of the
branch's commit (`film/film-needed.sh`); a manual run always renders. **A render failure is blocking**: the job
goes red, the candidate run fails, and `release-train.yml` finds no successful candidate and skips the train.

**Why.** The film imports the real Atlas engine, so a camera refactor in `scene.ts` can break its render while
every test stays green, and a hand-rendered film goes stale unnoticed. The maintainer requires the README film to
be always present and current. This differs from the hero, whose recording never blocks a release (it runs after
the tag, `release.yml`): the candidate is built from the head of `main`, and the Monday gate requires that head, so
"the film rendered fine" there means "for the code being released", and a merge after the candidate forces a new
one. The price is a longer Saturday run and a candidate that can now fail on something users never see.

**Alternatives rejected.**
- *A required status check on the release PR (the `main-protection` ruleset).* A repository setting outside the
  code, and the release PR changes only `CHANGELOG` and version files, so a path-based check there says nothing
  about the film.
- *Hashing `web/` as the hero does.* The film uses a few files of `web/`, not its UI; the hash is of an explicit path
  list, so an unrelated UI change does not render a 60 s film, and `internal/repodocs` fails when an import of
  `film/src` leaves the list.
- *Rendering on every push to `main`.* Minutes of software rendering for a film that rarely changes.
- *Recording after the tag, like the hero.* A failure could then never block anything, which is the requirement.

**Consequences.** Every file `film/src/main.ts` imports must be in `film/inputs.sh` (a test checks it). The render
is exercised only by `film.yml`: it is not run on pull requests, so the first real run after a change to the
machinery is the Saturday candidate or a manual `gh workflow run film.yml`. `film.yml` grants only
`contents: write`, only the `film` job of `preview.yml` gets it, and nothing in its path may tolerate failure.
