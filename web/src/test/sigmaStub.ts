/**
 * Sigma stand-in for jsdom, which has no WebGL. What is under test with it is
 * the shell, the data flow and the states, not the renderer. Used as
 * `vi.mock("sigma", () => import("./sigmaStub"))`.
 */
export default class SigmaStub {
  on() {}
  kill() {}
  refresh() {}
  setSetting() {}
  viewportToGraph() {
    return { x: 0, y: 0 };
  }
  getGraph() {
    return { hasNode: () => false };
  }
  getNodeDisplayData() {
    return { x: 0, y: 0 };
  }
  getCamera() {
    return { ratio: 1, animate: () => {}, setState: () => {} };
  }
  getMouseCaptor() {
    return { on: () => {} };
  }
}
