import type { CrossEdge, ExternalEdges, GraphSnapshot } from "../api/types";

/** A written `kb:<name>/<id>` link target: the mount name, then the id. A
 *  `@server` qualifier is reserved and resolves nowhere, so it is not parsed
 *  into a target (the link stays text). */
export function parseCrossRef(target: string): { kb: string; id: string } | null {
  const m = /^kb:([A-Za-z0-9_-]+)\/(.+)$/.exec(target.trim());
  return m ? { kb: m[1]!, id: m[2]! } : null;
}

/** Whether the server resolved the link to this page for the caller. */
export function isResolvedCross(cross: readonly CrossEdge[] | undefined, kb: string, id: string): boolean {
  return !!cross?.some((e) => e.direction === "out" && e.kb === kb && e.id === id && e.state === "resolved");
}

/** The id of the stub node a page of another KB is drawn as. */
export function stubId(kb: string, id: string): string {
  return `kb:${kb}/${id}`;
}

/** The snapshot with one small stub node per resolved external edge of a drawn
 *  page. An edge in any other state is not drawn: nothing says it exists. */
export function withExternalStubs(snapshot: GraphSnapshot, external: ExternalEdges | null): GraphSnapshot {
  if (!external) return snapshot;
  const drawn = new Set(snapshot.nodes.map((n) => n.id));
  const stubs = new Map<string, { in: number; out: number }>();
  const edges = [...snapshot.edges];
  for (const e of external.edges) {
    if (!drawn.has(e.from) || (e.state !== undefined && e.state !== "resolved")) continue;
    const id = stubId(e.kb, e.id);
    const count = stubs.get(id) ?? { in: 0, out: 0 };
    if (e.direction === "out") count.in++;
    else count.out++;
    stubs.set(id, count);
    edges.push(e.direction === "out" ? { source: e.from, target: id } : { source: id, target: e.from });
  }
  if (stubs.size === 0) return snapshot;
  const nodes = [...snapshot.nodes];
  for (const [id, c] of stubs) nodes.push({ id, title: id, in_degree: c.in, out_degree: c.out });
  return { ...snapshot, nodes, edges };
}
