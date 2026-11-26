import { useState } from "react";
import type { DashboardEntry } from "../api/types";

/** The columns a table with no `columns` of its own (or a caller who may not read them) draws. */
const DEFAULT_COLUMNS = ["title", "type", "status"];

/** The text of one cell: an entry field first, then a frontmatter key; "" when the row has none. */
export function cellText(e: DashboardEntry, col: string): string {
  switch (col) {
    case "id":
      return e.id;
    case "title":
      return e.title || e.id;
    case "type":
    case "map":
    case "status":
    case "state":
    case "reason":
      return e[col] ?? "";
    case "age_days":
      return e.age_days === undefined ? "" : String(e.age_days);
    case "stale":
      return e.stale ? "stale" : "";
    default:
      return e.fields?.[col] ?? "";
  }
}

/** Empty cells go last whichever way the column runs; numbers compare as numbers, the rest as text. */
function compareCells(a: string, b: string): number {
  const na = a !== "" && Number.isFinite(Number(a));
  const nb = b !== "" && Number.isFinite(Number(b));
  if (na && nb) return Number(a) - Number(b);
  return a.localeCompare(b);
}

/**
 * The rows of a `table` widget, sortable by clicking a header. The resolver already ordered and
 * trimmed the rows by the widget's own `sort`; a click re-orders what is shown, locally. Column
 * names come from the widget, so a caller without whole-KB read (no `columns`) gets the default ones.
 */
export function DashboardTable({
  entries,
  columns,
  onOpen,
}: {
  entries: DashboardEntry[];
  columns?: string[];
  onOpen?(conceptId: string): void;
}) {
  const cols = columns && columns.length > 0 ? columns : DEFAULT_COLUMNS;
  const [sort, setSort] = useState<{ col: string; dir: "asc" | "desc" } | null>(null);

  if (entries.length === 0) return <p className="page-note">Nothing here.</p>;

  const active = sort && cols.includes(sort.col) ? sort : null;
  const rows = [...entries];
  if (active) {
    const sign = active.dir === "asc" ? 1 : -1;
    rows.sort((a, b) => {
      const x = cellText(a, active.col);
      const y = cellText(b, active.col);
      if ((x === "") !== (y === "")) return x === "" ? 1 : -1;
      return sign * compareCells(x, y);
    });
  }
  const click = (col: string) =>
    setSort((s) => (s && s.col === col ? { col, dir: s.dir === "asc" ? "desc" : "asc" } : { col, dir: "asc" }));

  return (
    <div className="dashboard__table-wrap">
      <table className="dashboard__table">
        <thead>
          <tr>
            {cols.map((c) => (
              <th
                key={c}
                scope="col"
                aria-sort={active?.col === c ? (active.dir === "asc" ? "ascending" : "descending") : "none"}
              >
                <button type="button" className="dashboard__sort" onClick={() => click(c)}>
                  {c}
                  <span aria-hidden="true">{active?.col === c ? (active.dir === "asc" ? " ▲" : " ▼") : ""}</span>
                </button>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((e, i) => (
            // An entry id can repeat in one widget: the index keeps the key unique.
            <tr key={`${e.id}-${i}`}>
              {cols.map((c) => (
                <td key={c}>
                  {c === "title" && onOpen ? (
                    <button type="button" className="dashboard__cell-link" onClick={() => onOpen(e.id)}>
                      {cellText(e, c)}
                    </button>
                  ) : (
                    cellText(e, c)
                  )}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
