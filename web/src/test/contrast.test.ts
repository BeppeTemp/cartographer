import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * The WCAG 2.2 AA contrast audit, computed from tokens.css itself.
 *
 * A contrast check done once by hand and written in a doc is true for exactly
 * one version of the palette. This one reruns on every token change, for both
 * themes, so a tweak to a surface cannot quietly push a text colour under the
 * line. docs/testing.md records the thresholds and the pairs.
 *
 * Roles, not tokens, decide the threshold:
 *   - text (1.4.3): 4.5:1 on every surface it can sit on, hover included;
 *   - brand and severity colours, which appear as text in panels and cards:
 *     4.5:1 on the panel surfaces 0-2;
 *   - graphical objects (1.4.11): 3:1 -- graph node hues against the canvas,
 *     and the focus ring against the backdrop.
 * Edges and borders are deliberately not audited: they are decoration, never
 * the only carrier of a relationship (the inspector lists every link).
 */
const TOKENS = readFileSync(join(process.cwd(), "src/styles/tokens.css"), "utf8");

function block(selector: string): string {
  const start = TOKENS.indexOf(selector);
  return TOKENS.slice(start, TOKENS.indexOf("}", start));
}

function colours(css: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const match of css.matchAll(/(--[a-z0-9-]+):\s*(#[0-9a-fA-F]{6})\b/g)) {
    out[match[1]!] = match[2]!;
  }
  return out;
}

const dark = colours(block(":root {"));
const light = { ...dark, ...colours(block(':root[data-theme="light"]')) };

function luminance(hex: string): number {
  const channel = (offset: number) => {
    const c = parseInt(hex.slice(offset, offset + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * channel(1) + 0.7152 * channel(3) + 0.0722 * channel(5);
}

export function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}

const SURFACES = ["--surface-0", "--surface-1", "--surface-2", "--surface-3"];
const PANELS = ["--surface-0", "--surface-1", "--surface-2"];
const HUES = Array.from({ length: 12 }, (_, i) => `--hue-${i + 1}`);

interface Pair {
  fg: string;
  bg: string;
  min: number;
}

const pairs: Pair[] = [
  ...["--text-primary", "--text-secondary", "--text-muted"].flatMap((fg) =>
    SURFACES.map((bg) => ({ fg, bg, min: 4.5 })),
  ),
  ...["--primary", "--accent", "--sev-error", "--sev-warning", "--sev-info", "--sev-ok"].flatMap(
    (fg) => PANELS.map((bg) => ({ fg, bg, min: 4.5 })),
  ),
  { fg: "--text-on-primary", bg: "--primary", min: 4.5 },
  { fg: "--text-on-accent", bg: "--accent", min: 4.5 },
  ...HUES.map((fg) => ({ fg, bg: "--surface-0", min: 3 })),
  { fg: "--accent", bg: "--surface-0", min: 3 },
  { fg: "--graph-community-other", bg: "--surface-0", min: 3 },
];

describe("WCAG 2.2 AA contrast of the token palette", () => {
  for (const [theme, palette] of [
    ["dark", dark],
    ["light", light],
  ] as const) {
    it(`holds for every audited pair in the ${theme} theme`, () => {
      const failures: string[] = [];
      for (const { fg, bg, min } of pairs) {
        const a = palette[fg];
        const b = palette[bg];
        expect(a, `${theme}: ${fg} is not a hex colour token`).toBeDefined();
        expect(b, `${theme}: ${bg} is not a hex colour token`).toBeDefined();
        const ratio = contrast(a!, b!);
        if (ratio < min) failures.push(`${fg} on ${bg}: ${ratio.toFixed(2)} < ${min}`);
      }
      expect(failures).toEqual([]);
    });
  }

  it("computes the reference ratios WCAG publishes", () => {
    expect(contrast("#000000", "#ffffff")).toBeCloseTo(21, 5);
    expect(contrast("#777777", "#ffffff")).toBeCloseTo(4.48, 2);
  });
});
