/**
 * A LivingScene without WebGL, for jsdom: the graph view's own logic (data,
 * colours, selection, labels, fallbacks) runs; nothing is drawn. A test can
 * make it fail to start, as a browser without a context would:
 * `sceneStub.fail = true`. The calls a test asserts on are recorded in
 * `sceneStub.calls`; `sceneStub.reset()` clears them.
 */
export const sceneStub = {
  fail: false,
  created: 0,
  calls: { focus: [] as string[], unfocus: 0, colours: [] as string[][] },
  /** The offline scenes the video export made (D691), and what became of them. */
  offline: { created: 0, frames: [] as number[], disposed: 0, seeded: 0 },
  reset() {
    this.calls = { focus: [], unfocus: 0, colours: [] };
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
  setData(nodes: { id: string }[], links: { source: string; target: string }[] = []): void {
    this.nodes = nodes;
    this.links = links;
  }
  setColours(colours: string[]): void {
    sceneStub.calls.colours.push(colours);
  }
  setSelection(): void {}
  sendSignals(): void {}
  setLive(): void {}
  focus(id: string): void {
    sceneStub.calls.focus.push(id);
  }
  unfocus(): void {
    sceneStub.calls.unfocus++;
  }
  pose() {
    return { position: { x: 0, y: 0, z: 0 }, lookAt: { x: 0, y: 0, z: 0 } };
  }
  frameAll(): void {}
  fitEverything(): void {}
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
