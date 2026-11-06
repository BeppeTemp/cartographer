import { configDefaults, defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";

// The UI is served from /ui/ inside the Cartographer binary, so every asset
// URL has to be relative to that base -- an absolute /assets/... would escape
// the SPA mount and hit the 404 branch of the Go router.
//
// Source maps are off: the bundle is committed to the repository and embedded
// in the released binary, and shipping maps would double that payload for
// something only a developer with the sources can use.
// The static demo build (VITE_ATLAS_STATIC=1, demo/build.sh, D745) is served
// from wherever the site puts it (a GitHub Pages sub-path), so its base is
// relative; it is written to its own --outDir, never over the embedded bundle.
const perfOnly = process.env.PERF_ONLY === "1";
const staticDemo = process.env.VITE_ATLAS_STATIC === "1";

export default defineConfig({
  base: staticDemo ? "./" : "/ui/",
  plugins: [react()],
  // `npm run dev` serves the UI with hot reload and sends /api to a running
  // server: `cartographer serve --http :39273` by default, any other with
  // CARTOGRAPHER_DEV_API. A local config file under web/ would do the same but
  // enter the bundle's provenance hash and fail web-check in CI.
  server: {
    proxy: Object.fromEntries(
      ["/api", "/health"].map((path) => [path, process.env.CARTOGRAPHER_DEV_API ?? "http://127.0.0.1:39273"]),
    ),
  },
  build: {
    outDir: "../internal/webui/dist",
    emptyOutDir: true,
    sourcemap: false,
    target: "es2022",
  },
  test: {
    globals: true,
    environment: "jsdom",
    // jsdom's default about:blank gives the document an opaque origin, and an
    // opaque origin has no Storage: without a real URL every localStorage and
    // sessionStorage assertion fails for a reason that has nothing to do with
    // the code under test.
    environmentOptions: { jsdom: { url: "http://localhost:39273/ui/" } },
    // Only the unit/component tests: the Playwright suite under e2e/ runs
    // against the built binary through `make e2e-web`, never under jsdom.
    // The wall-clock budget (src/test/perf.test.tsx) runs alone: `npm test`
    // leaves it out, because beside vitest's other workers (or a signoff's
    // Windows leg) its timing measured the load, not the code; `npm run
    // test:perf` (PERF_ONLY) runs only it, in one worker.
    include: perfOnly ? ["src/test/perf.test.tsx"] : ["src/**/*.test.{ts,tsx}"],
    exclude: perfOnly ? [] : [...configDefaults.exclude, "src/test/perf.test.tsx"],
    setupFiles: ["./src/test/setup.ts"],
    css: true,
  },
});
