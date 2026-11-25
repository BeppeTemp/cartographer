import { useEffect, useRef, useState, type CSSProperties } from "react";
import type { DashboardEntry, DashboardGroup, DashboardResolved, DashboardWidget } from "../api/types";
import { accentColor, widgetIcon } from "../lib/dashboard";
import { Icon } from "./Icon";
import { Markdown } from "./Markdown";

/**
 * A resolved dashboard drawn as a grid of cards (four columns, one under
 * 900 px). Read-only: a dashboard changes through an agent's `dashboard_patch`.
 * A caller without whole-KB read gets resolved data and structural keys only,
 * so nothing here assumes a title, a source or a hash: a widget with no title
 * is named by its id.
 *
 * Drawn here: `stat`, `note`, `kanban`, `list` and the error tile of a widget
 * that failed to resolve. Any other type (`agenda`) shows a neutral tile naming it.
 */
export function DashboardView({
  data,
  onOpen,
}: {
  data: DashboardResolved;
  onOpen?(conceptId: string): void;
}) {
  return (
    <div className="dashboard">
      {data.description ? <p className="page-note">{data.description}</p> : null}
      <div className="dashboard__grid">
        {data.widgets.map((w, i) => (
          // The server keeps a widget that repeats an id (flagged, drawn as an error tile): the id alone is not a unique key.
          <WidgetCard key={`${w.id}-${i}`} widget={w} onOpen={onOpen} />
        ))}
      </div>
    </div>
  );
}

function WidgetCard({ widget, onOpen }: { widget: DashboardWidget; onOpen?(conceptId: string): void }) {
  // A widget whose resolved content changed flashes once; the first draw does not.
  const sig = JSON.stringify(widget);
  const seen = useRef(sig);
  const [flash, setFlash] = useState(0);
  useEffect(() => {
    if (seen.current === sig) return;
    seen.current = sig;
    setFlash((n) => n + 1);
  }, [sig]);

  const icon = widgetIcon(widget.icon);
  const span = Math.min(Math.max(widget.width ?? 1, 1), 4);
  const style = { "--dashboard-accent": accentColor(widget.accent), "--dashboard-span": span } as CSSProperties;
  const failed = !!widget.error;
  return (
    <section className={`dashboard__card${failed ? " dashboard__card--error" : ""}`} style={style}>
      {flash > 0 ? <span key={flash} className="dashboard__flash" aria-hidden="true" /> : null}
      <header className="dashboard__head">
        {icon ? (
          <span className="dashboard__icon" aria-hidden="true">
            <Icon name={icon} size={16} />
          </span>
        ) : null}
        <h2 className="dashboard__title">{widget.title || widget.id}</h2>
        {!failed && widget.type !== "note" ? <span className="dashboard__total">{widget.total}</span> : null}
      </header>
      <WidgetBody widget={widget} onOpen={onOpen} />
    </section>
  );
}

