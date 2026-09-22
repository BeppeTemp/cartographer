import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The UI is served from /ui/ inside the Cartographer binary, so every asset
// URL has to be relative to that base -- an absolute /assets/... would escape
// the SPA mount and hit the 404 branch of the Go router.
//
// Source maps are off: the bundle is committed to the repository and embedded
// in the released binary, and shipping maps would double that payload for
// something only a developer with the sources can use.
export default defineConfig({
  base: "/ui/",
  plugins: [react()],
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
    setupFiles: ["./src/test/setup.ts"],
    css: true,
  },
});
