import { useEffect, useRef, useState } from "react";
import { fetchPlaceholders, fetchSettings, fetchWhoami } from "../api/client";
import type {
  ClientPlaceholder,
  ClientProvider,
  PlaceholderList,
  SettingEntry,
  SettingOrigin,
  SettingsClient,
  SettingsKB,
  SettingsView,
  Whoami,
  WhoamiRule,
} from "../api/types";
import { Page, PageHeader, Segmented } from "./Page";
import { ErrorState, Skeleton } from "./States";

/** A value as a person reads it: lists joined, empty said out loud. */
export function formatValue(value: unknown): string {
  if (Array.isArray(value)) return value.length === 0 ? "(none)" : value.join(", ");
  if (value === "" || value === null || value === undefined) return "(empty)";
  return String(value);
}

/** A schema default (a YAML literal) as a person reads it. */
function formatDefault(literal: string): string {
  if (!literal || literal === '""') return "(empty)";
  if (literal === "[]" || literal === "{}") return "(none)";
  return literal;
}

/** Where a value comes from, in words. */
const ORIGIN_LABEL: Record<SettingOrigin, string> = {
  default: "default",
  file: "config file",
  env: "environment",
  flag: "flag",
  kb: "KB file",
};

interface Group {
  title: string;
  /** One sentence: what the group governs. */
  intro: string;
}

/** The server's keys, grouped by their first path segment. */
const SERVER_GROUPS: Record<string, Group> = {
  general: { title: "General", intro: "Where the server listens, what it mounts and whether it looks for updates." },
  auth: { title: "Access", intro: "Who may call the server, with which token, and what each role reaches." },
  git: { title: "Git", intro: "How every write becomes a commit, and how commits reach the remotes." },
  audit: { title: "Audit", intro: "The tamper-evident log of every call the server answers." },
  sops: { title: "Secrets", intro: "How the server decrypts the KBs' sops-encrypted secrets." },
  web: { title: "Atlas", intro: "The web UI this page is part of." },
};

/** A KB's keys, grouped by what they govern. */
const KB_GROUPS: [Group, (key: string) => boolean][] = [
  [
    { title: "Mount", intro: "Where the KB lives, its name, and the remote it follows." },
    (k) => ["path", "name", "remote"].includes(k),
  ],
  [
    { title: "Git", intro: "How this KB's commits are authored and where they land." },
    (k) => /^(ssh_key|known_hosts|author_|committer_|git_|github_)/.test(k),
  ],
  [
    { title: "Maintenance", intro: "When an unused skill or agent is reported." },
    (k) => /^usage_stale_days/.test(k),
  ],
  [{ title: "Secrets & signing", intro: "How this KB's secrets are decrypted and its artifacts signed." }, () => true],
];

/** The client's keys (.cartographer.yaml), grouped by what they govern. */
const CLIENT_GROUPS: [Group, (key: string) => boolean][] = [
  [
    { title: "Connection", intro: "Which server this machine talks to, how it authenticates, and whose name its writes carry." },
    (k) => ["server_url", "server_name", "auth", "token", "identities"].includes(k),
  ],
  [
    { title: "Agents & KBs", intro: "The agent clients connected, and which KBs each one receives where." },
    (k) => ["clients", "scopes", "workspaces"].includes(k),
  ],
  [
    { title: "Local paths", intro: "How {{repo:…}} and {{path:…}} placeholders resolve on this machine." },
    (k) => ["search_roots", "paths", "ignored_paths"].includes(k),
  ],
  [
    { title: "Trust", intro: "The signing keys pinned and the MCP descriptors approved on this machine." },
    (k) => ["signing_keys", "mcp_approvals"].includes(k),
  ],
  [{ title: "Updates & sync", intro: "Release checks and usage reports." }, () => true],
];

type Mode = "readable" | "yaml";

/**
 * The Settings page (D743, D818): how this server and the KBs the viewer can
 * see are configured, read-only. One tab for the server and one per KB; each
 * reads two ways. Readable: the keys grouped by what they govern, each with
 * its effective value, where that value comes from, what it does, its default,
 * the values it accepts and how to override it. YAML: the configuration in
 * force as the one file it amounts to, every key documented by the schema
 * (D747), the ones left at their default commented out. A secret is redacted
 * by the server; nothing here changes a setting, agents and operators do.
 */
