import { readFileSync, readdirSync, statSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { MOTION_MS } from "../lib/theme";

// Resolved from the package root rather than from import.meta.url: under the
// jsdom environment import.meta.url is an http:// URL, whose pathname is not a
// filesystem path.
const SRC = join(process.cwd(), "src");
const TOKENS = join(SRC, "styles/tokens.css");

function walk(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walk(full, out);
    else out.push(full);
  }
  return out;
}

/**
 * The token contract, enforced rather than documented.
 *
 * A hard-coded colour or duration outside tokens.css is how a theme quietly
 * stops applying to one panel: the light theme redefines the custom property,
 * the literal does not move, and the defect only shows up for the people who
 * use that theme. The Sigma renderer is the one exception -- WebGL cannot read
 * a CSS custom property, so palette.ts resolves tokens to concrete values at
 * runtime with a documented fallback for a missing one.
 */
describe("design tokens", () => {
  const files = walk(SRC).filter((f) => /\.(ts|tsx|css)$/.test(f));

  it("declares every colour, duration and radius in tokens.css", () => {
    const tokens = readFileSync(TOKENS, "utf8");
    for (const name of [
      "--surface-0",
      "--text-primary",
      "--primary",
      "--accent",
      "--sev-error",
      "--motion-base",
      "--ease-out",
      "--radius-card",
      "--space-4",
    ]) {
      expect(tokens, `${name} must exist`).toContain(`${name}:`);
    }
  });

  it("redefines the whole palette for the light theme", () => {
    const tokens = readFileSync(TOKENS, "utf8");
    const light = tokens.slice(tokens.indexOf('[data-theme="light"]'));
    const dark = tokens.slice(0, tokens.indexOf('[data-theme="light"]'));
    const colourNames = [...dark.matchAll(/(--[a-z0-9-]+):\s*#[0-9a-f]{3,8}/g)].map((m) => m[1]!);
    expect(colourNames.length).toBeGreaterThan(20);
    for (const name of colourNames) {
      expect(light, `${name} has no light-theme value`).toContain(`${name}:`);
    }
  });

  it("uses no literal colour outside tokens.css", () => {
    const offenders: string[] = [];
    for (const file of files) {
      if (file.endsWith("styles/tokens.css")) continue;
      // Tests carry colour fixtures on purpose: they assert what the encoding
      // functions do with a palette, and a token indirection there would test
      // the indirection rather than the behaviour.
      if (file.includes("/test/")) continue;
      // palette.ts resolves tokens for the WebGL canvas and carries a
      // documented fallback; nothing else may.
      const allowFallback = /lib\/palette\.ts$/.test(file);
      const source = readFileSync(file, "utf8");
      source.split("\n").forEach((text, index) => {
        // &#9633; is an HTML entity, not a colour.
        if (!/(^|[^&])#[0-9a-fA-F]{3,8}\b/.test(text)) return;
        if (allowFallback && /\|\||fallback|\?\?/.test(text)) return;
        if (/0x[0-9a-fA-F]+/.test(text)) return;
        offenders.push(`${file.slice(SRC.length)}:${index + 1} ${text.trim()}`);
      });
    }
    expect(offenders, "literal colours belong in tokens.css").toEqual([]);
  });

  // Every motion declaration, multi-line ones included, from the CSS outside
  // tokens.css: the property and its value with the token references taken
  // out, so what is left is what the file wrote literally.
  const motionDeclarations = () =>
    files
      .filter((f) => f.endsWith(".css") && !/styles\/tokens\.css$/.test(f))
      .flatMap((file) =>
        readFileSync(file, "utf8")
          .replace(/\/\*[\s\S]*?\*\//g, "")
          .split(/[;{}]/)
          .map((d) => d.trim().replace(/\s+/g, " "))
          .filter((d) => /^(transition|animation)(-[a-z-]+)?:/.test(d))
          .map((d) => ({
            where: `${file.slice(SRC.length)} ${d}`,
            literal: d.slice(d.indexOf(":") + 1).replace(/var\(--[a-z0-9-]+(,[^)]*)?\)/g, ""),
          })),
      );

  it("uses no literal duration or delay outside tokens.css", () => {
    // 0s is not a duration: it is how a visibility change waits for a fade.
    const offenders = motionDeclarations()
      .filter((d) => [...d.literal.matchAll(/(?<![\w.-])(\d*\.?\d+)m?s\b/g)].some((m) => Number(m[1]) !== 0))
      .map((d) => d.where);
    expect(offenders, "durations belong in tokens.css (--motion-*)").toEqual([]);
  });

  it("uses no literal easing outside tokens.css", () => {
    // linear only qualifies a 0s step, where there is no curve to choose.
    const offenders = motionDeclarations()
      .filter((d) => /\b(ease(-in|-out|-in-out)?|linear|cubic-bezier|steps)\b/.test(d.literal.replace(/\b0s linear\b/g, "")))
      .map((d) => d.where);
    expect(offenders, "easings belong in tokens.css (--ease-*)").toEqual([]);
  });

  it("times JavaScript motion on the same scale as the CSS", () => {
    // MOTION_MS is what motionMs falls back to without the stylesheet, and
    // what a video export uses: it must say what tokens.css says.
    const tokens = readFileSync(TOKENS, "utf8");
    const root = tokens.slice(0, tokens.indexOf('[data-theme="light"]'));
    for (const [name, ms] of Object.entries(MOTION_MS)) {
      expect(root, `${name} in tokens.css`).toContain(`${name}: ${ms}ms;`);
    }
  });

  it("takes graph hues 1-6 from the brand's graph categories", () => {
    // docs/brand/cartographer.tokens.json is the brand's source (D232); the
    // Atlas maps it onto its own names (D233). This is the one place the two
    // could drift apart without anything else noticing.
    const brand = JSON.parse(
      readFileSync(join(process.cwd(), "../docs/brand/cartographer.tokens.json"), "utf8"),
    ) as { graphCategories: { light: string[]; dark: string[] } };
    const tokens = readFileSync(TOKENS, "utf8");
    const split = tokens.indexOf('[data-theme="light"]');
    const hue = (css: string, i: number) => css.match(new RegExp(`--hue-${i}:\\s*(#[0-9a-fA-F]{6})`))?.[1];
    for (const [theme, css] of [
      ["dark", tokens.slice(0, split)],
      ["light", tokens.slice(split)],
    ] as const) {
      brand.graphCategories[theme].forEach((value, index) => {
        expect(hue(css, index + 1)?.toLowerCase(), `${theme} --hue-${index + 1}`).toBe(value.toLowerCase());
      });
    }
  });

  it("collapses every animation under prefers-reduced-motion", () => {
    const tokens = readFileSync(TOKENS, "utf8");
    const block = tokens.slice(tokens.indexOf("prefers-reduced-motion"));
    expect(block).toContain("--motion-base: 0.01ms");
    expect(block).toContain("--motion-deliberate: 0.01ms");
    expect(block).toContain("animation-duration: 0.01ms !important");
    expect(block).toContain("transition-duration: 0.01ms !important");
  });
});
