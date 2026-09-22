import { vi } from "vitest";
import type { GraphSnapshot, LintReport, Overview } from "../api/types";

/** Shared API fixtures for the shell-level tests. */
export const overview: Overview = {
  collections: [
    { name: "infra", title: "Infrastructure", kind: "map", concepts: 2, expanded_concepts: 0 },
    { name: "notes", kind: "journal", concepts: 1, expanded_concepts: 0 },
  ],
  concepts: { total: 3, by_type: { Service: 2, Note: 1 }, by_status: { active: 2 } },
  lint: { total: 4, by_severity: { error: 1, warning: 3 }, by_check: { broken_link: 1 } },
};

export const graph: GraphSnapshot = {
  nodes: [
    { id: "infra/a", collection: "infra", type: "Service", status: "active", in_degree: 1, out_degree: 1 },
    { id: "infra/b", collection: "infra", type: "Service", status: "active", in_degree: 1, out_degree: 0 },
    { id: "notes/c", collection: "notes", type: "Note", in_degree: 0, out_degree: 1 },
  ],
  edges: [
    { source: "infra/a", target: "infra/b" },
    { source: "notes/c", target: "infra/a" },
  ],
  total_nodes: 3,
  total_edges: 2,
  limit: 2000,
};

export const lint: LintReport = {
  findings: [
    { path: "infra/a.md", concept: "infra/a", check: "broken_link", severity: "error", message: "missing target" },
  ],
  count: 1,
  total: 4,
  by_severity: { error: 1, warning: 3 },
  by_check: { broken_link: 1 },
  severity_min: "info",
};

export function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

/** Routes a fetch to the right fixture, so a missing route fails loudly here
 *  rather than as an unexplained empty panel. */
export function stubApi(overrides: Record<string, () => Response> = {}) {
  const fetchMock = vi.fn(async (url: string) => {
    for (const [fragment, make] of Object.entries(overrides)) {
      if (url.includes(fragment)) return make();
    }
    if (url.includes("/kbs/") && url.includes("/overview")) return json(overview);
    if (url.includes("/kbs/") && url.includes("/graph")) return json(graph);
    if (url.includes("/kbs/") && url.includes("/lint")) return json(lint);
    if (url.includes("/kbs/") && url.includes("/concept")) {
      return json({
        id: "infra/a",
        title: "Alpha",
        collection: "infra",
        frontmatter: { type: "Service", status: "active" },
        body: "# Alpha\n\nA service.",
        body_bytes: 20,
        outline: [{ level: 1, title: "Alpha", bytes: 20 }],
        content_hash: "abc123",
        outbound: ["infra/b"],
        inbound: ["notes/c"],
        broken: [],
      });
    }
    if (url.endsWith("/kbs")) return json({ kbs: [{ name: "homelab", status: "normal", ready: true }] });
    throw new Error(`unstubbed request: ${url}`);
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}


/**
 * generateSnapshot builds a deterministic synthetic KB graph: `maps`
 * collections, each a loose cluster with a few hubs, plus cross-links between
 * clusters. It is the 2,000-node fixture of the performance budget
 * (docs/testing.md) and has the shape real KBs have -- clustered, hub-heavy,
 * sparse -- which a uniform random graph does not.
 */
export function generateSnapshot(nodes: number, maps = 12, seed = 1): GraphSnapshot {
  let state = seed >>> 0;
  const rand = () => {
    state = (Math.imul(state, 1664525) + 1013904223) >>> 0;
    return state / 4294967296;
  };
  const ids: string[] = [];
  const mapOf: number[] = [];
  for (let i = 0; i < nodes; i++) {
    const map = i % maps;
    ids.push(`map-${String(map).padStart(2, "0")}/concept-${String(i).padStart(5, "0")}`);
    mapOf.push(map);
  }
  const edges: GraphSnapshot["edges"] = [];
  const seen = new Set<string>();
  const add = (a: number, b: number) => {
    if (a === b) return;
    const key = `${a}>${b}`;
    if (seen.has(key)) return;
    seen.add(key);
    edges.push({ source: ids[a]!, target: ids[b]! });
  };
  for (let i = 0; i < nodes; i++) {
    const links = 1 + Math.floor(rand() * 3);
    for (let k = 0; k < links; k++) {
      // Mostly inside the Map, towards its first members (the hubs).
      const sameMap = rand() < 0.85;
      const map = sameMap ? mapOf[i]! : Math.floor(rand() * maps);
      const hubBias = Math.floor(rand() ** 2 * (nodes / maps));
      add(i, Math.min(map + hubBias * maps, nodes - 1));
    }
  }
  edges.sort((a, b) => (a.source + a.target < b.source + b.target ? -1 : 1));
  const degree = new Map<string, [number, number]>();
  for (const id of ids) degree.set(id, [0, 0]);
  for (const edge of edges) {
    degree.get(edge.source)![1]++;
    degree.get(edge.target)![0]++;
  }
  return {
    nodes: [...ids].sort().map((id) => ({
      id,
      collection: id.split("/")[0],
      type: "Entity",
      status: "active",
      in_degree: degree.get(id)![0],
      out_degree: degree.get(id)![1],
    })),
    edges,
    total_nodes: nodes,
    total_edges: edges.length,
    limit: 2000,
  };
}
