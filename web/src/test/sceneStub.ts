/**
 * A LivingScene without WebGL, for jsdom: the graph view's own logic (data,
 * colours, selection, labels, fallbacks) runs; nothing is drawn. A test can
 * make it fail to start, as a browser without a context would:
 * `sceneStub.fail = true`.
 */
export const sceneStub = { fail: false, created: 0 };

export class LivingScene {
  readonly frameListeners = new Set<() => void>();
  private nodes: { id: string }[] = [];
  constructor() {
    if (sceneStub.fail) throw new Error("stub: no WebGL");
    sceneStub.created++;
  }
  setData(nodes: { id: string }[]): void {
    this.nodes = nodes;
  }
  setColours(): void {}
  setSelection(): void {}
  sendSignals(): void {}
  setLive(): void {}
  focus(): void {}
  unfocus(): void {}
  pose() {
    return { position: { x: 0, y: 0, z: 0 }, lookAt: { x: 0, y: 0, z: 0 } };
  }
  frameAll(): void {}
  refitIfUntouched(): void {}
  zoomBy(): void {}
  linksOf(): never[] {
    return [];
  }
  neighboursOf(): never[] {
    return [];
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