export function Settings({
  reloadKey,
  activeKB,
  onOpenReleases,
  onRetry,
  onFailure,
}: {
  /** Changes when the server moves under the page: the settings are read again. */
  reloadKey: string;
  /** The version in the title opens the release history (D862). */
  onOpenReleases?(): void;
  /** The KB picked in the top bar: its tab comes first after the server's. */
  activeKB?: string;
  onRetry(): void;
  onFailure(err: unknown): boolean;
}) {
  const [view, setView] = useState<SettingsView | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const [tab, setTab] = useState("server");
  const [mode, setMode] = useState<Mode>("readable");
  const [query, setQuery] = useState("");

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

  // The KB picked in the top bar, and only that one: another KB's settings
  // are a pick away.
  const selected = view.kbs.find((k) => k.name === activeKB) ?? view.kbs[0];
  const kb = tab === "kb" ? selected : undefined;
  const clients = view.clients ?? [];
  const client = tab.startsWith("client") ? (clients.find((c) => `client:${c.machine}` === tab) ?? null) : undefined;
  const current = client !== undefined ? tab : kb ? "kb" : "server";

  return (
    <Page label="Settings" className="settings">
      <PageHeader
        eyebrow="Settings"
        title={
          <>
            Cartographer{" "}
            <button
              type="button"
              className="page__version"
              title="Release history"
              aria-haspopup="dialog"
              onClick={onOpenReleases}
            >
              {view.version}
            </button>
            {view.latest_version ? ` · ${view.latest_version} available` : ""}
          </>
        }
        subtitle={
          <>
            The configuration this server runs with, read-only
            {who && (
              <>
                {" · "}
                <Who who={who} />
              </>
            )}
          </>
        }
        actions={
          <>
            {mode === "readable" && (
              <input
                className="page-search"
                type="search"
                aria-label="Search settings"
                placeholder="Search settings"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            )}
            <Segmented<Mode>
              label="Show as"
              value={mode}
              options={[
                ["readable", "Readable"],
                ["yaml", "YAML"],
              ]}
              onChange={setMode}
            />
          </>
        }
      />

      <div className="settings__bar">
        <div className="inspector__tabs settings__tabs" role="tablist" aria-label="Settings of">
          {clients.length === 0 ? (
            <Tab id="client" current={current === "client"} onSelect={setTab} entries={[]}>
              Client
            </Tab>
          ) : (
            clients.map((c) => (
              <Tab
                key={c.machine}
                id={`client:${c.machine}`}
                current={current === `client:${c.machine}`}
                onSelect={setTab}
                entries={c.entries}
              >
                {clients.length === 1 ? "Client" : `Client · ${c.machine}`}
              </Tab>
            ))
          )}
          <Tab id="server" current={current === "server"} onSelect={setTab} entries={view.server}>
            Server
          </Tab>
          {selected && (
            <Tab id="kb" current={current === "kb"} onSelect={setTab} entries={selected.entries}>
              Knowledge Base
            </Tab>
          )}
        </div>
      </div>

      <div role="tabpanel" aria-labelledby={`settings-tab-${current}`}>
        {client !== undefined ? (
          <ClientPanel client={client} mode={mode} query={query} activeKB={selected?.name} />
        ) : kb ? (
          <KBPanel kb={kb} mode={mode} query={query} />
        ) : (
          <ServerPanel entries={view.server} yaml={view.server_yaml} mode={mode} query={query} />
        )}
      </div>
    </Page>
  );
}

/** A tab, with how many keys it holds. */
function Tab({
  id,
  current,
  onSelect,
  entries,
  children,
}: {
  id: string;
  current: boolean;
  onSelect(id: string): void;
  entries: SettingEntry[];
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      role="tab"
      id={`settings-tab-${id}`}
      aria-selected={current}
      className="inspector__tab"
      onClick={() => onSelect(id)}
    >
      {children}
      {entries.length > 0 && <span className="settings__tab-count">{entries.length}</span>}
    </button>
  );
}

