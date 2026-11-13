// Run from web/ so vite and its deps resolve: cd web && npx vite --config ../film/vite.config.ts
// No `import { defineConfig } from "vite"`: film/ has no node_modules, a plain object does the same.
import fs from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "..");

// The films are the lines of films.txt, the one list render.sh and record.mjs read too. A film's page is
// <name>.html, except the launch film's, which is index.html (its URL is the server's root).
// A NUL byte in the list is refused, as render.sh, publish.sh and record.mjs refuse it.
const filmsText = fs.readFileSync(path.join(here, "films.txt"), "utf8");
if (filmsText.includes("\u0000")) throw new Error("film/films.txt holds a NUL byte, which is not in a film name");
const films = filmsText.split("\n").map((l) => l.trim()).filter(Boolean);
const input = Object.fromEntries(films.map((name) => [name, path.join(here, name === "launch" ? "index.html" : `${name}.html`)]));

export default {
  root: here,
  cacheDir: path.join(root, "web/node_modules/.vite-film"),
  resolve: {
    // One copy of three, the Atlas's: a second one breaks instanceof checks inside the engine.
    alias: { three: path.join(root, "web/node_modules/three") },
  },
  server: { port: 5180, strictPort: true, fs: { allow: [root] } },
  build: { rollupOptions: { input } },
};
