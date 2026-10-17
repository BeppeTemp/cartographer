import { Fragment, type CSSProperties, type ReactNode } from "react";
import type { MapContract } from "../api/types";
import type { MotionToken } from "../lib/theme";

/** The docked panel's width in px: the strip it takes beside the graph, which
 *  App also takes off what the reading panel may cover. */
export const MAP_PANEL_WIDTH = 420;
/** How long the docked panel takes to slide in or out: the canvas beside it
 *  resizes along, so the graph never jumps. The CSS animation uses the same token. */
export const MAP_PANEL_SLIDE: MotionToken = "--motion-deliberate";

/** What the server does with an ontology mode (docs/data-plane.md §Maps and Journals). */
const ONTOLOGY_NOTE: Record<string, string> = {
  strict: "only these types are accepted",
  emergent: "a new type is accepted and registered",
  off: "types are not checked",
};

const days = (n: number) => `${n} day${n === 1 ? "" : "s"}`;

/**
 * A Map's contract, read off the `_map.md` descriptor `map_list` returns: the
 * rules the server enforces on every page inside, one titled section per
 * concern, so the panel doubles as the contract's documentation.
 *
 * Each section opens with one sentence on what it governs; each row is a plain
 * label, the value, and the key exactly as it is written in `_map.md`, with a
 * short note only where the value needs one. The longer explanation is the
 * row's tooltip.
 *
 * Docked to the right edge of the graph area while the Atlas is narrowed to
 * the Map, full height, a column of its own: the canvas is what is left beside
 * it, and the reading panel opens over it. On a narrow screen `inline`
 * makes it a folded block above the graph instead. A section with nothing to
 * show is left out; a value the Map does not set is not invented.
 */
export function MapContractPanel({
  map: m,
  concepts,
  inline = false,
  leaving = false,
}: {
  map: MapContract;
  /** The number the rail shows for the Map. */
  concepts: number;
  inline?: boolean;
  /** The Atlas has left the Map: the panel is sliding out. */
  leaving?: boolean;
}) {
  const title = m.title || m.name;
  const kind = m.kind === "journal" ? "journal" : "map";
  const count = `${concepts} concept${concepts === 1 ? "" : "s"}`;
  const label = `Contract of ${title}`;

  const body = (
    <>
      {!!m.malformed?.length && (
        <div className="mapcontract__warn" role="alert">
          <p className="mapcontract__warn-title">
            Some entries of <code>_map.md</code> cannot be read
          </p>
          <ul>
            {m.malformed.map((e) => (
              <li key={e.key}>
                <code>{e.key}</code>
                <span>{e.reason}</span>
              </li>
            ))}
          </ul>
          <p className="mapcontract__note">Lint reports each as contract_malformed until it is fixed.</p>
        </div>
      )}
      {sections(m, concepts).map((s) => (
        <section key={s.title} className="mapcontract__section">
          <h3 className="mapcontract__heading">{s.title}</h3>
          <p className="mapcontract__intro">{s.intro}</p>
          <dl className="mapcontract__rows">{s.rows}</dl>
        </section>
      ))}
    </>
  );
  // What the panel is and where it comes from, said once at the top: the file,
  // and how it changes.
  const source = (
    <p className="mapcontract__source">
      <code>{m.name}/_map.md</code>
      <span>Agents change it with map_update; the server checks every write against it.</span>
    </p>
  );

  if (inline) {
    return (
      <details className="mapcontract mapcontract--inline" aria-label={label}>
        <summary className="mapcontract__summary">
          <span className="mapcontract__summary-title">{title}</span>
          <span className="mapcontract__summary-meta">
            {kind} · {count}
          </span>
        </summary>
        <div className="mapcontract__scroll">
          {source}
          {body}
        </div>
      </details>
    );
  }
  return (
    <section
      className={leaving ? "mapcontract mapcontract--docked mapcontract--leaving" : "mapcontract mapcontract--docked"}
      aria-label={label}
      aria-hidden={leaving || undefined}
      style={
        {
          width: MAP_PANEL_WIDTH,
          "--map-panel-width": `${MAP_PANEL_WIDTH}px`,
        } as CSSProperties
      }
    >
      <header className="mapcontract__head">
        <h2 className="mapcontract__title">Map contract</h2>
        {source}
      </header>
      <div className="mapcontract__scroll">{body}</div>
    </section>
  );
}

interface Section {
  title: string;
  /** One sentence: what the section governs. */
  intro: string;
  rows: ReactNode[];
}

