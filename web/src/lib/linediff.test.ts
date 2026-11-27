import { describe, expect, it } from "vitest";
import { diffLines, hunks } from "./linediff";

describe("diffLines", () => {
  it("marks an added, a removed and a kept line", () => {
    const d = diffLines("a\nb\nc\n", "a\nc\nd\n")!;
    expect(d.map((l) => `${l.kind[0]}${l.text}`)).toEqual(["sa", "db", "sc", "ad"]);
  });
  it("treats an empty before as all added", () => {
    expect(diffLines("", "x\ny")!.every((l) => l.kind === "add")).toBe(true);
  });
  it("finds nothing between equal texts", () => {
    expect(diffLines("x\ny", "x\ny")!.every((l) => l.kind === "same")).toBe(true);
  });
});

describe("diffLines limits", () => {
  const lines = (n: number, p: string) => Array.from({ length: n }, (_, i) => `${p}${i}`).join("\n");
  it("gives up on texts with too many edits, or too many lines", () => {
    expect(diffLines(lines(600, "a"), lines(600, "b"))).toBeNull();
    expect(diffLines(lines(15000, "a"), lines(15000, "a") + "\nx")).toBeNull();
    expect(diffLines(lines(600, "a"), lines(600, "b"), 5000)).not.toBeNull();
  });
  it("still diffs a small change in a long text", () => {
    const a = lines(5000, "l");
    expect(diffLines(a, a.replace("l2500", "X"))).not.toBeNull();
  });
});

describe("hunks", () => {
  it("folds the unchanged lines far from a change", () => {
    const before = Array.from({ length: 20 }, (_, i) => `l${i}`).join("\n");
    const after = before.replace("l10", "L10");
    const h = hunks(diffLines(before, after)!, 2);
    expect(h).toHaveLength(1);
    expect(h[0]!.skipped).toBe(8);
    expect(h[0]!.lines.map((l) => l.text)).toEqual(["l8", "l9", "l10", "L10", "l11", "l12"]);
  });
});
