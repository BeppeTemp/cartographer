import { useMemo, useState } from "react";
import type { GraphNode } from "../api/types";
import { collectionVar } from "../lib/palette";

type SortKey = "id" | "degree" | "collection";

/**
 * The keyboard and screen-reader path to every node the canvas draws.
 *
 * A WebGL canvas is one opaque element to assistive technology, so without
 * this list the graph would simply not exist for part of the audience. It
 * exposes the same selection action, not a reduced one: reaching a concept
 * here and reaching it by clicking must land in the same place.
 */
export function NodeList({
  nodes,
  selected,
  onSelect,
}: {
  nodes: GraphNode[];
  selected: string | null;
  onSelect(id: string): void;
}) {
  const [sort, setSort] = useState<SortKey>("id");

  const sorted = useMemo(() => {
    const copy = [...nodes];
    copy.sort((a, b) => {
      if (sort === "degree") {
        const delta = b.in_degree + b.out_degree - (a.in_degree + a.out_degree);
        if (delta !== 0) return delta;
      }
      if (sort === "collection") {
        const delta = (a.collection ?? "").localeCompare(b.collection ?? "");
        if (delta !== 0) return delta;
      }
      return a.id.localeCompare(b.id);
    });
    return copy;
  }, [nodes, sort]);

  return (
    <section className="nodelist" aria-label="Concepts in this view">
      <div className="nodelist__head">
        <h2 className="nodelist__title">Concepts</h2>
        <label className="nodelist__sort">
          <span className="sr-only">Sort concepts by</span>
          <select
            className="input"
            value={sort}
            onChange={(event) => setSort(event.target.value as SortKey)}
          >
            <option value="id">Name</option>
            <option value="degree">Links</option>
            <option value="collection">Map</option>
          </select>
        </label>
      </div>
      {sorted.length === 0 ? (
        <p className="nodelist__empty">No concepts match the current filters.</p>
      ) : (
        <ul className="nodelist__items">
          {sorted.map((node) => (
            <li key={node.id}>
              <button
                type="button"
                className="nodelist__item"
                aria-current={node.id === selected ? "true" : undefined}
                onClick={() => onSelect(node.id)}
              >
                <span
                  className="nodelist__swatch"
                  aria-hidden="true"
                  style={{ background: collectionVar(node.collection ?? "") }}
                />
                <span className="nodelist__id">{node.id}</span>
                <span className="nodelist__meta">
                  {node.in_degree + node.out_degree} link
                  {node.in_degree + node.out_degree === 1 ? "" : "s"}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
