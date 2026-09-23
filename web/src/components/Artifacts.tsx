import { useCallback, useEffect, useMemo, useState, type CSSProperties } from "react";
import { fetchArtifact } from "../api/client";
import type { Artifact, ArtifactFile, ArtifactList } from "../api/types";
import { readWidth, writeWidth } from "../lib/panels";
import { Markdown } from "./Markdown";
import { Splitter } from "./Splitter";
import { EmptyState, ErrorState, Skeleton } from "./States";

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
}: {
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
    return KINDS.map(([kind, title]) => ({
      kind,
      title,
      items: (list?.artifacts ?? []).filter((a) => a.kind === kind && match(a)),
    })).filter((g) => g.items.length > 0);
  }, [list, filter]);

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
    <div id="artifact-list" className="artifacts__list">
      <header className="artifacts__intro">
        <p className="observatory__eyebrow">Artifacts</p>
        <h1 className="artifacts__title">
          {list.artifacts.length} artifact{list.artifacts.length === 1 ? "" : "s"} ship with this KB
        </h1>
      </header>
      {list.issues.length > 0 && (
        <ul className="artifacts__issues" aria-label="Artifacts left out">
          {list.issues.map((issue) => (
            <li key={issue}>{issue}</li>
          ))}
        </ul>
      )}
      <input
        className="input artifacts__filter"
        type="search"
        placeholder="Filter by name or description"
        aria-label="Filter artifacts"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
      />
      {groups.length === 0 && <p className="rail__empty">No artifact matches this filter.</p>}
      {groups.map((group) => (
        <section key={group.kind} className="artifacts__group" aria-label={group.title}>
          <h2 className="observatory__group-title">
            {group.title}
            <span className="observatory__group-count">{list.counts[group.kind] ?? group.items.length}</span>
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
                    <span className="artifacts__item-name">{a.name}</span>
                    {a.description && <span className="artifacts__item-desc">{a.description}</span>}
                  </button>
                </li>
              );
            })}
          </ul>
        </section>
      ))}
    </div>
  );

  const detail = selected ? (
    <ArtifactDetail
      key={`${kb}:${selected}`}
      kb={kb}
      id={selected}
      onBack={narrow ? () => onSelect(null) : undefined}
      onFailure={onFailure}
    />
  ) : (
    <div className="artifacts__placeholder">
      <p className="state__detail">Select an artifact to read its files.</p>
    </div>
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
}: {
  kb: string;
  id: string;
  onBack?: () => void;
  onFailure(err: unknown): boolean;
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
        <p className="observatory__eyebrow">{artifact.kind}</p>
        <h2 className="artifacts__name">{artifact.name}</h2>
        {artifact.description && <p className="artifacts__desc">{artifact.description}</p>}
        <dl className="artifacts__meta">
          {artifact.signed !== undefined && (
            <div>
              <dt>Signature</dt>
              <dd>{artifact.signed ? "Signed" : "Unsigned"}</dd>
            </div>
          )}
          {artifact.content_hash && (
            <div>
              <dt>Content hash</dt>
              <dd>
                <code title={artifact.content_hash}>{artifact.content_hash.slice(0, 12)}</code>
              </dd>
            </div>
          )}
          <div>
            <dt>Clients</dt>
            <dd>
              {artifact.clients.length === 0 ? (
                <span className="artifacts__none">Stays in the KB</span>
              ) : (
                <ul className="rail__chips">
                  {artifact.clients.map((c) => (
                    <li key={c.id} className="chip chip--static">
                      {c.name || c.id}
                    </li>
                  ))}
                </ul>
              )}
            </dd>
          </div>
        </dl>
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
          <FileContent file={current} />
        </div>
      )}
    </article>
  );
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

function FileContent({ file }: { file: ArtifactFile }) {
  if (file.binary) return <p className="artifacts__notice">Binary file — not shown.</p>;
  if (file.truncated) return <p className="artifacts__notice">Larger than 256 KiB — not shown.</p>;
  const text = file.content ?? "";
  if (!file.path.endsWith(".md")) return <pre className="artifacts__pre">{text}</pre>;
  const split = splitFrontmatter(text);
  return (
    <>
      {split && split.fields.length > 0 && (
        <table className="artifacts__frontmatter">
          <tbody>
            {split.fields.map(([key, value], i) => (
              <tr key={`${key}-${i}`}>
                <th scope="row">{key}</th>
                <td>{value}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <Markdown>{split ? split.body : text}</Markdown>
    </>
  );
}
