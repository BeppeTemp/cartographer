import { useState } from "react";
import type { Extensions, ExtensionPlugin } from "../api/types";
import { Sheet } from "./Sheet";
import { ErrorState } from "./States";

const COMPONENTS: [string, string][] = [
  ["meridian", "Meridian"],
  ["compass", "Compass"],
  ["surveyor", "Surveyor"],
];

const TONE: Record<string, string> = {
  approved: "pill--ok",
  pending: "pill--info",
  changed: "pill--warning",
  refused: "pill--error",
};

/** The state as a person reads it; an unknown state is shown as it came, never dropped. */
function label(state: string): string {
  return state === "not_reported" ? "not reported" : state;
}

/**
 * The plugins installed on each component and whether each is approved (D1938). Read-only: approval is a
 * command on the component, never a button here. `operatorOnly` is a narrowed token: the server returns
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
  const [open, setOpen] = useState<ExtensionPlugin | null>(null);
  if (!extensions) return error ? <ErrorState error={error} onRetry={onRetry} /> : <p className="page-note" role="status">Loading plugins.</p>;
  const plugins = extensions.plugins;
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
  if (plugins.length === 0)
    return (
      <>
        {stale}
        <p className="page-note" role="status">
          {operatorOnly ? "Visible to the operator only." : "No plugin installed on any component."}
        </p>
      </>
    );
  return (
    <>
      {stale}
      <table className="plugins__table" aria-label="Installed plugins">
        <thead>
          <tr>
            <th scope="col">Plugin</th>
            {COMPONENTS.map(([, name]) => (
              <th key={name} scope="col">
                {name}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {plugins.map((p) => (
            <tr key={p.name}>
              <th scope="row">
                <button type="button" className="button" onClick={() => setOpen(p)}>
                  {p.display_name || p.name} {p.version}
                </button>
              </th>
              {COMPONENTS.map(([id, name]) => {
                const cells = p.components.filter((c) => c.component === id);
                return (
                  <td key={id} aria-label={`${p.display_name || p.name} on ${name}`}>
                    {cells.length === 0
                      ? "-"
                      : cells.map((c, i) => (
                          <div key={i}>
                            <span className={`pill ${TONE[c.state] ?? ""}`}>{label(c.state)}</span>
                            {c.digest && (
                              <code title={c.digest} className="plugins__digest">
                                {c.digest.slice(0, 12)}
                              </code>
                            )}
                            {c.state === "changed" && <div>installed tree differs from the approved one</div>}
                          </div>
                        ))}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
      {open && <PluginSheet plugin={open} extensions={extensions} onClose={() => setOpen(null)} />}
    </>
  );
}

/** What the plugin's Atlas side draws or opens, shown before anyone clicks it: URLs are text here, not links. */
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
