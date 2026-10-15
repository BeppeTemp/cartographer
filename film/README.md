# Launch film (draft)

A ~46 s silent motion-graphics film. The graph is the real Atlas engine (`LivingScene`, offscreen); the page is a pure
function of time, so it renders deterministically. Nothing under `web/` is touched (it would change the bundle hash).

1. Preview (plays in real time, loops, `R` restarts): `cd web && npx vite --config ../film/vite.config.ts`, open http://localhost:5180/
2. Keep that server running, then in another shell from the repo root:
3. Review stills: `node film/record.mjs --stills 2.5,6,9,11,17,27,31,37,45` writes `film/out/still-<s>.png` (fractional seconds allowed)
4. Full render: `node film/record.mjs` writes `film/out/frames/*.png`, (emptied first) then `film/out/film.mp4` and `film/out/film.webp`: one command does it all (needs ffmpeg for the mp4 and `img2webp`, from Homebrew's `webp` package, for the animated WebP: ffmpeg has no libwebp encoder there)
5. Frames must be rendered in order from 0 (the physics is stateful); the script does, and `--stills` still renders all frames up to the last still.
6. WebGL in headless Chromium goes through SwiftShader (software), so a full render takes minutes, not seconds.
7. Everything is in `film/src/main.ts` (timeline in `overlays()` and `EVENTS`; the cast, one person per coding agent and colour, is `CAST`; scenes A team, B one wiki, C Monday, D Tuesday, E every day, F end card); the synthetic KB is `film/src/data.ts`.
8. `film/out/` is git-ignored.

Soundtrack: `soundtrack.mjs` synthesises it in code (pad, FM electric piano, bells and a knock on the
film's cues, which the page exposes as `window.film.cues`). A full render muxes it into `out/film.mp4`
(`out/film-video.mp4` stays silent); `node film/record.mjs --audio` regenerates only the soundtrack and
remuxes it onto the last video, in a few seconds.
