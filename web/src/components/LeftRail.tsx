import type { CSSProperties } from "react";
import type { Overview } from "../api/types";
import type { Panel } from "../lib/viewstate";
import { collectionVar } from "../lib/palette";
import { Count } from "./Count";
import { Icon } from "./Icon";

interface Props {
  overview: Overview | null;
  /** The KB the overview belongs to: a new one deals the list in again. */
  overviewKB?: string;
  scope: string | null;
  panel: Panel;
  /** The KB's artifact count, or null when this principal may not open the
   *  Artifacts panel (D238): the entry is then not drawn at all. */
  artifactsTotal: number | null;
  collapsed: boolean;
  /** False inside the narrow-layout sheet, which closes instead. */
  collapsible?: boolean;
  typeFilter: Set<string>;
  statusFilter: Set<string>;
  onScope(scope: string | null): void;
  onPanel(panel: Panel): void;
  onToggleCollapsed(): void;
  onToggleType(type: string): void;
  onToggleStatus(status: string): void;
  onClearFilters(): void;
}

export function LeftRail({
  overview,
  overviewKB = "",
  scope,
  panel,
  artifactsTotal,
  collapsed,
  collapsible = true,
  typeFilter,
  statusFilter,
  onScope,
  onPanel,
  onToggleCollapsed,
  onToggleType,
  onToggleStatus,
  onClearFilters,
}: Props) {
  const lintTotal = overview?.lint.total ?? 0;
  const activeCount = typeFilter.size + statusFilter.size;
  const filtersActive = activeCount > 0;
  // Type and Status filter the graph's nodes, so they exist only on the Atlas:
  // Health lists findings (some with no concept and so no type) and
  // Artifacts lists files, and neither reads the filters. The chips step aside
  // there rather than look applied while doing nothing. The selection lives
  // in App, so it is still applied on the way back to the Atlas.
  const nodeFilters = panel === "atlas";

  return (
    <nav className={`rail${collapsed ? " rail--collapsed" : ""}`} aria-label="Atlas navigation">
      <ul className="rail__panels">
        <li>
          <button
            type="button"
            className="rail__panel"
            aria-label="Atlas"
            title={collapsed ? "Atlas" : undefined}
            aria-current={panel === "atlas" ? "page" : undefined}
            onClick={() => onPanel("atlas")}
          >
            <span className="rail__glyph">
              <Icon name="atlas" />
            </span>
            <span className="rail__label">Atlas</span>
          </button>
        </li>
        <li>
          <button
            type="button"
            className="rail__panel"
            aria-label="Activity"
            title={collapsed ? "Activity" : undefined}
            aria-current={panel === "activity" ? "page" : undefined}
            onClick={() => onPanel("activity")}
          >
            <span className="rail__glyph">
              <Icon name="activity" />
            </span>
            <span className="rail__label">Activity</span>
          </button>
        </li>
        <li>
          <button
            type="button"
            className="rail__panel"
            aria-label="Work"
            title={collapsed ? "Work" : undefined}
            aria-current={panel === "work" ? "page" : undefined}
            onClick={() => onPanel("work")}
          >
            <span className="rail__glyph">
              <Icon name="work" />
            </span>
            <span className="rail__label">Work</span>
          </button>
        </li>
        <li>
          <button
            type="button"
            className="rail__panel"
            aria-label={lintTotal > 0 ? `Health, ${lintTotal} findings` : "Health"}
            title={collapsed ? "Health" : undefined}
            aria-current={panel === "health" ? "page" : undefined}
            onClick={() => onPanel("health")}
          >
            <span className="rail__glyph">
              <Icon name="health" />
            </span>
            <span className="rail__label">Health</span>
            {lintTotal > 0 && (
              <span className="rail__badge">
                <Count value={lintTotal} />
              </span>
            )}
          </button>
        </li>
        {artifactsTotal !== null && (
          <li>
            <button
              type="button"
              className="rail__panel"
              aria-label="Artifacts"
              title={collapsed ? "Artifacts" : undefined}
              aria-current={panel === "artifacts" ? "page" : undefined}
              onClick={() => onPanel("artifacts")}
            >
              <span className="rail__glyph">
                <Icon name="artifacts" />
              </span>
              <span className="rail__label">Artifacts</span>
            </button>
          </li>
        )}
      </ul>

      {!collapsed && (
        <div
          className="rail__scroll"
          onScroll={(event) => {
            const el = event.currentTarget;
            if ((el.scrollTop > 0) !== el.hasAttribute("data-scrolled")) el.toggleAttribute("data-scrolled", el.scrollTop > 0);
          }}
        >
          <section className="rail__section">
            {(["map", "journal"] as const).map((kind, groupIndex, kinds) => {
              const group = (overview?.collections ?? []).filter((c) => (c.kind === "journal" ? "journal" : "map") === kind);
              if (group.length === 0) return null;
              // The whole atlas heads the first list shown: one row among the
              // others, set apart only by its mark.
              const first = kinds.slice(0, groupIndex).every(
                (k) => !(overview?.collections ?? []).some((c) => (c.kind === "journal" ? "journal" : "map") === k),
              );
              return (
                <div key={`${overviewKB}:${kind}`} className="rail__group">
                  <h2 className="rail__title">{kind === "journal" ? "Journals" : "Maps"}</h2>
                  <ul className="rail__collections">
                    {first && (
                      <li className="rail__deal">
                        <button
                          type="button"
                          className="rail__collection"
                          aria-current={scope === null && panel === "atlas" ? "true" : undefined}
                          onClick={() => onScope(null)}
                        >
                          <span className="rail__swatch rail__swatch--all" aria-hidden="true">
                            <Icon name="atlas" size={12} />
                          </span>
                          <span className="rail__collection-name">All</span>
                          <span className="rail__collection-count">
                            <Count value={overview?.concepts.total ?? 0} />
                          </span>
                        </button>
                      </li>
                    )}
                    {group.map((collection, i) => (
                      <li key={collection.name} className="rail__deal" style={{ "--deal": i + (first ? 1 : 0) } as CSSProperties}>
                        <button
                          type="button"
                          className="rail__collection"
                          aria-current={scope === collection.name ? "true" : undefined}
                          onClick={() => onScope(collection.name)}
                        >
                          <span
                            className="rail__swatch"
                            aria-hidden="true"
                            style={{ background: collectionVar(collection.name) }}
                          />
                          <span className="rail__collection-name">{collection.title || collection.name}</span>
                          <span className="rail__collection-count">
                            <Count value={collection.concepts} />
                          </span>
                        </button>
                      </li>
                    ))}
                  </ul>
                </div>
              );
            })}
            {overview?.collections.length === 0 && (
              <p className="rail__empty">No Maps are visible to you in this KB.</p>
            )}
          </section>

          {nodeFilters && filtersActive && (
            <button type="button" className="rail__clear" onClick={onClearFilters}>
              <Icon name="close" size={14} />
              Clear {activeCount} filter{activeCount === 1 ? "" : "s"}
            </button>
          )}
          {nodeFilters && (
            <>
              <FilterSection
                title="Type"
                counts={overview?.concepts.by_type ?? {}}
                active={typeFilter}
                onToggle={onToggleType}
              />
              <FilterSection
                title="Status"
                counts={overview?.concepts.by_status ?? {}}
                active={statusFilter}
                onToggle={onToggleStatus}
              />
            </>
          )}

        </div>
      )}

      {/* The collapse toggle sits at the foot of the rail: at the top it took a
          whole row of its own above the navigation. */}
      {collapsible && (
        <button
          type="button"
          className="rail__collapse button button--icon"
          onClick={onToggleCollapsed}
          aria-label={collapsed ? "Expand navigation" : "Collapse navigation"}
          aria-expanded={!collapsed}
        >
          <Icon name={collapsed ? "panel-open" : "panel-close"} />
        </button>
      )}
    </nav>
  );
}

function FilterSection({
  title,
  counts,
  active,
  onToggle,
}: {
  title: string;
  counts: Record<string, number>;
  active: Set<string>;
  onToggle(value: string): void;
}) {
  const entries = Object.entries(counts).sort((a, b) => b[1] - a[1]);
  if (entries.length === 0) return null;
  return (
    <section className="rail__section">
      <h2 className="rail__title">{title}</h2>
      <ul className="rail__chips">
        {entries.map(([value, count]) => (
          <li key={value}>
            <button
              type="button"
              className="chip"
              aria-pressed={active.has(value)}
              onClick={() => onToggle(value)}
            >
              {value}
              <span className="chip__count">{count}</span>
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
