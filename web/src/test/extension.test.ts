import { describe, expect, it } from "vitest";
import { formatCell, shape, valueAt, viewPath } from "../lib/extension";
import type { ExtensionView } from "../api/types";

const view = (over: Partial<ExtensionView> = {}): ExtensionView => ({
  plugin: "p",
  name: "v",
  title: "V",
  description: "",
  icon: "list",
  scope: "kb",
  source: { route: "/api/v1/kbs/{kb}/sources", query: { status: "{kb}-x" }, items: "sources" },
  renderer: "table",
  columns: [{ key: "id", label: "ID", format: "text" }],
  limit: 2,
  ...over,
});

describe("formatCell is total", () => {
  it.each([null, undefined, 3, true, {}, [], { a: 1 }, "<img src=x onerror=alert(1)>", "javascript:alert(1)"])(
    "draws %j as text under every format",
    (value) => {
      for (const f of ["text", "number", "date", "relative", "badge", "concept", "nonsense"]) {
        expect(typeof formatCell(value, f).text).toBe("string");
      }
    },
  );
  it("formats numbers, dates and relative times", () => {
    expect(formatCell(1234567, "number").text).toBe("1,234,567");
    expect(formatCell("n/a", "number").text).toBe("n/a");
    expect(formatCell("2026-11-08T10:00:00Z", "date").text).toBe("2026-11-08");
    const now = Date.parse("2026-11-08T12:00:00Z");
    expect(formatCell("2026-11-08T10:00:00Z", "relative", now).text).toBe("2h ago");
  });
  it("tones a badge from four words only and links only a concept string", () => {
    expect(formatCell("WARN", "badge").tone).toBe("warning");
    expect(formatCell("whatever", "badge").tone).toBe("neutral");
    expect(formatCell("infra/a", "concept").concept).toBe("infra/a");
    expect(formatCell(5, "concept").concept).toBeUndefined();
    expect(formatCell("https://example.com", "text").concept).toBeUndefined();
  });
  it("caps a long value", () => {
    expect(formatCell("x".repeat(1000), "text").text.length).toBeLessThan(400);
  });
});

describe("a view's request and shape", () => {
  it("fills {kb} in the route and the query, and needs a KB for a kb view", () => {
    expect(viewPath(view(), "kb a")).toBe("/api/v1/kbs/kb%20a/sources?status=kb+a-x");
    expect(viewPath(view(), null)).toBeNull();
    expect(viewPath(view({ scope: "global", source: { route: "/api/v1/kbs" } }), null)).toBe("/api/v1/kbs");
  });
  it("follows a dotted path without touching the prototype", () => {
    expect(valueAt({ a: { b: 2 } }, "a.b")).toBe(2);
    expect(valueAt({ a: 1 }, "__proto__")).toBeUndefined();
    expect(valueAt([1], "length")).toBeUndefined();
  });
  it("caps rows at the limit and refuses the wrong shape", () => {
    const rows = shape(view(), { sources: [1, 2, 3, 4, 5] });
    expect(rows).toEqual({ kind: "rows", rows: [1, 2], more: 3 });
    expect(shape(view(), { sources: "x" })).toBeNull();
    expect(shape(view({ renderer: "stats" }), { sources: [] })).toBeNull();
    expect(shape(view({ renderer: "stats", source: { route: "/api/v1/x", items: "s" } }), { s: { a: 1 } })?.kind).toBe("object");
  });
});
