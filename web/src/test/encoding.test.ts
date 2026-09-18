import { describe, expect, it } from "vitest";
import {
  RENDERABLE_EDGE_TYPES,
  RENDERABLE_NODE_TYPES,
  edgeAppearance,
  nodeAppearance,
  withAlpha,
  type NodeInput,
  type Palette,
} from "../lib/encoding";

const palette: Palette = {
  accent: "#f5b544",
  severityError: "#f87171",
  severityWarning: "#fbbf24",
  edge: "#24384f",
  edgeActive: "#2dd4bf",
};

function node(overrides: Partial<NodeInput> = {}): NodeInput {
  return {
    id: "infra/a",
    baseSize: 8,
    collectionColor: "#2dd4bf",
    expanded: false,
    entry: 1,
    hiddenByFilter: false,
    focus: null,
    isNeighbourOfFocus: false,
    ...overrides,
  };
}

/**
 * The regression this file exists for: Sigma throws "could not find a suitable
 * program for node type" for any type it has no program registered for, and a
 * throw inside the renderer takes the whole page down to a blank background.
 * Stock Sigma ships one node program and two edge programs. Emitting anything
 * else means shipping another dependency, so the appearance functions may
 * never do it.
 */
describe("the graph never emits a type stock Sigma cannot render", () => {
  it("uses only renderable node types, whatever the node is", () => {
    const cases: NodeInput[] = [
      node(),
      node({ expanded: true }),
      node({ expanded: true, severity: "error" }),
      node({ severity: "warning" }),
      node({ focus: "infra/a" }),
      node({ focus: "other", isNeighbourOfFocus: true }),
      node({ focus: "other", isNeighbourOfFocus: false }),
      node({ hiddenByFilter: true }),
      node({ entry: 0 }),
      node({ entry: 0.5 }),
    ];
    for (const input of cases) {
      const appearance = nodeAppearance(input, palette);
      expect(RENDERABLE_NODE_TYPES).toContain(appearance.type);
    }
  });

  it("uses only renderable edge types", () => {
    for (const edgesVisible of [true, false]) {
      for (const focus of [null, "infra/a", "other"]) {
        const appearance = edgeAppearance(
          { source: "infra/a", target: "infra/b", hiddenByFilter: false, edgesVisible, focus },
          palette,
        );
        expect(RENDERABLE_EDGE_TYPES).toContain(appearance.type);
      }
    }
  });
});

describe("node appearance", () => {
  it("hides a node the filters exclude, and one that has not arrived", () => {
    expect(nodeAppearance(node({ hiddenByFilter: true }), palette).hidden).toBe(true);
    expect(nodeAppearance(node({ entry: 0 }), palette).hidden).toBe(true);
    expect(nodeAppearance(node(), palette).hidden).toBe(false);
  });

  it("grows an expanded concept, since no shape channel is available", () => {
    const flat = nodeAppearance(node(), palette).size;
    const expanded = nodeAppearance(node({ expanded: true }), palette).size;
    expect(expanded).toBeGreaterThan(flat);
  });

  it("tints by lint severity, with error winning over the collection hue", () => {
    expect(nodeAppearance(node({ severity: "error" }), palette).color).toBe(palette.severityError);
    expect(nodeAppearance(node({ severity: "warning" }), palette).color).toBe(
      palette.severityWarning,
    );
    expect(nodeAppearance(node(), palette).color).toBe("#2dd4bf");
  });

  it("marks the focused node and lifts it above the rest", () => {
    const focused = nodeAppearance(node({ focus: "infra/a" }), palette);
    expect(focused.color).toBe(palette.accent);
    expect(focused.highlighted).toBe(true);
    expect(focused.zIndex).toBeGreaterThan(0);
  });

  it("dims an unrelated node rather than hiding it", () => {
    const dimmed = nodeAppearance(node({ focus: "other" }), palette);
    expect(dimmed.hidden).toBe(false);
    expect(dimmed.color).toContain("rgba");
    expect(dimmed.label).toBe("");
  });

  it("labels a node by its last path segment", () => {
    expect(nodeAppearance(node({ id: "infra/monitoring/dashboards" }), palette).label).toBe(
      "dashboards",
    );
    expect(nodeAppearance(node({ id: "loose" }), palette).label).toBe("loose");
  });

  it("grows a node smoothly as it arrives", () => {
    const early = nodeAppearance(node({ entry: 0.2 }), palette).size;
    const late = nodeAppearance(node({ entry: 1 }), palette).size;
    expect(early).toBeGreaterThan(0);
    expect(early).toBeLessThan(late);
  });
});

describe("edge appearance", () => {
  it("holds edges back until most nodes have arrived", () => {
    expect(
      edgeAppearance(
        { source: "a", target: "b", hiddenByFilter: false, edgesVisible: false, focus: null },
        palette,
      ).hidden,
    ).toBe(true);
  });

  it("makes the focused node's edges explicit and dims the rest", () => {
    const active = edgeAppearance(
      { source: "a", target: "b", hiddenByFilter: false, edgesVisible: true, focus: "a" },
      palette,
    );
    expect(active.color).toBe(palette.edgeActive);
    expect(active.size).toBeGreaterThan(1);

    const other = edgeAppearance(
      { source: "c", target: "d", hiddenByFilter: false, edgesVisible: true, focus: "a" },
      palette,
    );
    expect(other.color).toContain("rgba");
  });

  it("hides an edge whose endpoint the filters removed", () => {
    expect(
      edgeAppearance(
        { source: "a", target: "b", hiddenByFilter: true, edgesVisible: true, focus: null },
        palette,
      ).hidden,
    ).toBe(true);
  });
});

describe("withAlpha", () => {
  it("converts hex to rgba and leaves anything else alone", () => {
    expect(withAlpha("#2dd4bf", 0.25)).toBe("rgba(45, 212, 191, 0.25)");
    expect(withAlpha("#abc", 0.5)).toBe("rgba(170, 187, 204, 0.5)");
    expect(withAlpha("rebeccapurple", 0.5)).toBe("rebeccapurple");
    expect(withAlpha("", 0.5)).toBe("");
  });
});
