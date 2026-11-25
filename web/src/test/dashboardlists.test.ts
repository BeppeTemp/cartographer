import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { DASHBOARD_ACCENTS, DASHBOARD_ICONS } from "../lib/dashboard";
import { isIconName } from "../components/Icon";

// Resolved from the package root: under jsdom import.meta.url is not a path.
const GO = readFileSync(join(process.cwd(), "../internal/kb/dashboard.go"), "utf8");

/** The string list a `name = []string{...}` line of dashboard.go declares. */
function goList(name: string): string[] {
  const m = new RegExp(`\\b${name}\\s*=\\s*\\[\\]string\\{([^}]*)\\}`).exec(GO);
  if (!m) throw new Error(`${name} not found in internal/kb/dashboard.go`);
  return [...m[1]!.matchAll(/"([^"]+)"/g)].map((x) => x[1]!);
}

describe("dashboard icon and accent lists", () => {
  // AC-kbdash-19: the Atlas lists equal the Go lists.
  it("equal the Go lists", () => {
    expect([...DASHBOARD_ICONS]).toEqual(goList("dashboardIcons"));
    expect([...DASHBOARD_ACCENTS]).toEqual(goList("dashboardAccents"));
  });
  it("name icons the Atlas draws", () => {
    for (const icon of DASHBOARD_ICONS) expect(isIconName(icon)).toBe(true);
  });
});
