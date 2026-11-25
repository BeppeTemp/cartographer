import { useEffect, useRef, useState, type CSSProperties } from "react";
import type { DashboardResolved, DashboardWidget } from "../api/types";
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
 * Drawn here: `stat`, `note` and the error tile of a widget that failed to
 * resolve. Any other type shows a neutral tile naming it.
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
    default:
      return <p className="page-note">A {widget.type ?? "unknown"} widget is not drawn yet.</p>;
  }
}
