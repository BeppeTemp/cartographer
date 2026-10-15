import type { ReactNode } from "react";
import type { MapContract } from "../api/types";

/**
 * The Map the Atlas is filtered to, as a compact card floating over the
 * graph's top corner: the contract `map_list` returns, the rules the server
 * enforces on the pages inside. A card and not the reading panel, so the
 * filtered graph keeps the whole canvas; it goes when "All" is picked in the
 * rail, and each fact explains itself on hover.
 */
export function MapPanel({
  map: m,
  concepts,
}: {
  map: MapContract;
  /** The number the rail shows for this Map. */
  concepts: number;
}) {
  const byType = Object.entries(m.field_values_by_type ?? {});
  const hasValues = Object.keys(m.field_values ?? {}).length > 0;
  return (
    <aside className="map-card" aria-label={`Map ${m.title || m.name}`}>
      <header title="The contract lives in the Map's _map.md; agents change it with map_update and the server enforces it on every write.">
        <p className="map-card__eyebrow">
          {m.kind || "map"} · <code>{m.name}</code>
        </p>
        <h2 className="map-card__title">{m.title || m.name}</h2>
      </header>
      <dl className="map-card__facts">
        <Fact label="concepts" hint="Pages in this Map, as the rail counts them.">
          {concepts}
          {m.expanded_concepts > 0 && <span className="map-card__note"> ({m.expanded_concepts} expanded)</span>}
        </Fact>
        {m.ontology_mode && (
          <Fact
            label="ontology"
            hint="How strictly the Map's concept types are enforced: whether a page may use a type the contract does not list."
          >
            {m.ontology_mode}
          </Fact>
        )}
        {m.stale_after !== undefined && (
          <Fact
            label="stale after"
            hint="Days without an update after which an open page (open, in-progress or blocked) is flagged as stale."
          >
            {m.stale_after} days{m.stale_after_defaulted && <span className="map-card__note"> (default)</span>}
          </Fact>
        )}
        {!!m.concept_types?.length && (
          <Fact label="types" hint="The concept types a page in this Map may declare.">
            {m.concept_types.join(", ")}
          </Fact>
        )}
        {hasValues && (
          <Fact label="values" hint="The values a frontmatter field accepts on any page in this Map.">
            <Values values={m.field_values!} />
          </Fact>
        )}
        {byType.map(([type, values]) => (
          <Fact key={type} label={`values, ${type}`} hint={`The values a field accepts on ${type} pages only.`}>
            <Values values={values} />
          </Fact>
        ))}
        {!!m.lint_ignore?.length && (
          <Fact label="lint ignore" hint="Lint checks skipped for this Map, so they raise no findings here.">
            <code className="map-card__codes">{m.lint_ignore.join(", ")}</code>
          </Fact>
        )}
      </dl>
    </aside>
  );
}

function Fact({ label, hint, children }: { label: string; hint: string; children: ReactNode }) {
  return (
    <div className="map-card__fact" title={hint}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}

function Values({ values }: { values: Record<string, string[]> }) {
  return (
    <ul className="map-card__values">
      {Object.entries(values).map(([field, allowed]) => (
        <li key={field}>
          <code>{field}</code>: {allowed.join(", ")}
        </li>
      ))}
    </ul>
  );
}