function ServerPanel({
  entries,
  yaml,
  mode,
  query,
}: {
  entries: SettingEntry[];
  yaml?: string;
  mode: Mode;
  query: string;
}) {
  const groups = new Map<string, SettingEntry[]>();
  for (const e of entries.filter((e) => matches(e, query))) {
    const g = e.key.includes(".") ? e.key.slice(0, e.key.indexOf(".")) : "general";
    groups.set(g, [...(groups.get(g) ?? []), e]);
  }
  return (
    <>
      {mode === "yaml" ? (
        <Yaml name="config.yaml" text={yaml} />
      ) : groups.size === 0 ? (
        <NoMatch query={query} />
      ) : (
        [...groups].map(([g, rows]) => (
          <Rows
            key={g}
            open={!!query}
            group={SERVER_GROUPS[g] ?? { title: g, intro: "" }}
            entries={rows}
            label={`${SERVER_GROUPS[g]?.title ?? g} settings`}
          />
        ))
      )}
    </>
  );
}

function KBPanel({ kb, mode, query }: { kb: SettingsKB; mode: Mode; query: string }) {
  const groups = KB_GROUPS.map(([group]) => ({ group, rows: [] as SettingEntry[] }));
  for (const e of kb.entries.filter((e) => matches(e, query))) {
    const key = shortKey(e.key);
    const i = KB_GROUPS.findIndex(([, match]) => match(key));
    groups[i]!.rows.push(e);
  }
  return (
    <>
      {mode === "readable" && <Placeholders kb={kb.name} open={!!query} query={query} />}
      {mode === "yaml" ? (
        <Yaml name={`${kb.name}/settings.yaml`} text={kb.yaml} />
      ) : groups.every((g) => g.rows.length === 0) ? (
        <NoMatch query={query} />
      ) : (
        groups
          .filter((g) => g.rows.length > 0)
          .map(({ group, rows }) => (
            <Rows
              key={group.title}
              open={!!query}
              group={group}
              entries={rows}
              label={`KB ${kb.name} ${group.title} settings`}
            />
          ))
      )}
    </>
  );
}

/**
 * The viewer's own client on one machine, as its last sync reported it
 * (D818). The server keeps it for the principal that sent it and shows it to
 * that principal only.
 */
function ClientPanel({
  client,
  mode,
  query,
  activeKB,
}: {
  client: SettingsClient | null;
  mode: Mode;
  query: string;
  activeKB?: string;
}) {
  if (!client) {
    return (
      <p className="page-note settings__nomatch">
        Your client has not reported its settings to this server yet. They arrive with its next{" "}
        <code>cartographer sync</code>, which the sync timer runs every half hour.
      </p>
    );
  }
  const groups = CLIENT_GROUPS.map(([group]) => ({ group, rows: [] as SettingEntry[] }));
  for (const e of client.entries.filter((e) => matches(e, query))) {
    groups[CLIENT_GROUPS.findIndex(([, match]) => match(e.key))]!.rows.push(e);
  }
  return (
    <>
      <p className="page-note settings__reported">
        {client.machine} · client {client.version} · reported {new Date(client.received_at).toLocaleString("en")}
      </p>
      {mode === "yaml" ? (
        client.yaml ? (
          <Yaml name=".cartographer.yaml" text={client.yaml} />
        ) : (
          <p className="page-note">This machine has no .cartographer.yaml: every key keeps its default.</p>
        )
      ) : (
        <>
          {client.providers && <Workspaces providers={client.providers} activeKB={activeKB} open={!!query} />}
          {client.placeholders && (
            <MachinePaths placeholders={client.placeholders} activeKB={activeKB} open={!!query} query={query} />
          )}
          {groups.every((g) => g.rows.length === 0) ? (
            <NoMatch query={query} />
          ) : (
            groups
              .filter((g) => g.rows.length > 0)
              .map(({ group, rows }) => (
                <Rows
                  key={group.title}
                  open={!!query}
                  group={group}
                  entries={rows}
                  label={`Client ${group.title} settings`}
                />
              ))
          )}
        </>
      )}
    </>
  );
}

/** A KB name, set apart when it is the one picked in the top bar. */
function KBName({ name, activeKB }: { name: string; activeKB?: string }) {
  return <span className={name === activeKB ? "settings__kb settings__kb--active" : "settings__kb"}>{name}</span>;
}

