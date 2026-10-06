import { useEffect, useMemo, useState } from "react";
import { fetchWork } from "../api/client";
import type { WorkEntry, WorkResponse } from "../api/types";

type Layout = "status" | "map";

/**
 * Open work (D302): concepts in an open status for their map, of any type,
 * and unchecked items in any concept. A read-only view of `work_list`: work
 * changes through the agents' ordinary write tools, so there is no drag here.
 */
export function Work({ kb, onOpen }: { kb: string; onOpen(conceptId: string): void }) {
  const [data, setData] = useState<WorkResponse | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [layout, setLayout] = useState<Layout>("status");
  const [map, setMap] = useState("");
  const [status, setStatus] = useState("");
  const [staleOnly, setStaleOnly] = useState(false);
  const [text, setText] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    setData(null);
    setError(null);
    fetchWork(kb, controller.signal)
      .then(setData)
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(err instanceof Error ? err.message : String(err));
      });
    return () => controller.abort();
  }, [kb]);

  const entries = useMemo(() => {
    const needle = text.trim().toLowerCase();
    return (data?.entries ?? []).filter(
      (e) =>
        (!map || e.map === map) &&
        (!status || groupOf(e) === status) &&
        (!staleOnly || e.stale) &&
        (!needle ||
          (e.title ?? e.id).toLowerCase().includes(needle) ||
          e.items.some((i) => i.text.toLowerCase().includes(needle))),
    );
  }, [data, map, status, staleOnly, text]);

  const groups = useMemo(() => {
    const out = new Map<string, WorkEntry[]>();
    for (const e of entries) {
      const key = layout === "status" ? groupOf(e) : e.map || "(root)";
      out.set(key, [...(out.get(key) ?? []), e]);
    }
    return [...out.entries()].sort((a, b) => a[0].localeCompare(b[0]));
  }, [entries, layout]);

  const maps = Object.keys(data?.by_map ?? {}).sort();
  const statuses = Object.keys(data?.by_status ?? {}).sort();
  if (data?.entries.some((e) => !e.open_phase)) statuses.push(ITEMS_ONLY);

  return (
    <section className="activity work" aria-label="Open work">
      <header className="activity__head">
        <div>
          <p className="observatory__eyebrow">Work</p>
          <h1 className="observatory__title">{headline(data)}</h1>
        </div>
        <div className="legend__switch activity__window" role="group" aria-label="Group by">
          {(["status", "map"] as const).map((v) => (
            <button key={v} type="button" className="legend__option" aria-pressed={layout === v} onClick={() => setLayout(v)}>
              By {v}
            </button>
          ))}
        </div>
      </header>
      <p className="work__note">
        A concept is open work when its status is listed in the map&apos;s open_statuses, or is one of the default open statuses (open,
        in-progress, blocked, decision-needed, proposed). &quot;active&quot; means the page is valid, not that work is pending.
      </p>

      {data && data.total > 0 && (
        <div className="work__filters">
          <select aria-label="Map" value={map} onChange={(e) => setMap(e.target.value)}>
            <option value="">All maps</option>
            {maps.map((m) => (
              <option key={m} value={m}>
                {m || "(root)"}
              </option>
            ))}
          </select>
          <select aria-label="Status" value={status} onChange={(e) => setStatus(e.target.value)}>
            <option value="">All statuses</option>
            {statuses.map((s) => (
              <option key={s} value={s}>
                {s}
              </option>
            ))}
          </select>
          <label className="work__stale">
            <input type="checkbox" checked={staleOnly} onChange={(e) => setStaleOnly(e.target.checked)} /> Stale only
          </label>
          <input
            type="search"
            aria-label="Filter work"
            placeholder="Filter title and items"
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
        </div>
      )}

      {error ? (
        <p className="activity__note">Could not read the work list: {error}</p>
      ) : !data ? (
        <p className="activity__note">Reading the work list…</p>
      ) : data.total === 0 ? (
        <p className="activity__note">No open work: no concept in an open status and no unchecked item.</p>
      ) : entries.length === 0 ? (
        <p className="activity__note">Nothing matches these filters.</p>
      ) : (
        <div className={layout === "status" ? "work__columns" : "work__groups"}>
          {groups.map(([key, list]) => (
            <section key={key} className="work__group" aria-label={`${key}, ${list.length}`}>
              <h2 className="work__group-title">
                {key} <span className="chip__count">{list.length}</span>
              </h2>
              <ul className="activity__list work__list">
                {list.map((e) => (
                  <li key={e.id}>
                    <WorkCard entry={e} onOpen={onOpen} />
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </div>
      )}
      {data && data.next_offset !== undefined && (
        <p className="activity__note activity__foot">
          Showing the first {data.entries.length} of {data.total}.
        </p>
      )}
    </section>
  );
}

function WorkCard({ entry, onOpen }: { entry: WorkEntry; onOpen(id: string): void }) {
  const [open, setOpen] = useState(false);
  const n = entry.items.length;
  return (
    <div className="work__card">
      <button type="button" className="activity__row" onClick={() => onOpen(entry.id)}>
        <span className="activity__row-title">{entry.title || entry.id}</span>
        <span className="activity__row-meta">
          {[entry.type, statusOf(entry), entry.age_days !== undefined ? `${entry.age_days}d` : null]
            .filter(Boolean)
            .join(" · ")}
          {entry.stale && <span className="work__stale-badge"> stale</span>}
        </span>
      </button>
      {n > 0 && (
        <button type="button" className="work__items-toggle" aria-expanded={open} onClick={() => setOpen(!open)}>
          {n} open item{n === 1 ? "" : "s"}
        </button>
      )}
      {open && (
        <ul className="work__items">
          {entry.items.map((i) => (
            <li key={i.line}>
              {i.text}
              {i.section && <span className="activity__row-meta"> — {i.section}</span>}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function statusOf(e: WorkEntry): string {
  return e.status || "none";
}

/**
 * A concept listed only for its unchecked items is grouped apart: its own
 * status is closed (done, active…), and showing it as a status column would
 * read as open work in that state.
 */
const ITEMS_ONLY = "unchecked items";

function groupOf(e: WorkEntry): string {
  return e.open_phase ? statusOf(e) : ITEMS_ONLY;
}

function headline(data: WorkResponse | null): string {
  if (!data) return "Reading the work list…";
  if (data.total === 0) return "Nothing is open.";
  const concepts = data.total === 1 ? "One concept carries" : `${data.total} concepts carry`;
  return `${concepts} open work, ${data.open_items} unchecked item${data.open_items === 1 ? "" : "s"}.`;
}
