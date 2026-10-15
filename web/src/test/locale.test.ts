import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// The Atlas is English in any browser locale; a value test of a formatter passes
// on an English CI runner even with the bug back, so the source is scanned.
const SRC = join(process.cwd(), "src");

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walk(full, out);
    else out.push(full);
  }
  return out;
}

describe("locale", () => {
  it("formats every date in English, never in the browser locale", () => {
    const offenders: string[] = [];
    for (const file of walk(SRC)) {
      if (!/\.tsx?$/.test(file) || file.includes("/test/")) continue;
      readFileSync(file, "utf8").split("\n").forEach((text, index) => {
        const at = `${file.slice(SRC.length - 3)}:${index + 1} ${text.trim()}`;
        if (/\.toLocale(Date|Time)?String\((?!\s*"en")/.test(text)) offenders.push(at);
        else if (/\bIntl\.|navigator\.languages?\b/.test(text)) offenders.push(at);
      });
    }
    expect(offenders, 'pass "en" as the first argument; no Intl or navigator.language').toEqual([]);
  });
});