/** The sections that have something to say, in reading order. */
function sections(m: MapContract, concepts: number): Section[] {
  const t = m.thresholds;
  const all: Section[] = [
    {
      title: "About",
      intro: "What the Map is, as its descriptor names it.",
      rows: [
        <Row key="title" label="Title" k="title" hint="The Map's name, shown in the rail and the reading panel.">
          {m.title || m.name}
        </Row>,
        <Row
          key="kind"
          label="Kind"
          k="kind"
          note={m.kind === "journal" ? "dated entries, appended over time" : "pages grouped by theme"}
          hint="map: a thematic domain with mixed types. journal: a chronological, append-oriented log."
        >
          {m.kind === "journal" ? "journal" : "map"}
        </Row>,
        !!m.description && (
          <Row key="description" label="Description" k="description" hint="What the Map covers, in one sentence.">
            {m.description}
          </Row>
        ),
        <Row key="concepts" label="Concepts" hint="Concepts in this Map that you can see.">
          {concepts}
        </Row>,
      ],
    },
    {
      title: "Types & fields",
      intro: "What a page in this Map may be, and what it must or must not carry.",
      rows: [
        has(m.concept_types) && (
          <Row key="types" label="Types" k="concept_types" hint="The types a concept in this Map may declare.">
            <Chips items={m.concept_types!} />
          </Row>
        ),
        !!m.ontology_mode && (
          <Row
            key="ontology"
            label="Ontology"
            k="ontology_mode"
            note={ONTOLOGY_NOTE[m.ontology_mode]}
            hint="How strictly the Map's concept types are enforced: strict, emergent or off."
          >
            {m.ontology_mode}
          </Row>
        ),
        has(m.required_fields) && (
          <Row
            key="required"
            label="Required fields"
            k="required_fields"
            hint="Frontmatter fields every concept here must carry: lint flags a page that lacks one."
          >
            <Chips items={m.required_fields!} code />
          </Row>
        ),
        ...Object.entries(m.required_fields_by_type ?? {}).map(([type, fields]) => (
          <Row
            key={`required.${type}`}
            label="Required fields"
            k={`required_fields.${type}`}
            note={`on ${type} pages, on top of the list above`}
            hint="Fields required of one exact type, on top of the map-wide list."
          >
            <Chips items={fields} code />
          </Row>
        )),
        has(m.forbidden_fields) && (
          <Row
            key="forbidden"
            label="Forbidden fields"
            k="forbidden_fields"
            hint="Fields no concept here may carry: lint flags a page that has one."
          >
            <Chips items={m.forbidden_fields!} code />
          </Row>
        ),
        m.expanded_concepts > 0 && (
          <Row
            key="expanded"
            label="Expanded"
            note="each in a folder with its assets and sub-pages"
            hint="Concepts expanded into a folder of their own, with assets and sub-pages."
          >
            {m.expanded_concepts} concept{m.expanded_concepts === 1 ? "" : "s"}
          </Row>
        ),
      ],
    },
    {
      title: "Templates",
      intro: "The page shapes this Map accepts.",
      rows: [
        has(m.templates) && (
          <Row
            key="templates"
            label="Templates"
            k="templates"
            hint="The page shapes this Map accepts; a page names its own in its shape field."
          >
            <Chips items={m.templates!} code />
          </Row>
        ),
        !!m.default_template && (
          <Row
            key="default"
            label="Default"
            k="default_template"
            note="followed by a page that names none"
            hint="The template a page follows when it names none and its type is the template's."
          >
            <code>{m.default_template}</code>
          </Row>
        ),
        !!m.require_template && (
          <Row
            key="require"
            label="Binding"
            k="require_template"
            note="every page must follow one of the templates"
            hint="Every page here must follow one of the Map's templates; lint checks the binding."
          >
            <Flag>required</Flag>
          </Row>
        ),
        !!m.template_sections && (
          <Row
            key="sections"
            label="Sections"
            k="template_sections"
            note="a page carries its template's H2 sections"
            hint="A page must carry the required H2 sections of its template."
          >
            <Flag>required</Flag>
          </Row>
        ),
      ],
    },
    {
      title: "Allowed values",
      intro: "A field listed here takes only these values; lint flags any other.",
      rows: [
        ...Object.entries(m.field_values ?? {}).map(([field, values]) => (
          <Row
            key={`values.${field}`}
            label={field}
            code
            k={`field_values.${field}`}
            hint={`The values ${field} accepts on any concept in this Map.`}
          >
            <Chips items={values} />
          </Row>
        )),
        ...Object.entries(m.field_values_by_type ?? {}).flatMap(([type, fields]) =>
          Object.entries(fields).map(([field, values]) => (
            <Row
              key={`values.${type}.${field}`}
              label={field}
              code
              k={`field_values.${type}.${field}`}
              note={`on ${type} pages only`}
              hint={`The values ${field} accepts on ${type} pages; it replaces the map-wide list for that field.`}
            >
              <Chips items={values} />
            </Row>
          )),
        ),
        ...Object.entries(m.value_synonyms ?? {}).map(([canonical, synonyms]) => (
          <Row
            key={`synonyms.${canonical}`}
            label={canonical}
            code
            k={`value_synonyms.${canonical}`}
            note={`also read as ${canonical}`}
            hint="Spellings read as the canonical value, so a synonym does not split a vocabulary."
          >
            <Chips items={synonyms} />
          </Row>
        )),
      ],
    },
    {
      title: "Lifecycle",
      intro: "When a page here counts as open work, and when it ages.",
      rows: [
        !!m.open_field && (
          <Row
            key="open_field"
            label="Open field"
            k="open_field"
            note="carries a page's open or closed state"
            hint="The frontmatter key that says whether a concept here is open or closed; status otherwise."
          >
            <code>{m.open_field}</code>
          </Row>
        ),
        has(m.open_statuses) && (
          <Row
            key="open_statuses"
            label="Open statuses"
            k="open_statuses"
            hint="Values that mean the work is not finished: they age, and work_list lists them."
          >
            <Chips items={m.open_statuses!} />
          </Row>
        ),
        has(m.open_markers) && (
          <Row
            key="open_markers"
            label="Open markers"
            k="open_markers"
            note="words that mark an open question"
            hint="Words in a page that mark an open question, in the KB's language."
          >
            <Chips items={m.open_markers!} code />
          </Row>
        ),
        m.stale_after !== undefined && (
          <Row
            key="stale"
            label="Stale after"
            k="stale_after"
            note="an open page not updated for this long is flagged"
            hint="An open concept not updated for this long is flagged stale; 0 turns it off."
          >
            {m.stale_after === 0 ? "off" : days(m.stale_after)}
            {m.stale_after_defaulted && <Default />}
          </Row>
        ),
        (m.harvest_after !== undefined || m.harvest_off) && (
          <Row
            key="harvest"
            label="Harvest after"
            k="harvest_after"
            note="a closed entry this old is proposed for harvest"
            hint="A closed journal entry this old is proposed for harvest: its lasting facts move to live pages."
          >
            {m.harvest_off || !m.harvest_after ? "off" : days(m.harvest_after)}
            {m.harvest_after_defaulted && <Default />}
          </Row>
        ),
      ],
    },
    {
      title: "Review & work",
      intro: "Where knowledge from here is promoted, and where its work is tracked.",
      rows: [
        !!m.promote_to && (
          <Row
            key="promote"
            label="Promote to"
            k="promote_to"
            note="where reusable procedures from here belong"
            hint="Where this journal's reusable procedures belong: a procedure not linked into it is proposed for promotion."
          >
            <code>{m.promote_to}</code>
          </Row>
        ),
        has(m.procedure_headings) && (
          <Row
            key="procedures"
            label="Procedures"
            k="procedure_headings"
            note="H2 prefixes that mark a procedure"
            hint="H2 prefixes that mark a section as a procedure, matched ignoring case and accents."
          >
            <Chips items={m.procedure_headings!} />
          </Row>
        ),
        !!m.glossary && (
          <Row
            key="glossary"
            label="Glossary"
            k="glossary"
            note="a term used anywhere is never a glossary gap"
            hint="This Map defines the KB's terms: a term used anywhere is never reported as a glossary gap."
          >
            <Flag>defines the KB's terms</Flag>
          </Row>
        ),
        !!m.work_map && (
          <Row
            key="work"
            label="Work goes to"
            k="work_map"
            note="open items here should link into it"
            hint="Where this Map's work belongs: a page here with open items and no link into it is scattered work."
          >
            <code>{m.work_map}</code>
          </Row>
        ),
      ],
    },
    {
      title: "Index",
      intro: "How the Map's index.md is kept.",
      rows: [
        !!m.index && (
          <Row
            key="index"
            label="Concept list"
            k="index"
            note={m.index === "generated" ? "the server rewrites it on every write" : "written by hand"}
            hint="generated: the server keeps the Map's concept list in a marked block of its index.md."
          >
            {m.index}
          </Row>
        ),
        !!m.require_index_entry && (
          <Row
            key="entry"
            label="Index entry"
            k="require_index_entry"
            note="every concept must be listed in index.md"
            hint="Every concept here must be listed in the Map's index.md."
          >
            <Flag>required</Flag>
          </Row>
        ),
      ],
    },
    {
      title: "Lint",
      intro: "Checks this Map relaxes or tunes; anything unset keeps the built-in default.",
      rows: [
        has(m.lint_ignore) && (
          <Row
            key="ignore"
            label="Accepted checks"
            k="lint_ignore"
            note="raise no findings in this Map"
            hint="Checks accepted for the whole Map: they raise no findings here."
          >
            <Chips items={m.lint_ignore!} code />
          </Row>
        ),
        t?.repeated_fact_min !== undefined && (
          <Row
            key="repeated"
            label="Repeated fact"
            k="repeated_fact_min"
            note="pages sharing a line before it is reported (default 3)"
            hint="Concepts that must carry the same line before it is reported as a repeated fact."
          >
            {t.repeated_fact_min} pages
          </Row>
        ),
        t?.hotspot_in_degree !== undefined && (
          <Row
            key="hot_links"
            label="Hotspot links"
            k="hotspot_in_degree"
            note="inbound links of a read hotspot (default 50)"
            hint="Inbound links that, with hotspot_bytes, make a concept a read hotspot."
          >
            {t.hotspot_in_degree}
          </Row>
        ),
        t?.hotspot_bytes !== undefined && (
          <Row
            key="hot_bytes"
            label="Hotspot size"
            k="hotspot_bytes"
            note="body size of a read hotspot (default 16384)"
            hint="Body size that, with hotspot_in_degree, makes a concept a read hotspot."
          >
            {t.hotspot_bytes} bytes
          </Row>
        ),
        t?.oversize_bytes !== undefined && (
          <Row
            key="over_bytes"
            label="Oversize page"
            k="oversize_bytes"
            note="a body above this is flagged"
            hint="A concept body above this size is flagged as oversize (default: half the 60 KB read guard)."
          >
            {t.oversize_bytes} bytes
          </Row>
        ),
        t?.oversize_concepts !== undefined && (
          <Row
            key="over_concepts"
            label="Oversize Map"
            k="oversize_concepts"
            note="top-level concepts above which the Map is flagged (default 50)"
            hint="Top-level concepts above which the Map itself is flagged as oversize."
          >
            {t.oversize_concepts}
          </Row>
        ),
        t?.title_max_length !== undefined && (
          <Row
            key="title_len"
            label="Title length"
            k="title_max_length"
            note="longer titles are flagged (default 100)"
            hint="A title longer than this many characters is flagged; 0 turns the rule off."
          >
            {t.title_max_length === 0 ? "off" : `${t.title_max_length} characters`}
          </Row>
        ),
        has(t?.forbidden_title_terms) && (
          <Row
            key="title_terms"
            label="Not in titles"
            k="forbidden_title_terms"
            hint="Substrings no title here may carry, matched ignoring case."
          >
            <Chips items={t!.forbidden_title_terms!} code />
          </Row>
        ),
        has(m.machine_path_allow_prefixes) && (
          <Row
            key="paths"
            label="Machine paths"
            k="machine_path_allow_prefixes"
            note="read as this Map's own paths, not a client's"
            hint="Absolute path prefixes read as this Map's operational paths, not client-local ones."
          >
            <Chips items={m.machine_path_allow_prefixes!} code />
          </Row>
        ),
      ],
    },
  ];
  return all.map((s) => ({ ...s, rows: s.rows.filter(Boolean) })).filter((s) => s.rows.length > 0);
}

