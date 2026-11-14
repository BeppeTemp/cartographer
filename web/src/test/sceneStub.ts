/**
 * A LivingScene without WebGL, for jsdom: the graph view's own logic (data,
 * colours, selection, labels, fallbacks) runs; nothing is drawn. A test can
 * make it fail to start, as a browser without a context would:
 * `sceneStub.fail = true`. The calls a test asserts on are recorded in
 * `sceneStub.calls`; `sceneStub.reset()` clears them.
 */
export interface SetDataCall {
  positions: Map<string, { x?: number; y?: number; z?: number }>;
  replace: boolean;
  morph: boolean;
  restore: unknown;
  /** The ids a local settle moves alone (a cluster opening or closing), if any. */
  local?: string[];
  /** The open clusters' hulls it got. */
  hulls?: { id: string; members: string[] }[];
  /** The ids it got flagged as a cluster glyph. */
  glyphs: string[];
  /** The links it got, as `source>target`. */
  links: string[];
}

export const sceneStub = {
  fail: false,
  /** The callbacks the last on-screen scene was built with: a test clicks a node through them. */
  callbacks: null as { onSelect(id: string | null): void } | null,
  /** What the on-screen scene answers for a node's screen position and radius (default: off screen, 1). */
  view: {} as {
    project?(id: string): { x: number; y: number } | null;
    radius?: number;
  },
  /** The on-screen scene, so a test can run its frame listeners. */
  scene: null as { frame(): void } | null,
  frame() {
    this.scene?.frame();
  },
  created: 0,
  calls: {
    focus: [] as string[],
    unfocus: 0,
    colours: [] as string[][],
    /** Every `setData` of a visible scene: the ids and positions it got (as they were then) and its flags. */
    data: [] as SetDataCall[],
    /** Every `fitEverything`, with the duration asked. */
    fit: [] as number[],
    /** Every `followFit`, with how many `setData`s had been made by then. */
    follow: [] as { occluded: number; floor: number; after: number }[],
    /** Every `unfocus`'s argument. */
    unfocusArgs: [] as unknown[],
  },
  /** The offline scenes the video export made (D691), and what became of them. */
  offline: { created: 0, frames: [] as number[], disposed: 0, seeded: 0 },
  reset() {
    this.calls = { focus: [], unfocus: 0, colours: [], data: [], fit: [], follow: [], unfocusArgs: [] };
    this.offline = { created: 0, frames: [], disposed: 0, seeded: 0 };
    this.view = {};
  },
};

export class LivingScene {
  private readonly frameListeners = new Set<() => void>();
  onFrame(listener: () => void): () => void {
    this.frameListeners.add(listener);
    return () => this.frameListeners.delete(listener);
  }
  readonly canvas = document.createElement("canvas");
  private nodes: { id: string }[] = [];
  private links: { source: string; target: string }[] = [];
  private readonly offscreen: boolean;
  constructor(_container?: unknown, callbacks?: unknown, options?: { offscreen?: unknown }) {
    if (sceneStub.fail) throw new Error("stub: no WebGL");
    sceneStub.created++;
    this.offscreen = !!options?.offscreen;
    if (!this.offscreen) {
      sceneStub.callbacks = callbacks as typeof sceneStub.callbacks;
      sceneStub.scene = this;
    }
    if (this.offscreen) sceneStub.offline.created++;
  }
  renderFrame(ms: number): void {
    sceneStub.offline.frames.push(ms);
  }
  seedPositions(): void {
    sceneStub.offline.seeded++;
  }
  setData(
    nodes: { id: string; x?: number; y?: number; z?: number; cluster?: boolean }[],
    links: { source: string; target: string }[] = [],
    _warmup?: number,
    replace = false,
    opts: { morph?: boolean; restore?: unknown; local?: Set<string>; hulls?: { id: string; members: string[] }[] } = {},
  ): void {
    this.nodes = nodes;
    this.links = links;
    if (this.offscreen) return;
    sceneStub.calls.data.push({
      positions: new Map(nodes.map((n) => [n.id, { x: n.x, y: n.y, z: n.z }])),
      replace,
      morph: !!opts.morph,
      restore: opts.restore,
      local: opts.local ? [...opts.local] : undefined,
      hulls: opts.hulls,
      glyphs: nodes.filter((n) => n.cluster).map((n) => n.id),
      links: links.map((l) => `${l.source}>${l.target}`),
    });
  }
  setColours(colours: string[]): void {
    sceneStub.calls.colours.push(colours);
  }
  setSelection(): void {}
  sendSignals(): void {}
  setLive(): void {}
  /** After a focus the camera is somewhere else: a close-up, shifted beside the inspector. */
  private focused = false;
  focus(id: string, _occluded?: unknown, savePose?: () => void): void {
    savePose?.();
    this.focused = true;
    sceneStub.calls.focus.push(id);
  }
  centreIn(_occluded: unknown): void {}
  unfocus(pose?: unknown): void {
    sceneStub.calls.unfocus++;
    sceneStub.calls.unfocusArgs.push(pose);
  }
  pose() {
    return this.focused
      ? { position: { x: 9, y: 9, z: 9 }, lookAt: { x: 5, y: 5, z: 5 } }
      : { position: { x: 1, y: 2, z: 3 }, lookAt: { x: 0, y: 0, z: 0 } };
  }
  shiftPx(): number {
    return this.focused ? 200 : 0;
  }
  frameAll(): void {}
  fitEverything(_occluded?: number, ms?: number): void {
    sceneStub.calls.fit.push(ms ?? -1);
  }
  followFit(occluded = 0, floor = 0): void {
    sceneStub.calls.follow.push({ occluded, floor, after: sceneStub.calls.data.length });
  }
  stopFollowingFit(): void {}
  fitDistance(): number {
    return 100;
  }
  zoomBy(): void {}
  linksOf(): never[] {
    return [];
  }
  neighboursOf(id: string) {
    const ids = new Set(
      this.links.flatMap((l) => (l.source === id ? [l.target] : l.target === id ? [l.source] : [])),
    );
    return this.nodes.filter((n) => ids.has(n.id));
  }
  nodeById(id: string) {
    return this.nodes.find((n) => n.id === id);
  }
  project(node: { id: string }) {
    return sceneStub.view.project?.(node.id) ?? null;
  }
  radiusOf() {
    return sceneStub.view.radius ?? 1;
  }
  frame(): void {
    for (const listener of this.frameListeners) listener();
  }
  dispose(): void {
    if (this.offscreen) sceneStub.offline.disposed++;
  }
}
