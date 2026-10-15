# Launch film (draft)

A ~28 s silent motion-graphics film. The graph is the real Atlas engine (`LivingScene`, offscreen); the page is a pure
function of time, so it renders deterministically. Nothing under `web/` is touched (it would change the bundle hash).

1. Preview (plays in real time, loops, `R` restarts): `cd web && npx vite --config ../film/vite.config.ts`, open http://localhost:5180/
2. Keep that server running, then in another shell from the repo root:
3. Review stills: `node film/record.mjs --stills 2,7,12,18,25` writes `film/out/still-<s>.png`
4. Full render: `node film/record.mjs` writes `film/out/frames/*.png`, then `film/out/film.mp4` and `film/out/film.webp` (needs ffmpeg)
5. Frames must be rendered in order from 0 (the physics is stateful); the script does, and `--stills` still renders all frames up to the last still.
6. WebGL in headless Chromium goes through SwiftShader (software), so a full render takes minutes, not seconds.
7. Everything is in `film/src/main.ts` (timeline in `overlays()` and `EVENTS`); the synthetic KB is `film/src/data.ts`.
8. `film/out/` is git-ignored.