const has = (v?: unknown[] | null): boolean => !!v && v.length > 0;

/**
 * One contract entry: the label and, under it, the key as written in
 * `_map.md`; the value beside them and, under it, a short note when the value
 * needs one. The full explanation is the tooltip.
 */
function Row({
  label,
  code = false,
  k,
  note,
  hint,
  children,
}: {
  label: string;
  /** The label is itself a field name. */
  code?: boolean;
  /** The key in `_map.md`; none for a value the server derives. */
  k?: string;
  note?: string;
  hint: string;
  children: ReactNode;
}) {
  return (
    <div className="mapcontract__row" title={hint}>
      <dt>
        {code ? (
          <code className="mapcontract__label">{label}</code>
        ) : (
          <span className="mapcontract__label">{label}</span>
        )}
        {k && (
          <code className="mapcontract__key">
            {/* A long key breaks after a dot, never inside a word. */}
            {k.split(".").map((part, i) => (
              <Fragment key={i}>
                {i > 0 && (
                  <>
                    .<wbr />
                  </>
                )}
                {part}
              </Fragment>
            ))}
          </code>
        )}
      </dt>
      <dd>
        <div className="mapcontract__value">{children}</div>
        {note && <p className="mapcontract__note">{note}</p>}
      </dd>
    </div>
  );
}

function Chips({ items, code = false }: { items: string[]; code?: boolean }) {
  return (
    <ul className="mapcontract__chips">
      {items.map((item) => (
        <li key={item} className={code ? "mapcontract__chip mapcontract__chip--code" : "mapcontract__chip"}>
          {item}
        </li>
      ))}
    </ul>
  );
}

const Flag = ({ children }: { children: ReactNode }) => <span className="mapcontract__flag">{children}</span>;
const Default = () => <span className="mapcontract__default"> (default)</span>;
