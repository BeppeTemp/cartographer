// Checks the compressed size of the shipped bundle against the Atlas budget
// (docs/testing.md §Atlas UI budgets): initial JS + CSS <= 700 KiB gzipped.
//
// "Initial" is everything index.html loads, which with this build is every
// asset: there is no lazy chunk. Gzip level 9 approximates what a browser
// receives from a compressing proxy; the binary itself serves the files
// uncompressed, so this is a ceiling on the work, not a transfer measurement.
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { gzipSync } from "node:zlib";

const BUDGET = 700 * 1024;
const dir = new URL("../../internal/webui/dist/assets/", import.meta.url).pathname;

let total = 0;
for (const name of readdirSync(dir).sort()) {
  if (!/\.(js|css)$/.test(name)) continue;
  const size = gzipSync(readFileSync(join(dir, name)), { level: 9 }).length;
  total += size;
  console.log(`budget: ${name} ${(size / 1024).toFixed(1)} KiB gzipped`);
}
console.log(`budget: total ${(total / 1024).toFixed(1)} KiB of ${BUDGET / 1024} KiB`);
if (total > BUDGET) {
  console.error("budget: the Atlas bundle is over its compressed JS+CSS budget");
  process.exit(1);
}
