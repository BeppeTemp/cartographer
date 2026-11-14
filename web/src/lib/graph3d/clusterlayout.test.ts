import { forceLink, forceManyBody, forceSimulation, type Simulation } from "d3-force-3d";
import { describe, expect, it } from "vitest";
import { anchorGlyphs } from "../clusters";
import {
  centroid,
  configureClusterForces,
  radiusOf,
  type LayoutNode,
} from "./clusterlayout";
import { configureForces, drift, endpoint, seedPosition, springsOf, type SpringLink } from "./physics";

/** A deterministic generator: the same graph every run. */
function rng(seed: number) {
  let s = seed >>> 0;
  return () => {
    s = (Math.imul(s, 1664525) + 1013904223) >>> 0;
    return s / 2 ** 32;
  };
}

/** The folded overview of a KB of 22 clusters of about 245 concepts, as test-5000 has. */
function overview() {
  const r = rng(7);
  const clusters: LayoutNode[] = Array.from({ length: 22 }, (_, i) => ({
    id: `c:m/k${i}`,
    cluster: true,
    weight: 0.35 + 0.65 * Math.sqrt((238 + (i % 14)) / 251),
    ...seedPosition(`c:m/k${i}`, 22, 3),
  }));
  const links: SpringLink[] = [];
  for (let e = 0; e < 120; e++) {
    const a = Math.floor(r() * 22);
    const b = Math.floor(r() * 22);
    if (a === b) continue;
    for (let k = 0; k < 1 + Math.floor(r() * 6); k++) links.push({ source: clusters[a]!.id, target: clusters[b]!.id });
  }
  return { clusters, links };
}

function simulate(nodes: LayoutNode[], links: SpringLink[], open: { members: LayoutNode[] }[], ticks: number) {
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const springs = springsOf(links as never, byId as never);
  const sim = forceSimulation<LayoutNode>(nodes, 3)
    .force("link", forceLink<LayoutNode, SpringLink>(springs).id((n) => n.id))
    .force("charge", forceManyBody<LayoutNode>())
    .velocityDecay(0.38)
    .stop();
  configureForces((name, ...rest: unknown[]) => (rest.length ? sim.force(name, rest[0] as never) : sim.force(name)), drift(3), 3);
  configureClusterForces(sim as unknown as Simulation<LayoutNode>, springs, byId, nodes, open);
  sim.alpha(1);
  sim.tick(ticks);
  return sim;
}

describe("an opened cluster is the simulation's own motion (real forces, no WebGL)", () => {
  const { clusters, links } = overview();
  simulate(clusters, links, [], 300);
  const opened = clusters[5]!;
  const glyphAt = { x: opened.x!, y: opened.y!, z: opened.z! };
  const rest = clusters.filter((c) => c !== opened);
  const before = new Map(rest.map((c) => [c.id, { x: c.x!, y: c.y!, z: c.z! }]));
  // The 247 members start at the glyph (a few units apart), linked among themselves and each to a few
  // folded clusters, as a real unfold is; the simulation is reheated moderately and cools fast.
  const r = rng(11);
  const members: LayoutNode[] = Array.from({ length: 247 }, (_, i) => {
    const id = `m/pallet-${i}`;
    const s = seedPosition(id, 247, 3);
    return { id, weight: Math.sqrt(r()), x: glyphAt.x + s.x * 0.02, y: glyphAt.y + s.y * 0.02, z: glyphAt.z + s.z * 0.02 };
  });
  const memberLinks: SpringLink[] = [];
  members.forEach((m, i) => {
    for (const d of [1, 2, 3]) memberLinks.push({ source: m.id, target: members[(i + d) % members.length]!.id });
    for (let k = 0; k < 2; k++) memberLinks.push({ source: m.id, target: rest[Math.floor(r() * rest.length)]!.id });
  });
  // A scale for the bounds below: about the radius 247 members fill.
  const room = 24 + 10 * Math.cbrt(members.length);
  const nodes = [...rest, ...members];
  const others = links.filter((l) => endpoint(l.source) !== opened.id && endpoint(l.target) !== opened.id).map((l) => ({ source: endpoint(l.source), target: endpoint(l.target) }));
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const springs = springsOf([...others, ...memberLinks] as never, byId as never);
  const sim = forceSimulation<LayoutNode>(nodes, 3)
    .force("link", forceLink<LayoutNode, SpringLink>(springs).id((n) => n.id))
    .force("charge", forceManyBody<LayoutNode>())
    .velocityDecay(0.55)
    .stop();
  configureForces((name, ...rest2: unknown[]) => (rest2.length ? sim.force(name, rest2[0] as never) : sim.force(name)), drift(3), 3);
  configureClusterForces(sim as unknown as Simulation<LayoutNode>, springs, byId, nodes, [{ members }]);
  sim.alpha(0.3).alphaDecay(0.08);
  // How far the nodes move per tick as it cools: a falling curve, no wobble.
  const steps: number[] = [];
  for (let t = 0; t < 140; t++) {
    const prev = nodes.map((n) => [n.x!, n.y!, n.z!]);
    sim.tick();
    steps.push(nodes.reduce((sum, n, i) => sum + Math.hypot(n.x! - prev[i]![0]!, n.y! - prev[i]![1]!, n.z! - prev[i]![2]!), 0) / nodes.length);
  }
  const c = centroid(members);

  it("cools without wobble: the motion per tick falls and ends", () => {
    for (let i = 30; i < steps.length; i++) expect(steps[i]!).toBeLessThanOrEqual(steps[i - 1]! * 1.1 + 0.002);
    expect(steps.at(-1)!).toBeLessThan(steps[0]! * 0.1);
  });

  it("moves the rest of the graph only as much as the physics wants: no glyph is carried far", () => {
    const moved = rest.map((n) => Math.hypot(n.x! - before.get(n.id)!.x, n.y! - before.get(n.id)!.y, n.z! - before.get(n.id)!.z));
    // A glyph linked to the members is drawn in among them (they mix), never thrown across the picture.
    expect(Math.max(...moved)).toBeLessThan(room * 3);
    expect(moved.reduce((a, b) => a + b, 0) / moved.length).toBeLessThan(room * 1.5);
  });

  it("opens where its glyph was", () => {
    expect(Math.hypot(c.x - glyphAt.x, c.y - glyphAt.y, c.z - glyphAt.z)).toBeLessThan(room);
  });

  it("keeps air between the members' discs", () => {
    let touching = 0;
    for (const m of members) {
      const gap = Math.min(...members.filter((o) => o !== m).map((o) => Math.hypot(m.x! - o.x!, m.y! - o.y!, m.z! - o.z!) - radiusOf(m) - radiusOf(o)));
      if (gap < 0) touching++;
    }
    expect(touching / members.length).toBeLessThan(0.05);
  });
});