/**
 * Where each agent client receives its KBs: everywhere (provider scope) or per
 * bound folder (workspace scope), and which global MCP entry the client has:
 * the local bridge that narrows a session to its folder's KBs, or the plain
 * HTTP URL (D737). A scope filter, not a security boundary.
 */
function Workspaces({ providers, activeKB, open }: { providers: ClientProvider[]; activeKB?: string; open: boolean }) {
  return (
    <Folded
      title="Workspaces"
      intro="Which KBs each agent client receives, and in which folders."
      count={`${providers.length} client${providers.length === 1 ? "" : "s"}`}
      open={open}
    >
      <ul className="settings__rows" aria-label="Workspaces">
        {providers.map((p) => (
          <li key={p.name} className="settings__row settings__ws">
            <span className="settings__line">
              <code className="settings__key">{p.name}</code>
              <span className="settings__chip-row">
                <span className="pill">{p.scope === "workspace" ? "per workspace" : "every KB, everywhere"}</span>
                {p.isolation === "workspace" && (
                  <span
                    className="pill pill--ok"
                    title="This client starts the Cartographer bridge: a session sees only the KBs bound to its folder."
                  >
                    bridge
                  </span>
                )}
                {p.isolation === "none" && (
                  <span className="pill" title="This client uses the server URL: a session reaches every KB the client is bound to.">
                    http
                  </span>
                )}
              </span>
            </span>
            {(p.scope === "workspace" || !!p.workspaces?.length) &&
              (p.workspaces?.length ? (
                <ul className="settings__ws-list">
                  {p.workspaces.map((w) => (
                    <li key={w.path}>
                      <code className="settings__ws-path" title={w.remote}>
                        {w.path}
                      </code>
                      <span className="settings__ws-kbs">
                        {w.kbs.length ? w.kbs.map((k) => <KBName key={k} name={k} activeKB={activeKB} />) : "no KB"}
                      </span>
                    </li>
                  ))}
                </ul>
              ) : (
                <span className="settings__desc">
                  No folder is bound yet: a session receives only the transversal bundle.
                </span>
              ))}
          </li>
        ))}
      </ul>
    </Folded>
  );
}

