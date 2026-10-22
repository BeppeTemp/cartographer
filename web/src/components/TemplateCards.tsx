import { useEffect, useState, type CSSProperties } from "react";
import { fetchArtifact } from "../api/client";
import type { Artifact } from "../api/types";
import { FindingBadge } from "./FindingBadge";
import { Bone } from "./Page";

/** Frontmatter keys every concept carries: a template setting them says nothing about its type. */
const COMMON_KEYS = new Set(["type", "title", "timestamp", "status", "provenance"]);

/** What a template prescribes, read from its source: the fields beyond the common ones, its one-line brief, its sections. */
export interface TemplateShape {
  fields: string[];
  brief: string | null;
  sections: string[];
}

export function templateShape(text: string): TemplateShape {
  const fm = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?/.exec(text);
  const pairs = fm
    ? fm[1]!
        .split(/\r?\n/)
        .map((l) => /^([A-Za-z_][\w.-]*)\s*:\s*(.*)$/.exec(l))
        .filter((m): m is RegExpExecArray => !!m)
        .map((m) => [m[1]!, m[2]!.trim()] as const)
    : [];
  // A template generated from a map (D352) states its shape in x-template.* keys:
  // its description, and the fields every page of the type must carry.
  const meta = new Map(pairs.filter(([k]) => k.startsWith("x-template.")).map(([k, v]) => [k.slice(11), v]));
  const required = (meta.get("required_fields") ?? "")
    .replace(/^\[|\]$/g, "")
    .split(",")
    .map((f) => f.trim())
    .filter(Boolean);
  const fields = [
    ...pairs.map(([k]) => k).filter((k) => !k.startsWith("x-template.") && !COMMON_KEYS.has(k)),
    ...required.filter((f) => !COMMON_KEYS.has(f)),
  ].filter((f, i, all) => all.indexOf(f) === i);
  const body = fm ? text.slice(fm[0].length) : text;
  const brief =
    /^>\s*(.+)$/m.exec(body)?.[1]?.trim() ?? (meta.get("description")?.replace(/^"|"$/g, "") || null);
  const sections = [...body.matchAll(/^##\s+(.+?)\s*$/gm)].map((m) => m[1]!);
  return { fields, brief, sections };
}

/**
 * The templates as the shapes they are: per type, how many concepts carry it,
 * the one line its template asks for, the fields it adds and the sections a
 * new page starts with. A card opens the template; its count opens the atlas
 * filtered to the type.
 */
export function TemplateCards({
  kb,
  items,
  selected,
  typeCount,
  onSelect,
  onFilterType,
}: {
  kb: string;
  items: Artifact[];
  selected: string | null;
  typeCount(type: string): number;
  onSelect(id: string | null): void;
  onFilterType?(type: string): void;
}) {
  const [shapes, setShapes] = useState<Record<string, TemplateShape | null>>({});
  const names = items.map((a) => a.name).join("\u0000");
  useEffect(() => {
    const controller = new AbortController();
    for (const name of names.split("\u0000").filter(Boolean)) {
      fetchArtifact(kb, "template", name, controller.signal)
        .then((a) => {
          const text = a.files.find((f) => f.path.endsWith(".md"))?.content ?? "";
          setShapes((s) => ({ ...s, [name]: templateShape(text) }));
        })
        .catch(() => {
          // The card keeps its name and count and says it could not read the
          // rest, instead of loading forever; the detail says what failed.
          if (!controller.signal.aborted) setShapes((s) => ({ ...s, [name]: null }));
        });
    }
    return () => controller.abort();
  }, [kb, names]);

  const counted = items.map((a) => ({ a, n: typeCount(a.name) })).sort((x, y) => y.n - x.n || x.a.name.localeCompare(y.a.name));
  const max = Math.max(1, ...counted.map((c) => c.n));
  return (
    <ul className="tpl-cards">
      {counted.map(({ a, n }) => {
        const id = `${a.kind}/${a.name}`;
        const shape = shapes[a.name];
        return (
          <li key={id}>
            <article
              className="tpl-card"
              aria-current={selected === id ? "true" : undefined}
              style={{ "--share": `${(n / max) * 100}%` } as CSSProperties}
            >
              <header className="tpl-card__head">
                <button type="button" className="tpl-card__name" onClick={() => onSelect(selected === id ? null : id)}>
                  {a.name}
                </button>
                {!!a.findings?.length && <FindingBadge count={a.findings.length} />}
                <button
                  type="button"
                  className="tpl-card__count"
                  disabled={n === 0}
                  title={n > 0 ? `Show the ${n} “${a.name}” concepts on the atlas` : "No concept of this type yet"}
                  onClick={() => onFilterType?.(a.name)}
                >
                  <strong>{n}</strong> {n === 1 ? "concept" : "concepts"}
                </button>
              </header>
              <span className="tpl-card__usage" aria-hidden="true" />
              {shape === null ? (
                <div className="tpl-card__body">
                  <p className="tpl-card__bare">Could not read this template: open it to see why.</p>
                </div>
              ) : !shape ? (
                <div className="tpl-card__body">
                  <Bone line w="70%" />
                  <Bone line w="45%" />
                </div>
              ) : (
                <div className="tpl-card__body">
                  {shape.brief && <p className="tpl-card__brief">{shape.brief}</p>}
                  {shape.fields.length > 0 && (
                    <ul className="tpl-card__fields" aria-label="Fields it adds">
                      {shape.fields.map((f) => (
                        <li key={f}>
                          <code>{f}</code>
                        </li>
                      ))}
                    </ul>
                  )}
                  {shape.sections.length === 0 && (
                    <p className="tpl-card__bare">Only a title: a new page starts empty.</p>
                  )}
                  {shape.sections.length > 0 && (
                    <ol className="tpl-card__sections" aria-label="Sections a new page starts with">
                      {shape.sections.map((s, i) => (
                        <li key={`${s}-${i}`}>{s}</li>
                      ))}
                    </ol>
                  )}
                </div>
              )}
            </article>
          </li>
        );
      })}
    </ul>
  );
}
