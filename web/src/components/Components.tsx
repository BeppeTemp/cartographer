/**
 * The live status blocks of Settings' tabs: what Meridian, Compass and the
 * Surveyor are doing now, from the components the server and its replicas report.
 */
import type { ComponentAlarm, ComponentRow, ComponentRun, ReplicaRow, SettingsClient } from "../api/types";
import { DocsLink } from "./DocsLink";
import { Facet } from "./Page";
import { age, duration, words } from "../lib/time";
import { useComponentsResponse } from "./Surveyors";

/** The skew of a replica in words, against the protocol the server speaks. */
export function skewWords(skew: string, serverProtocol: number): string {
  if (skew === "behind") return "one behind";
  if (skew === "ahead") return `one ahead, the server speaks protocol ${serverProtocol}`;
  return "current";
}

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
        · {age(r.received_at)} · {duration(r.duration_ms)}
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
  // A name may hold spaces: the id is what aria-labelledby points at.
  const id = `component-${c.kind}-${c.name}`.toLowerCase().replace(/[^a-z0-9_-]+/g, "-").replace(/^-+|-+$/g, "");
  return (
    <Facet title={heading} id={id} className="components__block">
      {c.kind === "surveyor" && (
        <p className="components__role">
          <span className="settings__tab-role">agent</span> keeps the KB up to date on its own: polls feeds, ingests, repairs.
        </p>
      )}
      <p className="components__line">
        <span className="components__health" data-state={c.state}>
          {c.state}
        </span>
        {/* "undeclared" is already the state: saying it twice read as a stutter. */}
        {c.declared ? " · expected" : c.state === "undeclared" ? "" : " · undeclared"} ·{" "}
        {c.min_replicas > 0
          ? `${c.replicas.length} of ${c.min_replicas} replica${c.min_replicas === 1 ? "" : "s"}`
          : `${c.replicas.length} replica${c.replicas.length === 1 ? "" : "s"} running`}
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

/** The live status line of a component: a dot, its name and what it is doing. */
function StatusLine({ state, name, role, status }: { state: string; name: string; role: string; status: string }) {
  return (
    <p className="components-status__line" data-state={state}>
      <span className="components-status__dot" aria-hidden="true" />
      <strong>{name}</strong>
      <span className="settings__tab-role">{role}</span>
      <span className="components-status__text">{status}</span>
    </p>
  );
}

/**
 * The live status of the server, shown at the top of its Settings tab: what
 * runs now, read-only. A failed read
 * or a 404 (an older server) renders nothing: the configuration below stays.
 */
export function MeridianStatus({ kb, live }: { kb: string; live?: number }) {
  const { data, settled } = useComponentsResponse(kb, live ?? 0);
  if (!settled || !data) return null;
  return (
    <section className="components-status" aria-label="Meridian status">
      <StatusLine
        state="ok"
        name="Meridian"
        role="server"
        status={`Running${data.server.version ? ` ${data.server.version}` : ""} · up ${age(data.server.started_at, { span: true })}`}
      />
      <Facet title="Meridian" id="component-meridian">
        <p className="components__line">
          {/* The upper bound is componentproto's Skew rule (N+1 is served as N), not a field of the answer. */}
          {data.server.version ? `version ${data.server.version} · ` : ""}started {age(data.server.started_at)} · protocol{" "}
          {data.protocol}, serves {data.min_protocol} to {data.protocol + 1}
        </p>
      </Facet>
    </section>
  );
}

/** The machines whose Compass reported (D818), given by Settings which has read them. */
export function CompassStatus({ machines }: { machines: SettingsClient[] }) {
  return (
    <section className="components-status" aria-label="Compass status">
      <StatusLine
        state={machines.length ? "ok" : "none"}
        name="Compass"
        role="client"
        status={
          machines.length === 0
            ? "No machine connected"
            : machines.length === 1
              ? `Connected from ${machines[0]!.machine}`
              : `Connected from ${machines.length} machines`
        }
      />
      <Facet title="Compass" id="component-compass">
        {machines.length === 0 ? (
          <p className="components__line">No machine has reported yet: a Compass reports at each sync.</p>
        ) : (
          <ul className="components__machines" aria-label="Machines">
            {machines.map((c) => (
              <li key={c.machine} className="components__line">
                <span className="plugins__machine">{c.machine}</span> · version {c.version} · last sync {age(c.received_at)}
                {c.providers?.length ? ` · ${c.providers.length} agent${c.providers.length === 1 ? "" : "s"}` : ""}
              </li>
            ))}
          </ul>
        )}
      </Facet>
    </section>
  );
}

/**
 * The Surveyor deployed for the KB: replicas, runs, alarms, budgets. On a
 * perimeter's tab only that perimeter's block (every one when none matches).
 */
export function SurveyorStatus({ kb, live, perimeter }: { kb: string; live?: number; perimeter?: string | null }) {
  const { data, settled } = useComponentsResponse(kb, live ?? 0);
  if (!settled || !data) return null;
  const all = data.components.filter((c) => c.kind === "surveyor");
  const mine = perimeter ? all.filter((c) => c.name === perimeter) : [];
  const surveyors = mine.length ? mine : all;
  const others = data.components.filter((c) => c.kind !== "surveyor");
  const worst = surveyors.find((c) => c.state !== "up" && c.state !== "undeclared");
  return (
    <section className="components-status" aria-label="Surveyor status">
      <StatusLine
        state={surveyors.length === 0 ? "none" : worst ? "warn" : "ok"}
        name="Surveyor"
        role="agent"
        status={
          surveyors.length === 0
            ? "Not deployed"
            : worst
              ? `${worst.name}: ${worst.state}`
              : `${surveyors.map((c) => c.name).join(", ")}: running`
        }
      />
      {surveyors.length === 0 ? (
        <Facet title="Surveyor" id="component-surveyor">
          <p className="components__line">
            None for this KB: a Surveyor polls the KB&apos;s feeds, ingests new sources and repairs it without an agent
            session. <DocsLink doc="docs/deployment.md#the-surveyor-a-third-role" />
          </p>
        </Facet>
      ) : (
        surveyors.map((c) => <Block key={`${c.kind}/${c.name}`} c={c} serverProtocol={data.protocol} />)
      )}
      {others.map((c) => (
        <Block key={`${c.kind}/${c.name}`} c={c} serverProtocol={data.protocol} />
      ))}
    </section>
  );
}
