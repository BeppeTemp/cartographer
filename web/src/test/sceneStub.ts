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
}

export const sceneStub = {
  fail: false,
  created: 0,
  calls: {
    focus: [] as string[],
    unfocus: 0,
    colours: [] as string[][],
    /** Every `setData` of a visible scene: the ids and positions it got (as they were then) and its flags. */
    data: [] as SetDataCall[],
    /** Every `fitEverything`, with the duration asked. */
    fit: [] as number[],
    /** Every `unfocus`'s argument. */
    unfocusArgs: [] as unknown[],
  },
  /** The offline scenes the video export made (D691), and what became of them. */
  offline: { created: 0, frames: [] as number[], disposed: 0, seeded: 0 },
  reset() {
    this.calls = { focus: [], unfocus: 0, colours: [], data: [], fit: [], unfocusArgs: [] };
    this.offline = { created: 0, frames: [], disposed: 0, seeded: 0 };
  },
};

export class LivingScene {
  readonly frameListeners = new Set<() => void>();
  readonly canvas = document.createElement("canvas");
  private nodes: { id: string }[] = [];
  private links: { source: string; target: string }[] = [];
  private readonly offscreen: boolean;
  constructor(_container?: unknown, _callbacks?: unknown, options?: { offscreen?: unknown }) {
    if (sceneStub.fail) throw new Error("stub: no WebGL");
    sceneStub.created++;
    this.offscreen = !!options?.offscreen;
    if (this.offscreen) sceneStub.offline.created++;
  }
  renderFrame(ms: number): void {
    sceneStub.offline.frames.push(ms);
  }
  seedPositions(): void {
    sceneStub.offline.seeded++;
  }
  setData(
    nodes: { id: string; x?: number; y?: number; z?: number }[],
    links: { source: string; target: string }[] = [],
    _warmup?: number,
    replace = false,
    opts: { morph?: boolean; restore?: unknown } = {},
  ): void {
    this.nodes = nodes;
    this.links = links;
    if (this.offscreen) return;
    sceneStub.calls.data.push({
      positions: new Map(nodes.map((n) => [n.id, { x: n.x, y: n.y, z: n.z }])),
      replace,
      morph: !!opts.morph,
      restore: opts.restore,
    });
  }
  setColours(colours: string[]): void {
    sceneStub.calls.colours.push(colours);
  }
  setSelection(): void {}
  sendSignals(): void {}
  setLive(): void {}
  focus(id: string, _occluded?: unknown, savePose?: () => void): void {
    savePose?.();
    sceneStub.calls.focus.push(id);
  }
  unfocus(pose?: unknown): void {
    sceneStub.calls.unfocus++;
    sceneStub.calls.unfocusArgs.push(pose);
  }
  pose() {
    return { position: { x: 1, y: 2, z: 3 }, lookAt: { x: 0, y: 0, z: 0 } };
  }
  shiftPx(): number {
    return 0;
  }
  frameAll(): void {}
  fitEverything(_occluded?: number, ms?: number): void {
    sceneStub.calls.fit.push(ms ?? -1);
  }
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
  project() {
    return null;
  }
  radiusOf() {
    return 1;
  }
  dispose(): void {
    if (this.offscreen) sceneStub.offline.disposed++;
  }
}