/** Where each placeholder the connected KBs cite resolves on this machine. */
function MachinePaths({
  placeholders,
  activeKB,
  open,
  query,
}: {
  placeholders: ClientPlaceholder[];
  activeKB?: string;
  open: boolean;
  query: string;
}) {
  const q = query.trim().toLowerCase();
  const shown = placeholders.filter(
    (p) => !q || [p.key, p.path, p.description].some((t) => t?.toLowerCase().includes(q)),
  );
  if (q && shown.length === 0) return null;
  const unresolved = placeholders.filter((p) => !p.path && !p.ignored).length;
  return (
    <Folded
      title="Paths on this machine"
      intro="Where the {{repo:…}} and {{path:…}} placeholders of the connected KBs resolve here."
      count={unresolved ? `${unresolved} unresolved · ${placeholders.length}` : `${placeholders.length}`}
      open={open}
    >
      {shown.length === 0 ? (
        <p className="page-note">No placeholder recorded: the connected KBs cite none, or no sync ran yet.</p>
      ) : (
        <ul className="settings__rows" aria-label="Paths on this machine">
          {shown.map((p) => (
            <li key={p.key} className="settings__row" data-set={p.path ? true : undefined}>
              <span className="settings__line">
                <code className="settings__key">{`{{${p.key}}}`}</code>
                <span className="settings__val">
                  <span className="settings__value">{p.path ?? (p.ignored ? "ignored" : "unresolved")}</span>
                  <span className="settings__origin">
                    {(p.kbs ?? []).map((k) => (
                      <KBName key={k} name={k} activeKB={activeKB} />
                    ))}
                  </span>
                </span>
              </span>
              {(p.description || (!p.path && p.reason)) && (
                <span className="settings__desc">{!p.path && p.reason ? p.reason : p.description}</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </Folded>
  );
}

/** The placeholder keys this KB declares in paths.yaml or cites. */
function Placeholders({ kb, open, query }: { kb: string; open: boolean; query: string }) {
  const [list, setList] = useState<PlaceholderList | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    setList(null);
    fetchPlaceholders(kb, controller.signal)
      .then(setList)
      .catch(() => setList(null));
    return () => controller.abort();
  }, [kb]);
  if (!list || list.keys.length === 0) return null;
  const q = query.trim().toLowerCase();
  const keys = list.keys.filter(
    (k) => !q || [k.key, k.description, k.default].some((t) => t?.toLowerCase().includes(q)),
  );
  if (q && keys.length === 0) return null;
  return (
    <Folded
      title="Placeholders"
      intro="The {{repo:…}} and {{path:…}} keys this KB declares in paths.yaml or cites; each machine resolves them on its own."
      count={`${list.keys.length}`}
      open={open}
    >
      <ul className="settings__rows" aria-label={`Placeholders of ${kb}`}>
        {keys.map((k) => {
          const uses = k.concepts.length + k.artifacts.length;
          return (
            <li key={`${k.kind}:${k.key}`} className="settings__row" data-set={k.declared ? true : undefined}>
              <span className="settings__line">
                <code className="settings__key">{`{{${k.kind}:${k.key}}}`}</code>
                <span className="settings__val">
                  <span className="settings__value">
                    {k.default ?? k.remote ?? (k.declared ? "(no default)" : "not declared")}
                  </span>
                  <span className="settings__origin">
                    {uses ? `cited ${uses} time${uses === 1 ? "" : "s"}` : "cited nowhere"}
                    {k.conflicts?.length ? ` · ${k.conflicts.map((c) => `${c.kb} says ${c.default}`).join(", ")}` : ""}
                  </span>
                </span>
              </span>
              {k.description && <span className="settings__desc">{k.description}</span>}
            </li>
          );
        })}
      </ul>
    </Folded>
  );
}

/** A folded section with a title, a sentence and a count: the shape of every group. */
function Folded({
  title,
  intro,
  count,
  open,
  children,
}: {
  title: string;
  intro: string;
  count: string;
  open: boolean;
  children: React.ReactNode;
}) {
  return (
    <details className="settings__group" open={open}>
      <summary className="settings__group-head">
        <span className="settings__group-text">
          <h2 className="settings__heading">{title}</h2>
          <span className="settings__intro">{intro}</span>
        </span>
        <span className="settings__group-count">{count}</span>
      </summary>
      {children}
    </details>
  );
}

const shortKey = (key: string) => key.replace(/^kbs\[\]\./, "");

/** A key matches a search by its name, what it does, its value or how it is overridden. */
function matches(e: SettingEntry, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return [e.key, e.description, formatValue(e.value), e.env, e.flag, e.values]
    .filter(Boolean)
    .some((text) => text!.toLowerCase().includes(q));
}

function NoMatch({ query }: { query: string }) {
  return <p className="page-note settings__nomatch">No setting matches “{query.trim()}”.</p>;
}

/** One group, folded until it is opened or a search finds keys in it. Folded,
 *  it still says what it governs and how many keys it holds. */
function Rows({
  group,
  entries,
  label,
  open,
}: {
  group: Group;
  entries: SettingEntry[];
  label: string;
  open: boolean;
}) {
  return (
    <details className="settings__group" open={open}>
      <summary className="settings__group-head">
        <span className="settings__group-text">
          <h2 className="settings__heading">{group.title}</h2>
          {group.intro && <span className="settings__intro">{group.intro}</span>}
        </span>
        <span className="settings__group-count">
          {entries.length} key{entries.length === 1 ? "" : "s"}
        </span>
      </summary>
      <ul className="settings__rows" aria-label={label}>
        {entries.map((e) => (
          <Row key={e.key} entry={e} />
        ))}
      </ul>
    </details>
  );
}

/**
 * One key, on one line: its name, and at the end its value and where the
 * value comes from; under them what it does. Opening the row says the rest:
 * the default, the accepted values, how to override it, and whether a change
 * waits for a restart.
 */
function Row({ entry: e }: { entry: SettingEntry }) {
  const key = shortKey(e.key);
  const set = e.origin !== "default";
  return (
    <li className="settings__row" data-set={set || undefined}>
      <details>
        <summary className="settings__row-head">
          <span className="settings__line">
            <code className="settings__key">{key}</code>
            <span className="settings__val">
              <span className="settings__value" aria-label={`Value of ${e.key}`}>
                {formatValue(e.value)}
              </span>
              <span className={`settings__origin settings__origin--${e.origin}`}>
                {set ? `set by ${ORIGIN_LABEL[e.origin] ?? e.origin} · default ${formatDefault(e.default)}` : "default"}
              </span>
            </span>
          </span>
          {e.description && <span className="settings__desc">{e.description}</span>}
        </summary>
        <dl className="settings__meta">
          <div>
            <dt>Default</dt>
            <dd>
              <code>{e.default || '""'}</code>
            </dd>
          </div>
          {e.values && (
            <div>
              <dt>Accepts</dt>
              <dd>{e.values}</dd>
            </div>
          )}
          {e.env && (
            <div>
              <dt>Environment</dt>
              <dd>
                <code>{e.env}</code>
              </dd>
            </div>
          )}
          {e.flag && (
            <div>
              <dt>Flag</dt>
              <dd>
                <code>{e.flag}</code>
              </dd>
            </div>
          )}
          {e.restart && (
            <div>
              <dt>Applies</dt>
              <dd>after the server restarts: a running server keeps the value it started with</dd>
            </div>
          )}
        </dl>
      </details>
    </li>
  );
}

/**
 * A settings file, highlighted: comments muted, keys set apart from values.
 * Select-all (Cmd/Ctrl+A) with the pointer over it, or with it focused,
 * selects the file alone and not the whole page.
 */
function Yaml({ name, text }: { name: string; text?: string }) {
  const ref = useRef<HTMLPreElement>(null);
  const hovered = useRef(false);
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      const pre = ref.current;
      if (!pre || ev.key.toLowerCase() !== "a" || !(ev.metaKey || ev.ctrlKey) || ev.altKey || ev.shiftKey) return;
      if (!hovered.current && !pre.contains(document.activeElement)) return;
      const target = ev.target as HTMLElement | null;
      if (target && target !== pre && target.closest("input, textarea, [contenteditable='true']")) return;
      ev.preventDefault();
      const range = document.createRange();
      range.selectNodeContents(pre);
      const sel = window.getSelection();
      sel?.removeAllRanges();
      sel?.addRange(range);
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);
  if (!text) return <p className="page-note">This server does not return the YAML view.</p>;
  return (
    <pre
      ref={ref}
      className="settings__yaml"
      aria-label={name}
      tabIndex={0}
      onMouseEnter={() => (hovered.current = true)}
      onMouseLeave={() => (hovered.current = false)}
    >
      <code>
        {text
          .replace(/\n$/, "")
          .split("\n")
          .map((line, i) => (
            <YamlLine key={i} line={line} />
          ))}
      </code>
    </pre>
  );
}

function YamlLine({ line }: { line: string }) {
  const trimmed = line.trimStart();
  if (trimmed.startsWith("#")) {
    return (
      <span className="yaml__line">
        <span className="yaml__comment">{line}</span>
        {"\n"}
      </span>
    );
  }
  // key: value  # comment — the comment is the last " #" outside a quote.
  const m = /^(\s*(?:- )?)([^:#]+?:)(\s.*?)?(\s+#.*)?$/.exec(line);
  if (!m) {
    return (
      <span className="yaml__line">
        {line}
        {"\n"}
      </span>
    );
  }
  return (
    <span className="yaml__line">
      {m[1]}
      <span className="yaml__key">{m[2]}</span>
      {m[3] && <span className="yaml__value">{m[3]}</span>}
      {m[4] && <span className="yaml__comment">{m[4]}</span>}
      {"\n"}
    </span>
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

/** Who is looking, in a few words: the principal and what it reaches. */
function Who({ who }: { who: Whoami }) {
  const name = who.id ? <strong>{who.id}</strong> : "an unnamed principal";
  return (
    <span className="settings__who" aria-label="Who is looking">
      signed in as {name}
      {who.admin
        ? ", administrator"
        : who.permissions.length === 0
          ? ", with no KB on this server"
          : `: ${who.permissions.map((rule) => `in ${rule.kb} you ${describeRule(rule)}`).join("; ")}`}
    </span>
  );
}
