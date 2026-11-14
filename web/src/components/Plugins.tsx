import { useEffect, useState } from "react";
import type { ExtensionComponent, Extensions, ExtensionPlugin } from "../api/types";
import { Sheet } from "./Sheet";
import { ErrorState } from "./States";

/** Each component: its id on the wire, its name, and what it is. */
const COMPONENTS: [string, string, string][] = [
  ["meridian", "Meridian", "server"],
  ["compass", "Compass", "client"],
  ["surveyor", "Surveyor", "agent"],
];

const TONE: Record<string, string> = {
  approved: "pill--ok",
  active: "pill--ok",
  "pending approval": "pill--info",
  modified: "pill--warning",
  invalid: "pill--error",
  pending: "pill--info",
  changed: "pill--warning",
  refused: "pill--error",
};

/** The state as a person reads it; an unknown state is shown as it came, never dropped. */
function label(state: string): string {
  return state === "not_reported" ? "not reported" : state;
}

/** How each extension point reads next to a count: [one, many]. An unknown point is shown by its own name. */
const POINTS: Record<string, [string, string]> = {
  providers: ["provider", "providers"],
  agents: ["agent", "agents"],
  subagents: ["subagent", "subagents"],
  skills: ["skill", "skills"],
  hooks: ["hook", "hooks"],
  commands: ["command", "commands"],
  tools: ["MCP tool", "MCP tools"],
  mcp: ["MCP tool", "MCP tools"],
  views: ["view", "views"],
  apps: ["app", "apps"],
  branding: ["branding", "branding"],
  recipes: ["recipe", "recipes"],
  connectors: ["connector", "connectors"],
  roles: ["role", "roles"],
  presets: ["preset", "presets"],
};

/** "2 agents · 3 skills · 1 view": what a plugin adds, from the counts per point. */
export function summarize(contributions: Record<string, number> | undefined): string[] {
  return Object.entries(contributions ?? {})
    .filter(([, n]) => n > 0)
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([point, n]) => {
      const names = POINTS[point];
      if (!names) return `${n} ${point}`;
      return names[0] === names[1] ? names[0] : `${n} ${n === 1 ? names[0] : names[1]}`;
    });
}

/** One plugin as one component holds it. */
interface Entry {
  plugin: ExtensionPlugin;
  comp: ExtensionComponent;
}

/**
 * The plugins installed on each component and whether each is approved (D1938), grouped by component. Read-only:
 * approval is a command on the component, never a button here. `operatorOnly` is a narrowed token: the server returns
 * no plugin to it, and the empty list must not read as "nothing installed".
 */
export function PluginsPanel({
  extensions,
  error,
  onRetry,
  operatorOnly,
}: {
  /** null until the first answer. */
  extensions: Extensions | null;
  /** The last read failed: the list below, if any, is the last good one. */
  error: unknown;
  onRetry(): void;
  operatorOnly: boolean;
}) {
  // The sheet holds the plugin's name, not the object: a refresh shows the current one, and a plugin that left closes it.
  const [openName, setOpenName] = useState<string | null>(null);
  const open = (openName && extensions?.plugins.find((p) => p.name === openName)) || null;
  useEffect(() => {
    if (openName && extensions && !open) setOpenName(null);
  }, [openName, extensions, open]);
  if (!extensions) return error ? <ErrorState error={error} onRetry={onRetry} /> : <p className="page-note" role="status">Loading plugins.</p>;
  const stale = (
    <>
      {error != null && (
        <p className="page-note" role="status">
          Could not refresh the plugins; showing the last loaded.{" "}
          <button type="button" className="button" onClick={onRetry}>
            Retry
          </button>
        </p>
      )}
    </>
  );
  if (operatorOnly && extensions.plugins.length === 0)
    return (
      <>
        {stale}
        <p className="page-note" role="status">
          Visible to the operator only.
        </p>
      </>
    );
  const reported = extensions.reported ?? ["meridian"];
  return (
    <>
      {stale}
      <div className="plugins__sections">
        {COMPONENTS.map(([id, name, role]) => {
          const entries: Entry[] = extensions.plugins.flatMap((plugin) =>
            plugin.components.filter((comp) => comp.component === id).map((comp) => ({ plugin, comp })),
          );
          return <ComponentSection key={id} id={id} name={name} role={role} entries={entries} reported={reported.includes(id)} onOpen={(p) => setOpenName(p.name)} />;
        })}
      </div>
      {open && <PluginSheet plugin={open} extensions={extensions} onClose={() => setOpenName(null)} />}
    </>
  );
}

function ComponentSection({
  id,
  name,
  role,
  entries,
  reported,
  onOpen,
}: {
  id: string;
  name: string;
  role: string;
  entries: Entry[];
  reported: boolean;
  onOpen(p: ExtensionPlugin): void;
}) {
  // A Compass is grouped by machine only when several report; a row without one goes under no heading.
  const machines = [...new Set(entries.map((e) => e.comp.machine ?? ""))].sort();
  const grouped = id === "compass" && machines.filter(Boolean).length > 1;
  return (
    <section className="plugins__section" aria-label={`Plugins on ${name}`}>
      <h3 className="plugins__section-title">
        {name} <span className="settings__tab-role">{role}</span>
      </h3>
      {entries.length === 0 ? (
        <p className="plugins__empty">{reported ? "No plugin installed." : "Not reported."}</p>
      ) : grouped ? (
        machines.map((m) => (
          <div key={m} className="plugins__machine-group">
            <h4 className="plugins__machine">{m || "unnamed machine"}</h4>
            <PluginRows entries={entries.filter((e) => (e.comp.machine ?? "") === m)} component={name} onOpen={onOpen} />
          </div>
        ))
      ) : (
        <PluginRows entries={entries} component={name} onOpen={onOpen} showMachine={id === "compass"} onlyMachine={machines[0]} />
      )}
    </section>
  );
}

