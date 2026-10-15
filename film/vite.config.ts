// Run from web/ so vite and its deps resolve: cd web && npx vite --config ../film/vite.config.ts
// No `import { defineConfig } from "vite"`: film/ has no node_modules, a plain object does the same.
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "..");

export default {
  root: here,
  cacheDir: path.join(root, "web/node_modules/.vite-film"),
  resolve: {
    // One copy of three, the Atlas's: a second one breaks instanceof checks inside the engine.
    alias: { three: path.join(root, "web/node_modules/three") },
  },
  server: { port: 5180, strictPort: true, fs: { allow: [root] } },
};
