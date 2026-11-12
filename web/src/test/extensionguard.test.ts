import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * No third-party JavaScript, and no interpreted string, in the Atlas (D1938):
 * the files that draw a plugin's data never use a sink that would run it.
 */
const src = resolve(__dirname, "..");
function files(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const full = join(dir, name);
    return statSync(full).isDirectory() ? files(full) : [full];
  });
}
const drawn = [
  ...files(join(src, "components")).filter((f) => /\/(extension\/|ExtensionView\.tsx$)/.test(f)),
  join(src, "lib", "extension.ts"),
];

const BANNED: [string, RegExp][] = [
  ["dangerouslySetInnerHTML", /dangerouslySetInnerHTML/],
  ["innerHTML", /innerHTML/],
  ["eval(", /\beval\s*\(/],
  ["new Function", /new\s+Function/],
  ["dynamic import of a non-relative specifier", /\bimport\s*\(\s*["'`][^./]/],
  ["document.write", /document\.write/],
  ["window.open", /window\.open/],
  ["<iframe", /<iframe/i],
  ["<script", /<script/i],
];

describe("the files that draw a plugin's data", () => {
  it("found them", () => expect(drawn.length).toBeGreaterThanOrEqual(3));
  it.each(BANNED)("never use %s", (_name, pattern) => {
    for (const file of drawn) expect(readFileSync(file, "utf8"), file).not.toMatch(pattern);
  });
});