describe("the mixed overview keeps loose concepts among the glyphs, not in a ball of their own", () => {
  const r = rng(3);
  const maps = ["alpha", "beta", "gamma", "delta", "eps", "zeta"];
  const clusterList = Array.from({ length: 20 }, (_, i) => ({
    id: `c:m/g${i}`,
    label: `G${i}`,
    collection: maps[i % maps.length] as string,
    size: 240 - i,
    slot: 0,
    internal_edges: 0,
  }));
  const glyphs: LayoutNode[] = clusterList.map((c) => ({
    id: c.id,
    cluster: true,
    weight: 0.35 + 0.65 * Math.sqrt(c.size / 251),
    ...seedPosition(c.id, 20, 3),
  }));
  // 112 loose concepts of several Maps (two of them with no glyph), most with no link at all,
  // ten larger ones linked among themselves (a streak of their own), a few linked to a glyph.
  const looseInfo = Array.from({ length: 112 }, (_, i) => ({
    id: `m/loose-${i}`,
    collection: [...maps, "nomap", "other"][i % 8] as string,
  }));
  const loose: LayoutNode[] = looseInfo.map((n, i) => ({
    id: n.id,
    weight: i < 10 ? 0.7 + 0.3 * r() : r() * 0.3,
    ...seedPosition(n.id, 132, 3),
  }));
  const links: SpringLink[] = [];
  glyphs.forEach((g, i) => {
    for (let k = 0; k < 2; k++) links.push({ source: g.id, target: glyphs[(i + 1 + Math.floor(r() * 18)) % 20]!.id });
  });
  // A small linked community among the loose ones (as real ones are: dense, not a chain).
  for (let i = 0; i < 10; i++) for (const d of [1, 2, 3]) if (i + d < 10) links.push({ source: loose[i]!.id, target: loose[i + d]!.id });
  const toGlyph = [{ source: loose[20]!.id, target: glyphs[3]!.id }];
  links.push(...toGlyph);
  const anchors = anchorGlyphs(looseInfo, clusterList, toGlyph, new Set(), links.filter((l) => String(l.source).startsWith("m/") && String(l.target).startsWith("m/")) as { source: string; target: string }[]);
  loose.forEach((n) => (n.anchor = anchors.get(n.id)));
  simulate([...glyphs, ...loose], links, [], 300);
  const dist = (a: LayoutNode, b: LayoutNode) => Math.hypot(a.x! - b.x!, a.y! - b.y!, a.z! - b.z!);

  it("anchors every loose concept without a glyph link, to a glyph of its Map when there is one", () => {
    expect(anchors.has(loose[20]!.id)).toBe(false);
    // Every concept of a component with no glyph link, all to one glyph: 111 of the 112.
    expect(anchors.size).toBe(111);
    expect(new Set(loose.slice(0, 10).map((n) => anchors.get(n.id))).size).toBe(1);
    const byId = new Map(clusterList.map((c) => [c.id, c]));
    // (A component takes its dominant Map's glyph: the check is for the singles.)
    for (const n of looseInfo.slice(10).filter((x) => maps.includes(x.collection) && anchors.has(x.id))) {
      expect(byId.get(anchors.get(n.id)!)!.collection).toBe(n.collection);
    }
  });

  it("puts every loose concept near a glyph, and the whole layout in one envelope", () => {
    loose.forEach((n, i) => {
      const near = glyphs.reduce((best, g) => (dist(n, g) < dist(n, best) ? g : best));
      // A member of a small linked community hangs off the one tied to the glyph: its own radius more.
      const slack = i < 10 ? 40 : 6;
      expect(dist(n, near), `${n.id} is ${dist(n, near).toFixed(0)} from its nearest glyph`).toBeLessThanOrEqual(
        radiusOf(near) * 2.5 + 40 + slack,
      );
    });
    // One connected envelope: nodes within four radii (plus a wide margin: the glyphs are spread, not packed) of one another chain into one piece.
    const all = [...glyphs, ...loose];
    const parent = all.map((_, i) => i);
    const find = (i: number): number => (parent[i] === i ? i : (parent[i] = find(parent[i]!)));
    all.forEach((a, i) =>
      all.forEach((b, j) => {
        if (j > i && dist(a, b) <= (radiusOf(a) + radiusOf(b)) * 4 + 120) parent[find(i)] = find(j);
      }),
    );
    const parts = new Map<number, string[]>();
    all.forEach((n, i) => (parts.get(find(i)) ?? parts.set(find(i), []).get(find(i))!).push(n.id));
    expect([...parts.values()].map((p) => p.length), "pieces of the layout").toHaveLength(1);
  });
});