function WidgetBody({ widget, onOpen }: { widget: DashboardWidget; onOpen?(conceptId: string): void }) {
  if (widget.error) {
    return (
      <p className="dashboard__error" role="alert">
        This widget cannot be drawn: {widget.error}
      </p>
    );
  }
  switch (widget.type) {
    case "stat":
      return widget.show === "groups" || (widget.groups && widget.groups.length > 0) ? (
        <ul className="dashboard__groups">
          {(widget.groups ?? []).map((g) => (
            <li key={g.key}>
              <span>{g.key}</span>
              <span className="dashboard__count">{g.total}</span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="dashboard__stat">{widget.total}</p>
      );
    case "note":
      return widget.text ? (
        <div className="dashboard__note">
          <Markdown onNavigate={onOpen}>{widget.text}</Markdown>
        </div>
      ) : null;
    case "kanban":
      return (
        <div className="dashboard__columns">
          {(widget.groups ?? []).map((g, i) => (
            <section key={`${g.key}-${i}`} className="dashboard__column" aria-label={`${g.key || "(none)"}, ${g.total}`}>
              <h3 className="dashboard__column-title">
                {g.key || "(none)"} <span className="dashboard__count">{g.total}</span>
              </h3>
              <EntryList entries={g.entries ?? []} onOpen={onOpen} />
            </section>
          ))}
        </div>
      );
    case "list":
      return widget.groups && widget.groups.length > 0 ? (
        <div>
          {widget.groups.map((g: DashboardGroup, i) => (
            <section key={`${g.key}-${i}`} aria-label={`${g.key || "(none)"}, ${g.total}`}>
              <h3 className="dashboard__column-title">
                {g.key || "(none)"} <span className="dashboard__count">{g.total}</span>
              </h3>
              <EntryList entries={g.entries ?? []} onOpen={onOpen} />
            </section>
          ))}
        </div>
      ) : (
        <EntryList entries={widget.entries ?? []} onOpen={onOpen} />
      );
    default:
      return <p className="page-note">A {widget.type ?? "unknown"} widget is not drawn yet.</p>;
  }
}

function EntryList({ entries, onOpen }: { entries: DashboardEntry[]; onOpen?(conceptId: string): void }) {
  if (entries.length === 0) return <p className="page-note">Nothing here.</p>;
  return (
    <ul className="dashboard__entries">
      {entries.map((e, i) => (
        // An entry id can repeat across groups of one widget: the index keeps the key unique.
        <li key={`${e.id}-${i}`}>
          <EntryCard entry={e} onOpen={onOpen} />
        </li>
      ))}
    </ul>
  );
}

/** Today as YYYY-MM-DD in the browser's zone, the form a `review_after` takes. */
function today(): string {
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

/**
 * A `review_after` is due the day after its date, as lint reads it: today is not due, a date
 * strictly before today is. A value that is not a real calendar date is never due.
 */
export function isDue(reviewAfter: string | undefined, now: string = today()): boolean {
  const m = /^(\d{4})-(\d{2})-(\d{2})/.exec(reviewAfter ?? "");
  if (!m) return false;
  const [y, mo, d] = [Number(m[1]), Number(m[2]), Number(m[3])];
  const t = new Date(Date.UTC(y, mo - 1, d));
  if (t.getUTCFullYear() !== y || t.getUTCMonth() !== mo - 1 || t.getUTCDate() !== d) return false;
  return m[0] < now;
}

/**
 * One row of a kanban column or a list, in the look of the Work page's cards.
 * Only the id is certain: the title, the type and the fields may be absent.
 */
function EntryCard({ entry, onOpen }: { entry: DashboardEntry; onOpen?(conceptId: string): void }) {
  const waiting = entry.fields?.waiting_on;
  const review = entry.fields?.review_after;
  const due = isDue(review);
  const body = (
    <>
      <span className="work__card-title">{entry.title || entry.id}</span>
      <span className="work__card-meta">
        {entry.type ? <span className="pill">{entry.type}</span> : null}
        {entry.stale ? (
          <span className="pill pill--warning">{entry.age_days !== undefined ? `stale \u00b7 ${entry.age_days}d` : "stale"}</span>
        ) : entry.age_days !== undefined ? (
          <span className="work__age">{entry.age_days}d</span>
        ) : null}
      </span>
      {waiting ? <span className="work__card-note">Waiting on {waiting}</span> : null}
      {review ? (
        <span className={`work__card-note${due ? " dashboard__due" : ""}`}>
          {due ? "Review due " : "Review after "}
          {review}
        </span>
      ) : null}
    </>
  );
  return (
    <div className="work__card">
      {onOpen ? (
        <button type="button" className="work__card-main" onClick={() => onOpen(entry.id)}>
          {body}
        </button>
      ) : (
        <div className="work__card-main work__card-main--static">{body}</div>
      )}
    </div>
  );
}
