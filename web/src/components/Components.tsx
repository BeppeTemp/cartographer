import { useEffect, useState } from "react";
import { fetchComponents } from "../api/client";
import type { ComponentAlarm, ComponentRow, ComponentRun, ComponentsResponse, ReplicaRow } from "../api/types";
import { DocsLink } from "./DocsLink";
import { Facet, Page, PageHeader } from "./Page";
import { age } from "./Surveyors";

/** Liveness changes without a KB write, so the page re-reads on its own beat. */
const REFRESH_MS = 60_000;

/**
 * What the registry and the live replicas say about the KB (D1901). Read-only
 * and optional like the strip on Work: a failure or a 404 is "nothing to show",
 * never an error page.
 */
function useComponentsResponse(kb: string, live: number): { data: ComponentsResponse | null; settled: boolean } {
  const [data, setData] = useState<ComponentsResponse | null>(null);
  const [settled, setSettled] = useState(false);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    // Never poll a hidden tab (docs/deployment.md): the next visible beat catches up.
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") setTick((n) => n + 1);
    }, REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [kb]);
  useEffect(() => {
    // Another KB's rows are not this one's: forget them until its own answer lands.
    setData(null);
    setSettled(false);
  }, [kb]);
  useEffect(() => {
    const controller = new AbortController();
    fetchComponents(kb, controller.signal)
      .then((res) => {
        setData(res && Array.isArray(res.components) ? res : null);
        setSettled(true);
      })
      .catch((err: unknown) => {
        if (err instanceof DOMException && err.name === "AbortError") return;
        setData(null);
        setSettled(true);
      });
    return () => controller.abort();
  }, [kb, live, tick]);
  return { data, settled };
}

/** The skew of a replica in words, against the protocol the server speaks. */
export function skewWords(skew: string, serverProtocol: number): string {
  if (skew === "behind") return "one behind";
  if (skew === "ahead") return `one ahead, the server speaks protocol ${serverProtocol}`;
  return "current";
}

const words = (code: string) => code.replace(/_/g, " ");

/** An alarm with no `kb` carries a code and a time only: no empty source or detail is rendered. */
function AlarmItem({ a }: { a: ComponentAlarm }) {
  // Ages are the server's clock (received_at), never the Surveyor's own `since`.
  const at = a.received_at;
  const text = [words(a.code), a.source, a.detail].filter((t) => t && t.trim()).join(": ");
  return (
    <li className="components__alarm">
      {text}
      {at && <span className="components__meta"> · {age(at)}</span>}
    </li>
  );
}

function RunItem({ r }: { r: ComponentRun }) {
  return (
    <li className="components__run" data-outcome={r.outcome}>
      <span>{r.task}</span> <span>{words(r.outcome)}</span>
      <span className="components__meta">
        {" "}
        · {age(r.received_at)} · {Math.round(r.duration_ms / 1000)} s
      </span>
      {r.error && <span className="components__meta"> · {r.error}</span>}
    </li>
  );
}

function Replica({ r, serverProtocol }: { r: ReplicaRow; serverProtocol: number }) {
  return (
    <div className="components__replica" role="group" aria-label={`Replica ${r.id}`}>
      <p className="components__line">
        <strong>{r.id}</strong>
        {r.version ? ` · ${r.version}` : ""} · <span data-state={r.state}>{words(r.state)}</span> · seen{" "}
        {age(r.last_beat_at)}
      </p>
      <p className="components__line components__meta">
        protocol {r.protocol} ({skewWords(r.protocol_skew, serverProtocol)})
        {r.tasks.length > 0 && ` · tasks ${r.tasks.map((t) => t.kind).join(", ")}`}
        {r.last_run && ` · last run ${age(r.last_run.at)}`}
        {r.next_window_at && ` · next window ${r.next_window_at.slice(0, 10)}`}
      </p>
      {r.budgets.length > 0 && (
        <ul className="components__budgets">
          {r.budgets.map((b) => (
            <li key={b.profile + (b.tier ?? "")}>
              {b.profile}
              {b.tier ? ` (${b.tier})` : ""}: {`${b.spent} / ${b.limit}`}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function Block({ c, serverProtocol }: { c: ComponentRow; serverProtocol: number }) {
  const heading = `${c.name} ${c.kind}`;
  return (
    <Facet title={heading} id={`component-${c.kind}-${c.name}`} className="components__block">
      <p className="components__line">
        <span className="components__health" data-state={c.state}>
          {c.state}
        </span>{" "}
        · {c.declared ? "expected" : "undeclared"} · {c.replicas.length} of {c.min_replicas} replica
        {c.min_replicas === 1 ? "" : "s"}
        {c.last_seen_at ? ` · last seen ${age(c.last_seen_at)}` : ""}
      </p>
      {c.replicas.map((r) => (
        <Replica key={r.id} r={r} serverProtocol={serverProtocol} />
      ))}
      {c.alarms.length > 0 && (
        <ul className="components__alarms" aria-label="Alarms">
          {c.alarms.map((a, i) => (
            <AlarmItem key={`${a.code}-${a.origin}-${i}`} a={a} />
          ))}
        </ul>
      )}
      {c.runs.length > 0 && (
        <ul className="components__runs" aria-label="Recent runs">
          {c.runs.map((r) => (
            <RunItem key={r.run_id} r={r} />
          ))}
        </ul>
      )}
    </Facet>
  );
}

/**
 * The Components view: what is deployed for this KB and whether it is healthy.
 * Read-only, nothing here starts a run; a Surveyor's configuration is in
 * Settings. The Atlas is not a row: it is this page.
 */
export function Components({ kb, live }: { kb: string; live?: number }) {
  const { data, settled } = useComponentsResponse(kb, live ?? 0);
  const rows = data?.components ?? [];
  return (
    <Page label="Components" busy={!settled}>
      <PageHeader
        eyebrow="Components"
        title={
          !settled
            ? "Reading the components"
            : rows.length === 0
              ? "No Surveyor is deployed or expected for this KB."
              : `${rows.length} component${rows.length === 1 ? "" : "s"} for this KB.`
        }
        subtitle={
          rows.length === 0 && settled ? (
            <>
              See the Surveyor section of the deployment guide. <DocsLink doc="docs/deployment.md#the-surveyor-a-third-role" />
            </>
          ) : undefined
        }
      />
      {data && (
        <Facet title="Meridian" id="component-meridian">
          <p className="components__line">
            {/* The upper bound is componentproto's Skew rule (N+1 is served as N), not a field of the answer. */}
            {data.server.version ? `version ${data.server.version} · ` : ""}started {age(data.server.started_at)} · protocol{" "}
            {data.protocol}, serves {data.min_protocol} to {data.protocol + 1}
          </p>
        </Facet>
      )}
      {rows.map((c) => (
        <Block key={`${c.kind}/${c.name}`} c={c} serverProtocol={data?.protocol ?? 0} />
      ))}
    </Page>
  );
}
