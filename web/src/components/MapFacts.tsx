import type { ReactNode } from "react";
import type { MapContract } from "../api/types";

/**
 * A Map's contract, under its row in the rail while the Atlas is narrowed to
 * it: what `map_list` returns, the rules the server enforces on the pages
 * inside. Each fact explains itself on hover.
 */
export function MapFacts({ map: m }: { map: MapContract }) {
  const byType = Object.entries(m.field_values_by_type ?? {});
  const hasValues = Object.keys(m.field_values ?? {}).length > 0;
  return (
    <dl
      className="map-facts"
      aria-label={`Contract of ${m.title || m.name}`}
      title="The contract lives in the Map's _map.md; agents change it with map_update and the server enforces it on every write."
    >
      {m.kind && m.kind !== "map" && (
        <Fact label="kind" hint="What the folder holds: a map of knowledge pages, or a journal of dated entries.">
          {m.kind}
        </Fact>
      )}
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
          {m.stale_after} days{m.stale_after_defaulted && <span className="map-facts__note"> (default)</span>}
        </Fact>
      )}
      {m.expanded_concepts > 0 && (
        <Fact label="expanded" hint="Concepts here expanded into a folder of their own, with assets and sub-pages.">
          {m.expanded_concepts}
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
          <ul className="map-facts__codes">
            {m.lint_ignore.map((c) => (
              <li key={c}>
                <code>{c}</code>
              </li>
            ))}
          </ul>
        </Fact>
      )}
    </dl>
  );
}

function Fact({ label, hint, children }: { label: string; hint: string; children: ReactNode }) {
  return (
    <div className="map-facts__fact" title={hint}>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </div>
  );
}

function Values({ values }: { values: Record<string, string[]> }) {
  return (
    <ul className="map-facts__values">
      {Object.entries(values).map(([field, allowed]) => (
        <li key={field}>
          <code>{field}</code>: {allowed.join(", ")}
        </li>
      ))}
    </ul>
  );
}
