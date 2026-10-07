import { useCallback, useEffect, useMemo, useState, type CSSProperties } from "react";
import { fetchArtifact } from "../api/client";
import type { Artifact, ArtifactFile, ArtifactFinding, ArtifactList } from "../api/types";
import { readWidth, writeWidth } from "../lib/panels";
import { Markdown } from "./Markdown";
import { Count, Facet, Quiet } from "./Page";
import { SeverityBadge } from "./SeverityBadge";
import { Splitter } from "./Splitter";
import { EmptyState, ErrorState, Skeleton } from "./States";

/** What each kind is, said once where the overview introduces it. */
const KIND_ABOUT: Record<string, string> = {
  skill: "Procedures an agent loads when its description matches the task.",
  agent: "Subagents a client can hand a task to.",
  hook: "Commands a client runs on its own events.",
  mcp: "MCP servers the KB asks its clients to connect.",
  instructions: "The KB's standing orders, written into each client's instruction file.",
  template: "The shape a new concept of a type starts from.",
};

/** Kinds in reading order: what an agent does, then what it is told. */
const KINDS: [string, string][] = [
  ["skill", "Skills"],
  ["agent", "Subagents"],
  ["hook", "Hooks"],
  ["mcp", "MCP servers"],
  ["instructions", "Instructions"],
  ["template", "Templates"],
];

const LIST_DEFAULT = 320;
const LIST_MIN = 240;
const LIST_MAX = 560;

export const artifactId = (a: Pick<Artifact, "kind" | "name">) => `${a.kind}/${a.name}`;

const SEVERITIES = ["error", "warning", "info"];

/** Past this many days without a use, an artifact is flagged (the server's default, D326). */
const STALE_DAYS = 42;
const ACTIVE_DAYS = 7;

/**
 * The "Last used" cell of a skill or agent (D326): a label that says it in
 * words — colour only reinforces it — and its tone. Null for kinds the scanner
 * does not follow (hooks, MCP descriptors, instructions, templates).
 */
export function lastUsed(a: Artifact): { text: string; tone: "active" | "normal" | "stale" | "never" } | null {
  if (a.kind !== "skill" && a.kind !== "agent") return null;
  if (a.last_used === undefined) return null; // an older server: say nothing rather than "never"
  const provider = a.last_used_provider ? ` (${a.last_used_provider})` : "";
  if (!a.last_used || a.last_used_days_ago == null) return { text: "never used", tone: "never" };
  const days = a.last_used_days_ago;
  const when = days === 0 ? "today" : days === 1 ? "1 day ago" : `${days} days ago`;
  if (a.last_used_catalog_only) return { text: `catalogue loaded ${when}${provider}, no use seen`, tone: "stale" };
  const tone = days <= ACTIVE_DAYS ? "active" : days > STALE_DAYS ? "stale" : "normal";
  return { text: `${when}${provider}`, tone };
}

/** The most severe level among an artifact's findings. */
function worstSeverity(findings: ArtifactFinding[]): string {
  return SEVERITIES.find((s) => findings.some((f) => f.severity === s)) ?? "info";
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  return `${(n / 1024 / 1024).toFixed(1)} MiB`;
}

/**
 * The Artifacts panel (D238): what this KB ships to agent clients, for a
 * human to read. A list grouped by kind on one side, the selected artifact's
 * metadata and files on the other; the selection is URL state, so Back and a
 * shared link both work. Read-only, like the rest of the UI API.
 */
