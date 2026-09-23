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
  reset() {
    this.calls = { focus: [], unfocus: 0, colours: [] };
  },
};

export class LivingScene {
  readonly frameListeners = new Set<() => void>();
  private nodes: { id: string }[] = [];
  private links: { source: string; target: string }[] = [];
  constructor() {
    if (sceneStub.fail) throw new Error("stub: no WebGL");
    sceneStub.created++;
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
  refitIfUntouched(): void {}
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
  dispose(): void {}
}