function PluginRows({
  entries,
  component,
  onOpen,
  showMachine,
  onlyMachine,
}: {
  entries: Entry[];
  component: string;
  onOpen(p: ExtensionPlugin): void;
  showMachine?: boolean;
  onlyMachine?: string;
}) {
  return (
    <ul className="plugins__list" aria-label={`Plugins on ${component}${showMachine && onlyMachine ? ` (${onlyMachine})` : ""}`}>
      {entries.map(({ plugin: p, comp: c }, i) => {
        const name = p.display_name || p.name;
        const brings = summarize(c.contributions);
        return (
          <li key={`${p.name}-${i}`} className="plugins__card" aria-label={`${name} on ${component}`}>
            <div className="plugins__head">
              <button type="button" className="plugins__name" onClick={() => onOpen(p)}>
                {name} <span className="plugins__version">{c.version || p.version}</span>
              </button>
              <span className={`pill ${TONE[c.state] ?? ""}`}>{label(c.state)}</span>
              {showMachine && onlyMachine && <span className="plugins__machine">{onlyMachine}</span>}
              {c.digest && (
                <code title={c.digest} className="plugins__digest">
                  {c.digest.slice(0, 12)}
                </code>
              )}
              {brings.length > 0 && <span className="plugins__brings">{brings.join(" · ")}</span>}
            </div>
            {p.description && (
              <p className="plugins__desc" title={p.description}>
                {p.description}
              </p>
            )}
            {c.state === "changed" && <p className="plugins__note">installed tree differs from the approved one</p>}
          </li>
        );
      })}
    </ul>
  );
}

/** Everything the Atlas knows of one plugin; the views and apps are shown before anyone clicks them: URLs are text here, not links. */
function PluginSheet({ plugin, extensions, onClose }: { plugin: ExtensionPlugin; extensions: Extensions; onClose(): void }) {
  const views = extensions.views.filter((v) => v.plugin === plugin.name);
  const apps = extensions.apps.filter((a) => a.plugin === plugin.name);
  const name = plugin.display_name || plugin.name;
  return (
    <Sheet side="end" label={`Plugin ${name}`} onClose={onClose}>
      <h2>{name}</h2>
      <p className="page-note">
        {plugin.version} · read-only: approval is a command on the component
      </p>
      {plugin.description && <p>{plugin.description}</p>}
      {COMPONENTS.flatMap(([id, component]) =>
        plugin.components
          .filter((c) => c.component === id)
          .map((c, i) => {
            const brings = summarize(c.contributions);
            return (
              <section key={`${id}-${i}`} className="plugins__detail" aria-label={`${name} on ${component}${c.machine ? ` (${c.machine})` : ""}`}>
                <h3>
                  {component}
                  {c.machine ? ` · ${c.machine}` : ""}
                </h3>
                <dl className="plugins__facts">
                  <dt>State</dt>
                  <dd>
                    <span className={`pill ${TONE[c.state] ?? ""}`}>{label(c.state)}</span>
                  </dd>
                  <dt>Version</dt>
                  <dd>{c.version || plugin.version}</dd>
                  {c.installed && (
                    <>
                      <dt>Installed</dt>
                      <dd>
                        <code className="plugins__full">{c.installed}</code>
                      </dd>
                    </>
                  )}
                  {c.approved && (
                    <>
                      <dt>Approved</dt>
                      <dd>
                        <code className="plugins__full">{c.approved}</code>
                      </dd>
                    </>
                  )}
                  {!c.installed && !c.approved && c.digest && (
                    <>
                      <dt>Digest</dt>
                      <dd>
                        <code className="plugins__full">{c.digest}</code>
                      </dd>
                    </>
                  )}
                  {c.source && (
                    <>
                      <dt>Source</dt>
                      <dd>
                        <code className="plugins__full">{c.source}</code>
                      </dd>
                    </>
                  )}
                  {c.permissions && c.permissions.length > 0 && (
                    <>
                      <dt>Permissions</dt>
                      <dd>{c.permissions.join(", ")}</dd>
                    </>
                  )}
                  {brings.length > 0 && (
                    <>
                      <dt>Adds</dt>
                      <dd>{brings.join(" · ")}</dd>
                    </>
                  )}
                </dl>
              </section>
            );
          }),
      )}
      {views.length === 0 && apps.length === 0 && <p>No approved view or app in the Atlas.</p>}
      {views.length > 0 && (
        <>
          <h3>Views</h3>
          <ul aria-label="Views">
            {views.map((v) => (
              <li key={v.name}>
                {v.title} <code>{v.source.route}</code>
              </li>
            ))}
          </ul>
        </>
      )}
      {apps.length > 0 && (
        <>
          <h3>External apps</h3>
          <ul aria-label="External apps">
            {apps.map((a) => (
              <li key={a.name}>
                {a.title} <code>{a.url}</code>
              </li>
            ))}
          </ul>
        </>
      )}
    </Sheet>
  );
}
