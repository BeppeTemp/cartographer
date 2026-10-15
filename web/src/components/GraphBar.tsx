import type { GraphSnapshot, MapContract } from "../api/types";
import type { ColorBy } from "../lib/palette";
import { GrowthButton } from "./GraphView";

/**
 * The graph's context bar (D818): one line above the canvas, part of the
 * layout and never drawn over the graph. It says what is drawn -- the KB, the
 * Map it is narrowed to, how much -- and carries what acts on the drawing: the
 * colour switch and the growth replay. The colour key and the Map's contract
 * are in the rail, beside the Maps they belong to.
 */
export function GraphBar({
  kb,
  map,
  mapConcepts,
  snapshot,
  colorBy,
  onColorBy,
}: {
  kb: string;
  map: MapContract | null;
  /** The number the rail shows for the Map. */
  mapConcepts: number;
  snapshot: GraphSnapshot;
  colorBy: ColorBy;
  onColorBy(value: ColorBy): void;
}) {
  const facts = map
    ? [`${mapConcepts} concept${mapConcepts === 1 ? "" : "s"}`]
    : [
        `${snapshot.nodes.length} concept${snapshot.nodes.length === 1 ? "" : "s"}`,
        `${snapshot.edges.length} link${snapshot.edges.length === 1 ? "" : "s"}`,
      ];
  return (
    <section className="graphbar" aria-label="Graph context">
      <p className="graphbar__context">
        <span className="graphbar__crumb">{kb}</span>
        {map && (
          <>
            <span className="graphbar__sep" aria-hidden="true">
              ›
            </span>
            <strong className="graphbar__map">{map.title || map.name}</strong>
          </>
        )}
        <span className="graphbar__facts">{facts.join(" · ")}</span>
      </p>
      <div className="legend__switch" role="group" aria-label="Colour nodes by">
        {(
          [
            ["community", "Community"],
            ["map", "Map"],
          ] as const
        ).map(([value, label]) => (
          <button
            key={value}
            type="button"
            className="legend__option"
            aria-pressed={colorBy === value}
            onClick={() => onColorBy(value)}
          >
            {label}
          </button>
        ))}
      </div>
      <GrowthButton />
    </section>
  );
}
