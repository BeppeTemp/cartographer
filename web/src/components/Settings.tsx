import { useEffect, useState } from "react";
import { fetchSettings, fetchWhoami } from "../api/client";
import type { SettingEntry, SettingsKB, SettingsView, Whoami, WhoamiRule } from "../api/types";
import { Count, Page, PageHeader } from "./Page";
import { ErrorState, Skeleton } from "./States";

/** A value as a person reads it: lists joined, empty said out loud. */
export function formatValue(value: unknown): string {
  if (Array.isArray(value)) return value.length === 0 ? "(none)" : value.join(", ");
  if (value === "" || value === null || value === undefined) return "(empty)";
  return String(value);
}

/**
 * The Settings panel (D743): how this server is configured, read-only. Every
 * key shows its effective value, the layer that set it (default, file, env or
 * flag), what it does, and whether changing it needs a restart. A secret is
 * redacted by the server and arrives as "(set)" or "(unset)"; nothing here can
 * change a setting.
 */
export function Settings({
  reloadKey,
  onRetry,
  onFailure,
}: {
  /** Changes when the server moves under the page: the settings are read again. */
  reloadKey: string;
  onRetry(): void;
  onFailure(err: unknown): boolean;
}) {
  const [view, setView] = useState<SettingsView | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);

  // Who is looking: a read of the caller's own policy, left out when it fails.
  const [who, setWho] = useState<Whoami | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    fetchWhoami(controller.signal)
      .then(setWho)
      .catch(() => undefined);
    return () => controller.abort();
  }, [reloadKey]);

  useEffect(() => {
    const controller = new AbortController();
    fetchSettings(controller.signal)
      .then((data) => {
        setView(data);
        setError(null);
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted || onFailure(err)) return;
        setError(err);
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
    // onFailure is a fresh closure each render of the shell; the reload key is the trigger.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reloadKey]);

  if (loading && !view) return <Skeleton lines={6} label="Loading settings" />;
  if (error) return <ErrorState error={error} onRetry={onRetry} />;
  if (!view) return null;

  const set = view.server.filter((e) => e.origin !== "default").length;
  return (
    <Page label="Settings" className="settings">
      <PageHeader
        eyebrow="Settings"
        title={
          <>
            <Count>{set}</Count> of <Count>{view.server.length}</Count> server settings are set; the rest use their
            default.
          </>
        }
        subtitle={
          <>
            Version {view.version}
            {view.latest_version ? `, ${view.latest_version} is available` : ""}
            {view.update_channel ? `, installed by ${view.update_channel}` : ""}. Read-only: change a setting in the
            config file, the environment or a flag.
          </>
        }
      />
      {who && <WhoSection who={who} />}
      <Section id="settings-server" title="Server" entries={view.server} />
      {view.kbs.map((kb, i) => (
        <KBSection key={kb.name} id={`settings-kb-${i}`} kb={kb} />
      ))}
    </Page>
  );
}

function KBSection({ id, kb }: { id: string; kb: SettingsKB }) {
  const git = kb.git;
  return (
    <Section id={id} title={`KB ${kb.name}`} entries={kb.entries}>
      {git && (
        <p className="settings__facts">
          <span>{git.has_remote ? "Has a remote" : "No remote"}</span>
          <span>sync {git.sync ? "on" : "off"}</span>
          {git.push_state && <span>push {git.push_state}</span>}
          {git.unpushed_commits ? <span>{git.unpushed_commits} unpushed</span> : null}
          {git.behind_commits ? <span>{git.behind_commits} behind</span> : null}
          {git.push_last_error && <span role="alert">last push error: {git.push_last_error}</span>}
        </p>
      )}
    </Section>
  );
}

function Section({
  id,
  title,
  entries,
  children,
}: {
  id: string;
  title: string;
  entries: SettingEntry[];
  children?: React.ReactNode;
}) {
  return (
    <section className="settings__section" aria-labelledby={id}>
      <h2 id={id} className="settings__title">
        {title}
      </h2>
      {children}
      <ul className="settings__rows" aria-label={`${title} settings`}>
        {entries.map((e) => (
          <Row key={e.key} entry={e} />
        ))}
      </ul>
    </section>
  );
}

function Row({ entry }: { entry: SettingEntry }) {
  return (
    <li className="settings__row">
      <details>
        <summary className="settings__summary">
          <code className="settings__key">{entry.key}</code>
          <span className="settings__value" aria-label={`Value of ${entry.key}`}>
            {formatValue(entry.value)}
          </span>
          <span className={`settings__origin settings__origin--${entry.origin}`}>{entry.origin}</span>
          {entry.restart && <span className="settings__restart">restart required</span>}
        </summary>
        <div className="settings__detail">
          <p>{entry.description}</p>
          <p className="settings__meta">
            Default: <code>{entry.default}</code>
            {entry.env ? (
              <>
                {" "}
                Env: <code>{entry.env}</code>
              </>
            ) : null}
            {entry.flag ? (
              <>
                {" "}
                Flag: <code>{entry.flag}</code>
              </>
            ) : null}
          </p>
        </div>
      </details>
    </li>
  );
}

/** What a rule lets the principal reach in one KB, in plain words. */
function describeRule(rule: WhoamiRule): string {
  const scope = [
    rule.maps?.length ? `the maps ${rule.maps.join(", ")}` : "every map",
    rule.journals?.length ? `the journals ${rule.journals.join(", ")}` : null,
    rule.types?.length ? `only concepts of type ${rule.types.join(", ")}` : null,
  ]
    .filter(Boolean)
    .join("; ");
  return `${rule.write ? "can read and write" : "can read"} ${scope}`;
}

function WhoSection({ who }: { who: Whoami }) {
  return (
    <section className="settings__section" aria-labelledby="settings-who">
      <h2 id="settings-who" className="settings__title">
        Who is looking
      </h2>
      <p className="page-note">
        {who.id ? (
          <>
            You are <strong>{who.id}</strong>
            {who.admin ? ", an administrator." : "."}
          </>
        ) : who.admin ? (
          "You are an unnamed principal with administrator rights (the server runs without authentication)."
        ) : (
          "You are an unnamed principal."
        )}
      </p>
      {who.admin ? (
        <p className="page-note">An administrator reads and writes every KB this server mounts.</p>
      ) : who.permissions.length === 0 ? (
        <p className="page-note">Your token grants no KB on this server.</p>
      ) : (
        <ul className="settings__facts">
          {who.permissions.map((rule) => (
            <li key={rule.kb}>
              In <code>{rule.kb}</code> you {describeRule(rule)}.
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
