import type { SceneMode } from "../lib/graph3d/scene";

/**
 * A LivingScene without WebGL, for jsdom: the graph view's own logic (data,
 * colours, selection, labels, fallbacks) runs; nothing is drawn. A test can
 * make a mode fail to start, as a browser without a context would:
 * `sceneStub.failModes.add("3d")`.
 */
export const sceneStub = { failModes: new Set<SceneMode>(), created: [] as SceneMode[] };

export class LivingScene {
  readonly frameListeners = new Set<() => void>();
  readonly mode: SceneMode;
  private nodes: { id: string }[] = [];
  constructor(_container: HTMLElement, _callbacks: unknown, options: { mode?: SceneMode }) {
    this.mode = options.mode ?? "3d";
    if (sceneStub.failModes.has(this.mode)) throw new Error(`stub: no WebGL for ${this.mode}`);
    sceneStub.created.push(this.mode);
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
  relax(): void {}
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
