import { describe, expect, it } from "vitest";
import {
  DIM_ALPHA,
  NEIGHBOUR_LABEL_LIMIT,
  RENDERABLE_EDGE_TYPES,
  RENDERABLE_NODE_TYPES,
  edgeAppearance,
  nodeAppearance,
  fade,
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
    hueColor: "#2dd4bf",
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
    // The outline recedes with the fill: an opaque paper-white outline around
    // a faint fill reads as a hole in the light theme.
    expect(dimmed.borderColor).toBe(dimmed.color);
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

describe("fade", () => {
  it("mixes towards the canvas into an opaque colour", () => {
    // Opaque on purpose: an rgba() colour is added to the page by Sigma's
    // unpremultiplied blending and turns white on a light canvas.
    expect(fade("#000000", 0.25, "#ffffff")).toBe("#bfbfbf");
    expect(fade("#2dd4bf", 1, "#000000")).toBe("#2dd4bf");
    expect(fade("#abc", 0, "#123456")).toBe("#123456");
  });

  it("falls back to rgba without a canvas and leaves non-hex alone", () => {
    expect(fade("#2dd4bf", 0.25)).toBe("rgba(45, 212, 191, 0.25)");
    expect(fade("rebeccapurple", 0.5, "#ffffff")).toBe("rebeccapurple");
    expect(fade("", 0.5)).toBe("");
  });
});

describe("selection context", () => {
  it("dims unrelated nodes to the specified 0.25", () => {
    expect(DIM_ALPHA).toBe(0.25);
    expect(nodeAppearance(node({ focus: "other" }), palette).color).toBe(
      fade("#2dd4bf", 0.25),
    );
  });

  it("names the focused node's neighbours, unless it is a hub", () => {
    const few = nodeAppearance(
      node({ focus: "other", isNeighbourOfFocus: true, focusDegree: 3 }),
      palette,
    );
    expect(few.forceLabel).toBe(true);
    const many = nodeAppearance(
      node({ focus: "other", isNeighbourOfFocus: true, focusDegree: NEIGHBOUR_LABEL_LIMIT + 1 }),
      palette,
    );
    expect(many.forceLabel).toBe(false);
    expect(nodeAppearance(node(), palette).forceLabel).toBe(false);
  });

  it("brightens the focused node's edges and dims the others to 0.25", () => {
    const base = { hiddenByFilter: false, edgesVisible: true, focus: "a" };
    const touching = edgeAppearance({ ...base, source: "x", target: "a" }, palette);
    expect(touching.color).toBe(palette.edgeActive);
    expect(touching.zIndex).toBeGreaterThan(0);
    const other = edgeAppearance({ ...base, source: "c", target: "d", groupColor: "#a78bfa" }, palette);
    expect(other.color).toBe(fade(palette.edge, DIM_ALPHA));
  });
});

describe("edge tint by colour group", () => {
  it("tints an edge inside one group with its hue, and leaves bridges neutral", () => {
    const base = { source: "a", target: "b", hiddenByFilter: false, edgesVisible: true, focus: null };
    const inside = edgeAppearance({ ...base, groupColor: "#a78bfa" }, palette);
    expect(inside.color).toContain("rgba(167, 139, 250");
    const bridge = edgeAppearance(base, palette);
    expect(bridge.color).toBe(palette.edge);
  });
});

describe("edges at rest", () => {
  it("are plain lines; only the focused node's edges carry arrowheads", () => {
    const base = { source: "a", target: "b", hiddenByFilter: false, edgesVisible: true, focus: null };
    expect(edgeAppearance(base, palette).type).toBe("line");
    expect(edgeAppearance({ ...base, groupColor: "#a78bfa" }, palette).type).toBe("line");
    expect(edgeAppearance({ ...base, focus: "a" }, palette).type).toBe("arrow");
    expect(edgeAppearance({ ...base, focus: "z" }, palette).type).toBe("line");
  });
});
