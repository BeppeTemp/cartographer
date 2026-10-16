import { describe, expect, it } from "vitest";
import { withPendingSources } from "../components/Work";

const base = {
  total: 1,
  open_concepts: 1,
  by_status: { open: 1 },
  by_map: { sources: 1 },
  open_items: 0,
  entries: [{ id: "sources/a", title: "A", map: "sources", status: "open", open_phase: true, stale: false, items: [] }],
};

describe("withPendingSources", () => {
  it("adds each pending source once, as a 'to ingest' entry, skipping one the work list already has", () => {
    const out = withPendingSources(base, [
      { id: "sources/a", ingest_status: "pending", cited_by: 0, cited_by_ids: [] },
      { id: "sources/b", title: "B", ingest_status: "pending", cited_by: 0, cited_by_ids: [] },
      { id: "sources/c", ingest_status: "ingested", cited_by: 1, cited_by_ids: ["x/y"] },
    ]);
    expect(out.entries.map((e) => [e.id, e.status])).toEqual([
      ["sources/a", "open"],
      ["sources/b", "to ingest"],
    ]);
    expect(out.total).toBe(2);
    expect(out.by_status["to ingest"]).toBe(1);
    expect(out.by_map.sources).toBe(2);
  });
});