export function Artifacts({
  kb,
  list,
  loading,
  error,
  selected,
  narrow,
  onSelect,
  onRetry,
  onFailure,
  titleOf = () => undefined,
  typeCount = () => 0,
  onOpenConcept,
  onFilterType,
}: {
  titleOf?(id: string): string | undefined;
  /** How many concepts carry a type: a template's reach. */
  typeCount?(type: string): number;
  onOpenConcept?(id: string): void;
  /** Opens the atlas filtered to one concept type. */
  onFilterType?(type: string): void;
  kb: string;
  list: ArtifactList | null;
  loading: boolean;
  error: unknown;
  selected: string | null;
  narrow: boolean;
  onSelect(id: string | null): void;
  onRetry(): void;
  /** True when the failure was handled (a 401 sends the user to sign in). */
  onFailure(err: unknown): boolean;
}) {
  const [filter, setFilter] = useState("");
  // One kind at a time, or all of them: a KB with twenty skills must not make
  // its two hooks a scroll away.
  const [kind, setKind] = useState<string | null>(null);
  const [width, setWidth] = useState(() =>
    Math.min(LIST_MAX, Math.max(LIST_MIN, readWidth("artifacts.width", LIST_DEFAULT))),
  );
  const commitWidth = useCallback((px: number) => {
    setWidth(px);
    writeWidth("artifacts.width", px);
  }, []);

  const groups = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    const match = (a: Artifact) =>
      !needle || a.name.toLowerCase().includes(needle) || (a.description ?? "").toLowerCase().includes(needle);
    return KINDS.filter(([k]) => kind === null || k === kind)
      .map(([k, title]) => ({
        kind: k,
        title,
        items: (list?.artifacts ?? []).filter((a) => a.kind === k && match(a)),
      }))
      .filter((g) => g.items.length > 0);
  }, [list, filter, kind]);
  // "never used" on every row says nothing when no use was ever reported (no
  // client scanner): the overview says it once instead.
  const anyUse = (list?.artifacts ?? []).some((a) => !!a.last_used);

  if (loading && !list) return <Skeleton lines={6} label="Loading artifacts" />;
  if (error) return <ErrorState error={error} onRetry={onRetry} />;
  if (!list) return null;
  if (list.artifacts.length === 0 && list.issues.length === 0) {
    return (
      <EmptyState
        title="This KB ships no artifacts"
        detail="Skills, subagents, hooks, MCP servers, instructions and templates appear here once the KB has them."
      />
    );
  }

  const listPane = (
    <nav id="artifact-list" className="artifacts__list" aria-label="Artifacts by kind">
      <div className="artifacts__controls">
        <header className="artifacts__intro">
          <p className="page__eyebrow">Artifacts</p>
          <h1 className="page__title">
            <Count>{list.artifacts.length}</Count> artifact{list.artifacts.length === 1 ? "" : "s"} ship with this KB
          </h1>
          <FindingSummary list={list} />
        </header>
        <input
          className="input artifacts__filter"
          type="search"
          placeholder="Filter by name or description"
          aria-label="Filter artifacts"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        />
        <ul className="artifacts__kinds" aria-label="Show kind">
          <li>
            <button type="button" className="chip" aria-pressed={kind === null} onClick={() => setKind(null)}>
              All <span className="chip__count">{list.artifacts.length}</span>
            </button>
          </li>
          {KINDS.filter(([k]) => (list.counts[k] ?? 0) > 0).map(([k, title]) => (
            <li key={k}>
              <button
                type="button"
                className="chip"
                aria-pressed={kind === k}
                onClick={() => setKind(kind === k ? null : k)}
              >
                {title} <span className="chip__count">{list.counts[k]}</span>
              </button>
            </li>
          ))}
        </ul>
      </div>
      {list.issues.length > 0 && (
        <ul className="artifacts__issues" aria-label="Artifacts left out">
          {list.issues.map((issue) => (
            <li key={issue}>{issue}</li>
          ))}
        </ul>
      )}
      {groups.length === 0 && <p className="rail__empty">No artifact matches this filter.</p>}
      {groups.map((group) => (
        <section key={group.kind} className="artifacts__group" aria-label={group.title}>
          <h2 className="artifacts__group-title">
            {group.title} <span className="page-section__count">{list.counts[group.kind] ?? group.items.length}</span>
          </h2>
          <ul className="artifacts__items">
            {group.items.map((a) => {
              const id = artifactId(a);
              return (
                <li key={id}>
                  <button
                    type="button"
                    className="artifacts__item"
                    aria-current={selected === id ? "true" : undefined}
                    onClick={() => onSelect(id)}
                  >
                    <span className="artifacts__item-head">
                      <span className="artifacts__item-name">{a.name}</span>
                      {!!a.findings?.length && (
                        <SeverityBadge severity={worstSeverity(a.findings)} count={a.findings.length} />
                      )}
                      {anyUse && <LastUsed artifact={a} />}
                    </span>
                    {a.description && (
                      <span className="artifacts__item-desc" title={a.description}>
                        {a.description}
                      </span>
                    )}
                  </button>
                </li>
              );
            })}
          </ul>
        </section>
      ))}
    </nav>
  );

  const detail = selected ? (
    <ArtifactDetail
      key={`${kb}:${selected}`}
      kb={kb}
      id={selected}
      onBack={narrow ? () => onSelect(null) : undefined}
      onFailure={onFailure}
      titleOf={titleOf}
      typeCount={typeCount}
      onOpenConcept={onOpenConcept}
      onFilterType={onFilterType}
    />
  ) : (
    <Overview list={list} onSelect={onSelect} onKind={setKind} />
  );

  if (narrow) {
    return <section className="artifacts artifacts--narrow" aria-label="Artifacts">{selected ? detail : listPane}</section>;
  }
  return (
    <section
      className="artifacts"
      aria-label="Artifacts"
      style={{ "--artifacts-list-width": `${width}px` } as CSSProperties}
    >
      <div className="artifacts__side">
        {listPane}
        <Splitter
          value={width}
          min={LIST_MIN}
          max={LIST_MAX}
          defaultValue={LIST_DEFAULT}
          edge="end"
          controls="artifact-list"
          label="Resize artifact list"
          onChange={setWidth}
          onCommit={commitWidth}
        />
      </div>
      {detail}
    </section>
  );
}

