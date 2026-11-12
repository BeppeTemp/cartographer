import type { ExtensionColumn } from "../../api/types";
import { formatCell, valueAt, type Cell } from "../../lib/extension";

// The four fixed renderers of a plugin view (D1938): data in, never code; values are React text, so markup is inert.
interface Props<T> {
  data: T;
  columns: ExtensionColumn[];
  label: string;
  onOpen(conceptId: string): void;
}

function Value({ row, col, onOpen }: { row: unknown; col: ExtensionColumn; onOpen(conceptId: string): void }) {
  const cell: Cell = formatCell(valueAt(row, col.key), col.format);
  if (cell.concept)
    return (
      <button type="button" className="linklike" onClick={() => onOpen(cell.concept!)}>
        {cell.text}
      </button>
    );
  return cell.tone ? <span className={`pill pill--${cell.tone}`}>{cell.text}</span> : <>{cell.text}</>;
}

export function Table({ data, columns, label, onOpen }: Props<unknown[]>) {
  return (
    <table className="ext-table" aria-label={label}>
      <thead>
        <tr>
          {columns.map((c, i) => (
            <th key={i} scope="col">
              {c.label}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {data.map((row, r) => (
          <tr key={r}>
            {columns.map((c, i) => (
              <td key={i}>
                <Value row={row} col={c} onOpen={onOpen} />
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

export function List({ data, columns, label, onOpen }: Props<unknown[]>) {
  return (
    <ul className="ext-list" aria-label={label}>
      {data.map((row, r) => (
        <li key={r}>{columns[0] && <Value row={row} col={columns[0]} onOpen={onOpen} />}</li>
      ))}
    </ul>
  );
}

function Pairs({ data, columns, label, onOpen, stats }: Props<Record<string, unknown>> & { stats?: boolean }) {
  const value = (c: ExtensionColumn) => <Value row={data} col={c} onOpen={onOpen} />;
  return stats ? (
    <ul className="ext-stats" aria-label={label}>
      {columns.map((c, i) => (
        <li key={i}>
          <strong>{value(c)}</strong>
          {c.label}
        </li>
      ))}
    </ul>
  ) : (
    <dl className="ext-kv" aria-label={label}>
      {columns.map((c, i) => (
        <div key={i} className="ext-kv__row">
          <dt>{c.label}</dt>
          <dd>{value(c)}</dd>
        </div>
      ))}
    </dl>
  );
}

export const Stats = (props: Props<Record<string, unknown>>) => <Pairs {...props} stats />;
export const KeyValue = (props: Props<Record<string, unknown>>) => <Pairs {...props} />;
