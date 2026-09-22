import type { GraphSnapshot, Overview } from "../api/types";
import type { Panel } from "../lib/viewstate";
import { collectionVar } from "../lib/palette";

interface Props {
  overview: Overview | null;
  snapshot: GraphSnapshot | null;
  scope: string | null;
  panel: Panel;
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
  snapshot,
  scope,
  panel,
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
  const filtersActive = typeFilter.size > 0 || statusFilter.size > 0;

  return (
    <nav className={`rail${collapsed ? " rail--collapsed" : ""}`} aria-label="Atlas navigation">
      {collapsible && (
        <button
          type="button"
          className="rail__collapse button button--icon"
          onClick={onToggleCollapsed}
          aria-label={collapsed ? "Expand navigation" : "Collapse navigation"}
          aria-expanded={!collapsed}
        >
          <span aria-hidden="true">{collapsed ? "»" : "«"}</span>
        </button>
      )}

      <ul className="rail__panels">
        <li>
          <button
            type="button"
            className="rail__panel"
            aria-current={panel === "atlas" ? "page" : undefined}
            onClick={() => onPanel("atlas")}
          >
            <span className="rail__glyph" aria-hidden="true">
              &#9673;
            </span>
            <span className="rail__label">Atlas</span>
          </button>
        </li>
        <li>
          <button
            type="button"
            className="rail__panel"
            aria-current={panel === "observatory" ? "page" : undefined}
            onClick={() => onPanel("observatory")}
          >
            <span className="rail__glyph" aria-hidden="true">
              &#9678;
            </span>
            <span className="rail__label">Observatory</span>
            {lintTotal > 0 && <span className="rail__badge">{lintTotal}</span>}
          </button>
        </li>
      </ul>

      {!collapsed && (
        <div className="rail__scroll">
          <section className="rail__section">
            <h2 className="rail__title">Maps &amp; Journals</h2>
            <ul className="rail__collections">
              <li>
                <button
                  type="button"
                  className="rail__collection"
                  aria-current={scope === null ? "true" : undefined}
                  onClick={() => onScope(null)}
                >
                  <span className="rail__swatch rail__swatch--all" aria-hidden="true" />
                  <span className="rail__collection-name">Whole atlas</span>
                  <span className="rail__collection-count">
                    {overview?.concepts.total ?? 0}
                  </span>
                </button>
              </li>
              {(overview?.collections ?? []).map((collection) => (
                <li key={collection.name}>
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
                    <span className="rail__collection-name">
                      {collection.title || collection.name}
                      {collection.kind === "journal" && (
                        <span className="rail__kind"> journal</span>
                      )}
                    </span>
                    <span className="rail__collection-count">{collection.concepts}</span>
                  </button>
                </li>
              ))}
            </ul>
            {overview?.collections.length === 0 && (
              <p className="rail__empty">No Maps are visible to you in this KB.</p>
            )}
          </section>

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

          {filtersActive && (
            <button type="button" className="button rail__clear" onClick={onClearFilters}>
              Clear filters
            </button>
          )}

          {snapshot && (
            <p className="rail__meta">
              {snapshot.nodes.length} node{snapshot.nodes.length === 1 ? "" : "s"},{" "}
              {snapshot.edges.length} link{snapshot.edges.length === 1 ? "" : "s"} in view
            </p>
          )}
        </div>
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