function ArtifactDetail({
  kb,
  id,
  onBack,
  onFailure,
  titleOf,
  typeCount,
  onOpenConcept,
  onFilterType,
}: {
  kb: string;
  id: string;
  onBack?: () => void;
  onFailure(err: unknown): boolean;
  titleOf(id: string): string | undefined;
  typeCount(type: string): number;
  onOpenConcept?(id: string): void;
  onFilterType?(type: string): void;
}) {
  const [artifact, setArtifact] = useState<Artifact | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [file, setFile] = useState(0);

  useEffect(() => {
    const slash = id.indexOf("/");
    const kind = id.slice(0, slash);
    const name = id.slice(slash + 1);
    const controller = new AbortController();
    fetchArtifact(kb, kind, name, controller.signal)
      .then(setArtifact)
      .catch((err) => {
        if (controller.signal.aborted) return;
        if (!onFailure(err)) setError(err);
      });
    return () => controller.abort();
  }, [kb, id, onFailure]);

  const back = onBack && (
    <button type="button" className="button artifacts__back" onClick={onBack}>
      Back to artifacts
    </button>
  );
  if (error) {
    return (
      <div className="artifacts__detail">
        {back}
        <ErrorState error={error} />
      </div>
    );
  }
  if (!artifact) {
    return (
      <div className="artifacts__detail">
        {back}
        <Skeleton lines={4} label="Loading the artifact" />
      </div>
    );
  }

  const current = artifact.files[Math.min(file, artifact.files.length - 1)];
  return (
    <article className="artifacts__detail" aria-label={`Artifact ${id}`}>
      {back}
      <header className="artifacts__head">
        <p className="page__eyebrow">{artifact.kind}</p>
        <h2 className="artifacts__name">{artifact.name}</h2>
        {artifact.description && <p className="artifacts__desc">{artifact.description}</p>}
        {/* The facts in one line of pills, the clients as a sentence: a
            table of four rows was the heaviest thing on the page. */}
        <div className="artifacts__facts">
          {artifact.signed !== undefined && (
            <span className={artifact.signed ? "pill pill--ok" : "pill"}>{artifact.signed ? "Signed" : "Unsigned"}</span>
          )}
          {artifact.content_hash && (
            <span className="pill" title={`Content hash ${artifact.content_hash}`}>
              <code>{artifact.content_hash.slice(0, 12)}</code>
            </span>
          )}
          {lastUsed(artifact) && (
            <span className="pill artifacts__used-pill">
              <span className="artifacts__fact-label">Last used</span> <LastUsed artifact={artifact} />
              {artifact.last_used && (
                <>
                  {" "}
                  <time dateTime={artifact.last_used}>{artifact.last_used}</time>
                </>
              )}
            </span>
          )}
        </div>
        <p className="artifacts__clients">
          <span className="artifacts__fact-label">Synced to</span>{" "}
          {artifact.clients.length === 0 ? (
            <span className="artifacts__none">no client: it stays in the KB</span>
          ) : (
            artifact.clients.map((c, i) => (
              <span key={c.id}>
                {i > 0 && <span aria-hidden="true"> · </span>}
                <span className="artifacts__client">{c.name || c.id}</span>
              </span>
            ))
          )}
        </p>
        {/* Where the artifact meets the atlas: a template shapes every
            concept of its type; a skill or agent points agents at the
            concepts it names. */}
        {artifact.kind === "template" ? (
          typeCount(artifact.name) > 0 && (
            <p className="artifacts__reach">
              <button type="button" className="button" onClick={() => onFilterType?.(artifact.name)}>
                Show the {typeCount(artifact.name)} “{artifact.name}” concepts on the atlas
              </button>
            </p>
          )
        ) : (
          !!artifact.concepts?.length && (
            <section className="artifacts__reach" aria-label="Concepts it reads">
              <h3 className="inspector__group-title">
                Concepts it reads <span className="inspector__count">{artifact.concepts.length}</span>
              </h3>
              <ul className="inspector__links">
                {artifact.concepts.map((cid) => (
                  <li key={cid}>
                    <button type="button" className="inspector__link" onClick={() => onOpenConcept?.(cid)}>
                      <span className="inspector__link-text">
                        <span className="inspector__link-title">{titleOf(cid) ?? cid.split("/").pop()}</span>
                        <span className="inspector__link-id">{cid}</span>
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            </section>
          )
        )}
      </header>

      {artifact.files.length > 1 && (
        <div className="artifacts__files" role="tablist" aria-label="Files">
          {artifact.files.map((f, i) => (
            <button
              key={f.path}
              type="button"
              role="tab"
              id={`artifact-file-${i}`}
              aria-selected={f === current}
              aria-controls="artifact-file-panel"
              className="artifacts__file-tab"
              onClick={() => setFile(i)}
            >
              {f.path.split("/").pop()}
            </button>
          ))}
        </div>
      )}
      {current && (
        <div
          id="artifact-file-panel"
          className="artifacts__file"
          role={artifact.files.length > 1 ? "tabpanel" : undefined}
          aria-labelledby={artifact.files.length > 1 ? `artifact-file-${artifact.files.indexOf(current)}` : undefined}
        >
          <p className="artifacts__path">
            <code>{current.path}</code> · {formatBytes(current.size)}
            {current.executable && " · executable"}
          </p>
          <FileContent file={current} known={{ name: artifact.name, description: artifact.description ?? "" }} />
        </div>
      )}
      {!!artifact.findings?.length && (
        <details className="artifacts__findings" open>
          <summary>
            Findings <span className="inspector__count">{artifact.findings.length}</span>
          </summary>
          <ul aria-label="Artifact findings">
            {artifact.findings.map((f, i) => (
              <li key={`${f.check}-${i}`}>
                <SeverityBadge severity={f.severity} /> <code>{f.check}</code> {f.message}
              </li>
            ))}
          </ul>
        </details>
      )}
    </article>
  );
}

function LastUsed({ artifact }: { artifact: Artifact }) {
  const used = lastUsed(artifact);
  if (!used) return null;
  return <span className={`artifacts__used artifacts__used--${used.tone}`}>{used.text}</span>;
}

/** Frontmatter as key/value rows, never rendered as Markdown. */
function splitFrontmatter(text: string): { fields: [string, string][]; body: string } | null {
  const m = /^---\r?\n([\s\S]*?)\r?\n---\r?\n?/.exec(text);
  if (!m) return null;
  const fields: [string, string][] = [];
  for (const line of m[1]!.split(/\r?\n/)) {
    const at = line.indexOf(":");
    if (at > 0 && !/^\s/.test(line)) fields.push([line.slice(0, at).trim(), line.slice(at + 1).trim()]);
    else if (fields.length > 0 && line.trim()) fields[fields.length - 1]![1] += `\n${line.trim()}`;
  }
  return { fields, body: text.slice(m[0].length) };
}

/** A YAML scalar as a reader wants it: without the quotes YAML needed. */
function unquote(v: string): string {
  const t = v.trim();
  return t.length >= 2 && ((t[0] === '"' && t.endsWith('"')) || (t[0] === "'" && t.endsWith("'"))) ? t.slice(1, -1) : t;
}

/** A value that is a list — `[a, b]`, `- a` lines, or a long comma run — as its items. */
function listOf(v: string): string[] | null {
  const t = v.trim();
  if (t.startsWith("[") && t.endsWith("]")) return t.slice(1, -1).split(",").map((x) => unquote(x)).filter(Boolean);
  const lines = t.split("\n").map((x) => x.trim());
  if (lines.length > 1 && lines.every((x) => x.startsWith("- "))) return lines.map((x) => unquote(x.slice(2)));
  const parts = t.split(/,\s*/);
  if (parts.length > 3 && parts.every((x) => /^[\w.:/@-]+$/.test(x))) return parts;
  return null;
}

const LIST_PREVIEW = 8;

/** A list value as chips, folded past a few: forty tool names are not a paragraph. */
function ListValue({ items }: { items: string[] }) {
  const [open, setOpen] = useState(false);
  const shown = open ? items : items.slice(0, LIST_PREVIEW);
  return (
    <ul className="artifacts__values">
      {shown.map((x, i) => (
        <li key={`${x}-${i}`}>
          <code>{x}</code>
        </li>
      ))}
      {items.length > LIST_PREVIEW && (
        <li>
          <button type="button" className="chip chip--more" onClick={() => setOpen(!open)}>
            {open ? "Show fewer" : `+${items.length - LIST_PREVIEW} more`}
          </button>
        </li>
      )}
    </ul>
  );
}

/** A nested value as YAML, folded to a few lines until asked for. */
function BlockValue({ text }: { text: string }) {
  const [open, setOpen] = useState(false);
  const long = text.split("\n").length > 4 || text.length > 320;
  return (
    <div className="artifacts__block" data-open={open || !long}>
      <pre>{text.replace(/^\n+/, "")}</pre>
      {long && (
        <button type="button" className="chip chip--more" onClick={() => setOpen(!open)}>
          {open ? "Show less" : "Show all"}
        </button>
      )}
    </div>
  );
}

/**
 * A file's content. Frontmatter fields the header already shows (name,
 * description) are left out of the table rather than said twice; list values
 * become chips.
 */
function FileContent({ file, known = {} }: { file: ArtifactFile; known?: Record<string, string> }) {
  if (file.binary) return <p className="artifacts__notice">Binary file — not shown.</p>;
  if (file.truncated) return <p className="artifacts__notice">Larger than 256 KiB — not shown.</p>;
  const text = file.content ?? "";
  if (!file.path.endsWith(".md")) return <pre className="artifacts__pre">{text}</pre>;
  const split = splitFrontmatter(text);
  const fields = (split?.fields ?? []).filter(([key, value]) => known[key] === undefined || known[key] !== unquote(value));
  return (
    <>
      {fields.length > 0 && (
        <table className="artifacts__frontmatter">
          <tbody>
            {fields.map(([key, value], i) => {
              const items = listOf(value);
              return (
                <tr key={`${key}-${i}`}>
                  <th scope="row">{key}</th>
                  <td>
                    {items ? (
                      <ListValue items={items} />
                    ) : value.includes("\n") ? (
                      <BlockValue text={value} />
                    ) : (
                      unquote(value)
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      <Markdown>{split ? split.body : text}</Markdown>
    </>
  );
}

/** The panel's health line (D316): every artifact finding, by severity. */
function FindingSummary({ list }: { list: ArtifactList }) {
  const bySeverity = list.finding_severities ?? {};
  const total = Object.values(bySeverity).reduce((a, b) => a + b, 0);
  if (total === 0) return <p className="artifacts__health">No artifact findings.</p>;
  return (
    <p className="artifacts__health">
      {SEVERITIES.filter((s) => (bySeverity[s] ?? 0) > 0).map((s, i) => (
        <span key={s}>
          {i > 0 && <span aria-hidden="true"> · </span>}
          <SeverityBadge severity={s} count={bySeverity[s]} />
        </span>
      ))}
      <span>
        {" "}
        — {total} finding{total === 1 ? "" : "s"} across artifacts
      </span>
    </p>
  );
}

/**
 * Before anything is selected, the other side answers what the list cannot:
 * which skills and agents are used, which sit idle, and which carry findings —
 * each a way into its artifact. The counts per kind are on the list's own
 * filter, so they are not repeated here.
 */
function Overview({
  list,
  onSelect,
  onKind,
}: {
  list: ArtifactList;
  onSelect(id: string): void;
  onKind(kind: string): void;
}) {
  const tracked = list.artifacts.filter((a) => lastUsed(a) !== null);
  const anyUse = tracked.some((a) => !!a.last_used);
  const used = tracked
    .filter((a) => a.last_used_days_ago != null && !a.last_used_catalog_only)
    .sort((a, b) => (a.last_used_days_ago ?? 0) - (b.last_used_days_ago ?? 0));
  const idle = tracked.filter((a) => {
    const tone = lastUsed(a)?.tone;
    return tone === "never" || tone === "stale";
  });
  const flagged = list.artifacts.filter((a) => a.findings?.length);
  const chip = (a: Artifact, extra?: string) => (
    <li key={artifactId(a)}>
      <button type="button" className="chip" onClick={() => onSelect(artifactId(a))}>
        {a.name}
        {extra && <span className="chip__count">{extra}</span>}
      </button>
    </li>
  );
  const PREVIEW = 8;
  return (
    <div className="artifacts__overview" aria-label="Artifacts overview" role="region">
      <p className="artifacts__overview-lead">Select an artifact to read its files.</p>
      {KINDS.filter(([k]) => (list.counts[k] ?? 0) > 0).map(([k, title]) => {
        const items = list.artifacts.filter((a) => a.kind === k);
        return (
          <Facet key={k} title={`${title} · ${list.counts[k]}`} id={`artifacts-kind-${k}`} className="artifacts__kind">
            <p className="page-note artifacts__facet-note">{KIND_ABOUT[k]}</p>
            <ul className="artifacts__chips" aria-label={title}>
              {items.slice(0, PREVIEW).map((a) => chip(a))}
              {items.length > PREVIEW && (
                <li>
                  <button type="button" className="chip chip--more" onClick={() => onKind(k)}>
                    +{items.length - PREVIEW} more
                  </button>
                </li>
              )}
            </ul>
          </Facet>
        );
      })}
      {tracked.length > 0 && !anyUse && (
        <Quiet tone="neutral">No skill or agent use has been reported yet: usage appears once a client reports it.</Quiet>
      )}
      {anyUse && (
        <Facet title={`Recently used · ${used.length}`} id="artifacts-used">
          <ul className="artifacts__chips" aria-label="Recently used">
            {used.slice(0, 12).map((a) => chip(a, a.last_used_days_ago === 0 ? "today" : `${a.last_used_days_ago}d`))}
          </ul>
        </Facet>
      )}
      {anyUse && idle.length > 0 && (
        <Facet title={`Idle · ${idle.length}`} id="artifacts-idle">
          <p className="page-note artifacts__facet-note">
            Never used, or not in the last {STALE_DAYS} days: candidates to improve or retire.
          </p>
          <ul className="artifacts__chips" aria-label="Idle artifacts">
            {idle.map((a) => chip(a))}
          </ul>
        </Facet>
      )}
      {flagged.length > 0 && (
        <Facet title={`With findings · ${flagged.length}`} id="artifacts-flagged">
          <ul className="artifacts__chips" aria-label="Artifacts with findings">
            {flagged.map((a) => chip(a, String(a.findings!.length)))}
          </ul>
        </Facet>
      )}
    </div>
  );
}
