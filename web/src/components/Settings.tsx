import { useEffect, useRef, useState, type CSSProperties } from "react";
import { Icon } from "./Icon";
import { fetchPlaceholders, fetchSettings, fetchWhoami } from "../api/client";
import type {
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

/** A value as a person reads it: lists joined, maps as `key: value`, empty said out loud. */
export function formatValue(value: unknown): string {
  if (Array.isArray(value)) return value.length === 0 ? "(none)" : value.map(formatValue).join(", ");
  if (value === "" || value === null || value === undefined) return "(empty)";
  if (typeof value === "object") {
    const entries = Object.entries(value);
    return entries.length === 0 ? "(none)" : entries.map(([k, v]) => `${k}: ${formatValue(v)}`).join(" · ");
  }
  return String(value);
}

/** The items a list or a map shows as chips: a map's entries read `key: value`. */
function items(value: unknown): string[] | null {
  if (Array.isArray(value)) return value.map(formatValue);
  if (value && typeof value === "object") return Object.entries(value).map(([k, v]) => `${k}: ${formatValue(v)}`);
  return null;
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
  [{ title: "Maintenance", intro: "When an unused skill or agent is reported." }, (k) => /^usage_stale_days/.test(k)],
  [{ title: "Secrets & signing", intro: "How this KB's secrets are decrypted and its artifacts signed." }, () => true],
];

/** The client's keys (.cartographer.yaml), grouped by what they govern. */
const CLIENT_GROUPS: [Group, (key: string) => boolean][] = [
  [
    {
      title: "Connection",
      intro: "Which server this machine talks to, how it authenticates, and whose name its writes carry.",
    },
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
  const [tab, setTab] = useState("client");
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
  // The page opens on the viewer's own client: the first machine that reported, else the template.
  const client = tab.startsWith("client")
    ? (clients.find((c) => `client:${c.machine}` === tab) ?? clients[0] ?? null)
    : undefined;
  const current = client !== undefined ? (client ? `client:${client.machine}` : "client") : kb ? "kb" : "server";

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
            <Tab id="client" current={current === "client"} onSelect={setTab}>
              Client
            </Tab>
          ) : (
            clients.map((c) => (
              <Tab
                key={c.machine}
                id={`client:${c.machine}`}
                current={current === `client:${c.machine}`}
                onSelect={setTab}
              >
                {clients.length === 1 ? "Client" : `Client · ${c.machine}`}
              </Tab>
            ))
          )}
          <Tab id="server" current={current === "server"} onSelect={setTab}>
            Server
          </Tab>
          {selected && (
            <Tab id="kb" current={current === "kb"} onSelect={setTab}>
              Knowledge Base
            </Tab>
          )}
        </div>
      </div>

      <div role="tabpanel" aria-labelledby={`settings-tab-${current}`}>
        {client !== undefined ? (
          <ClientPanel
            client={client}
            template={view.client_template}
            kbs={view.kbs.map((k) => k.name)}
            mode={mode}
            query={query}
            activeKB={selected?.name}
          />
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
  children,
}: {
  id: string;
  current: boolean;
  onSelect(id: string): void;
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
      {mode === "readable" && <Placeholders kb={kb.name} query={query} />}
      {mode === "yaml" ? (
        <Yaml name={`${kb.name}/settings.yaml`} text={kb.yaml} />
      ) : groups.every((g) => g.rows.length === 0) ? (
        <NoMatch query={query} />
      ) : (
        groups
          .filter((g) => g.rows.length > 0)
          .map(({ group, rows }) => (
            <Rows key={group.title} group={group} entries={rows} label={`KB ${kb.name} ${group.title} settings`} />
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
  template,
  kbs,
  mode,
  query,
  activeKB,
}: {
  client: SettingsClient | null;
  template?: { entries: SettingEntry[]; yaml?: string };
  kbs: string[];
  mode: Mode;
  query: string;
  activeKB?: string;
}) {
  if (!client) {
    const note = (
      <p className="page-note settings__reported">
        No client of yours has reported to this server yet: these are the keys of <code>.cartographer.yaml</code> at
        their defaults. Yours arrive with the next <code>cartographer sync</code>, which the sync timer runs every half
        hour.
      </p>
    );
    if (!template) return note;
    const groups = CLIENT_GROUPS.map(([group]) => ({ group, rows: [] as SettingEntry[] }));
    for (const e of template.entries.filter((e) => matches(e, query))) {
      groups[CLIENT_GROUPS.findIndex(([, match]) => match(e.key))]!.rows.push(e);
    }
    return (
      <>
        {note}
        {mode === "yaml" ? (
          <Yaml name=".cartographer.yaml" text={template.yaml} />
        ) : (
          <>
            {groups
              .filter((g) => g.rows.length > 0)
              .map(({ group, rows }) => (
                <Rows key={group.title} group={group} entries={rows} label={`Client ${group.title} settings`} />
              ))}
            <ClientKBs kbs={kbs} client={null} activeKB={activeKB} query={query} />
          </>
        )}
      </>
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
          {groups
            .filter((g) => g.rows.length > 0)
            .map(({ group, rows }) => (
              <Rows key={group.title} group={group} entries={rows} label={`Client ${group.title} settings`} />
            ))}
          <ClientKBs kbs={kbs} client={client} activeKB={activeKB} query={query} />
        </>
      )}
    </>
  );
}

/** The KBs a provider receives: its `clients` entry's kbs, or every KB when it names none. */
function receivedKBs(client: SettingsClient, provider: string): string[] | null {
  const map = client.entries.find((e) => e.key === "clients")?.value;
  const entry = map && typeof map === "object" ? (map as Record<string, { kbs?: string[] }>)[provider] : undefined;
  return entry?.kbs?.length ? entry.kbs : null;
}

/** Where one provider gives one KB to its sessions on this machine. */
function whereFor(client: SettingsClient, p: ClientProvider, kb: string): string[] | "everywhere" | null {
  const kbs = receivedKBs(client, p.name);
  if (p.scope === "workspace") {
    const folders = (p.workspaces ?? []).filter((w) => w.kbs.includes(kb)).map((w) => w.path);
    return folders.length ? folders : null;
  }
  return kbs === null || kbs.includes(kb) ? "everywhere" : null;
}

/**
 * The client side of every KB (D948): the page is the machine's, common to
 * all KBs, so it has one card per KB, each saying which of the machine's agent
 * clients receive that KB and where -- everywhere, or in the folders bound to
 * it -- and where its placeholders resolve here. A scope filter, not a
 * security boundary (D737).
 */
function ClientKBs({
  kbs,
  client,
  activeKB,
  query,
}: {
  kbs: string[];
  client: SettingsClient | null;
  activeKB?: string;
  query: string;
}) {
  const names = [...kbs];
  for (const p of client?.providers ?? []) {
    for (const w of p.workspaces ?? []) for (const k of w.kbs) if (!names.includes(k)) names.push(k);
  }
  const q = query.trim().toLowerCase();
  const cards = names.map((kb) => {
    const agents = (client?.providers ?? []).map((p) => ({ p, where: client ? whereFor(client, p, kb) : null }));
    const paths = (client?.placeholders ?? []).filter((ph) => ph.kbs?.includes(kb));
    const text = [kb, ...agents.flatMap((a) => (Array.isArray(a.where) ? a.where : [])), ...paths.map((ph) => ph.key)];
    return { kb, agents, paths, hit: !q || text.some((t) => t.toLowerCase().includes(q)) };
  });
  const shown = cards.filter((c) => c.hit);
  if (q && shown.length === 0) return null;
  const others = (client?.placeholders ?? []).filter((ph) => !ph.kbs?.length);
  return (
    <Folded
      title="Knowledge bases"
      intro="For each KB, which of this machine's agents receive it and where: everywhere, or only in the folders bound to it. Open one to see it."
      count={`${names.length} KB${names.length === 1 ? "" : "s"}`}
    >
      <ul className="client-kbs" aria-label="Knowledge bases on this machine">
        {shown.map(({ kb, agents, paths }) => {
          const reaching = agents.filter((a) => a.where !== null);
          return (
            <li key={kb}>
              <details className="client-kb" data-active={kb === activeKB || undefined} open={kb === activeKB || !!q}>
                <summary className="client-kb__head">
                  <h3 className="client-kb__name">{kb}</h3>
                  {client && (
                    <span className="client-kb__sum">
                      {reaching.length === 0
                        ? "no agent receives it"
                        : `${reaching.length} agent${reaching.length === 1 ? "" : "s"}`}
                    </span>
                  )}
                </summary>
                {!client ? (
                  <p className="client-kb__none">
                    Bind it to a folder with{" "}
                    <code>cartographer workspace bind &lt;agent&gt; &lt;folder&gt; --kb {kb}</code>: its agents then
                    receive it only there.
                  </p>
                ) : (
                  <ul className="client-kb__agents" aria-label={`Agents receiving ${kb}`}>
                    {agents.map(({ p, where }) => (
                      <li key={p.name} className="client-kb__agent" data-off={where === null || undefined}>
                        <code className="client-kb__provider">{p.name}</code>
                        <span className="client-kb__where">
                          {where === "everywhere"
                            ? "everywhere on this machine"
                            : where === null
                              ? p.scope === "workspace"
                                ? "no folder bound to it"
                                : "not received"
                              : where.map((path) => (
                                  <code key={path} className="client-kb__folder">
                                    {path}
                                  </code>
                                ))}
                        </span>
                        {p.isolation === "workspace" ? (
                          <span
                            className="pill pill--ok"
                            title="This agent starts the Cartographer bridge: a session sees only the KBs bound to its folder."
                          >
                            bridge
                          </span>
                        ) : p.isolation === "none" ? (
                          <span
                            className="pill"
                            title="This agent uses the server URL: a session reaches every KB the agent receives."
                          >
                            http
                          </span>
                        ) : null}
                      </li>
                    ))}
                  </ul>
                )}
                {paths.length > 0 && (
                  <dl className="client-kb__paths" aria-label={`Paths of ${kb} on this machine`}>
                    {paths.map((ph) => (
                      <div key={ph.key}>
                        <dt>
                          <code>{`{{${ph.key}}}`}</code>
                        </dt>
                        <dd data-empty={!ph.path || undefined} title={!ph.path ? ph.reason : ph.description}>
                          {ph.path ?? (ph.ignored ? "ignored" : "unresolved")}
                        </dd>
                      </div>
                    ))}
                  </dl>
                )}
              </details>
            </li>
          );
        })}
      </ul>
      {others.length > 0 && (
        <dl className="client-kb__paths client-kb__paths--others" aria-label="Other paths on this machine">
          {others.map((ph) => (
            <div key={ph.key}>
              <dt>
                <code>{`{{${ph.key}}}`}</code>
              </dt>
              <dd data-empty={!ph.path || undefined}>{ph.path ?? (ph.ignored ? "ignored" : "unresolved")}</dd>
            </div>
          ))}
        </dl>
      )}
    </Folded>
  );
}

/** The placeholder keys this KB declares in paths.yaml or cites. */
function Placeholders({ kb, query }: { kb: string; query: string }) {
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
    >
      <ul className="settings__rows" aria-label={`Placeholders of ${kb}`}>
        {keys.map((k) => {
          const uses = k.concepts.length + k.artifacts.length;
          return (
            <li key={`${k.kind}:${k.key}`} className="settings__row">
              <div className="settings__row-head settings__row-head--static">
                <span className="settings__what">
                  <code className="settings__key">{`{{${k.kind}:${k.key}}}`}</code>
                  {k.description && <span className="settings__desc">{k.description}</span>}
                  <span className="settings__note">
                    {uses ? `cited ${uses} time${uses === 1 ? "" : "s"}` : "cited nowhere"}
                    {k.conflicts?.length ? ` · ${k.conflicts.map((c) => `${c.kb} says ${c.default}`).join(", ")}` : ""}
                  </span>
                </span>
                <span className="settings__val">
                  <span className="settings__value" data-empty={!(k.default ?? k.remote) || undefined}>
                    {k.default ?? k.remote ?? (k.declared ? "no default" : "not declared")}
                  </span>
                </span>
              </div>
            </li>
          );
        })}
      </ul>
    </Folded>
  );
}

/** Each group's mark, in the glyph-and-hue language of the artifact kinds. */
const GROUP_MARK: Record<string, [string, string]> = {
  General: ["◎", "var(--hue-1)"],
  Access: ["◈", "var(--hue-3)"],
  Git: ["⇅", "var(--hue-2)"],
  Audit: ["▦", "var(--hue-8)"],
  Secrets: ["✦", "var(--hue-6)"],
  "Secrets & signing": ["✦", "var(--hue-6)"],
  Atlas: ["◍", "var(--hue-5)"],
  Mount: ["⌂", "var(--hue-4)"],
  Maintenance: ["↻", "var(--hue-7)"],
  Placeholders: ["{}", "var(--hue-9)"],
  "Knowledge bases": ["▣", "var(--hue-10)"],
  Connection: ["⇄", "var(--hue-3)"],
  "Agents & KBs": ["◉", "var(--hue-1)"],
  "Local paths": ["↳", "var(--hue-9)"],
  Trust: ["✓", "var(--hue-5)"],
  "Updates & sync": ["↻", "var(--hue-7)"],
};

/** A group, always open: its mark and title, what it governs, how many keys it holds. */
function Folded({
  title,
  intro,
  count,
  children,
}: {
  title: string;
  intro: string;
  count: string;
  children: React.ReactNode;
}) {
  const [glyph, hue] = GROUP_MARK[title] ?? ["•", "var(--text-muted)"];
  return (
    <section className="settings__group" aria-label={title}>
      <header className="settings__group-head">
        <h2 className="settings__heading">
          <span className="kind-mark" aria-hidden="true" style={{ "--kind": hue } as CSSProperties}>
            {glyph}
          </span>
          {title}
        </h2>
        <span className="settings__group-count">{count}</span>
        {intro && <p className="settings__intro">{intro}</p>}
      </header>
      {children}
    </section>
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

/** One group of keys. */
function Rows({ group, entries, label }: { group: Group; entries: SettingEntry[]; label: string }) {
  return (
    <Folded title={group.title} intro={group.intro} count={`${entries.length} key${entries.length === 1 ? "" : "s"}`}>
      <ul className="settings__rows" aria-label={label}>
        {entries.map((e) => (
          <Row key={e.key} entry={e} />
        ))}
      </ul>
    </Folded>
  );
}

/** A boolean setting reads as a switch. */
function asBool(value: unknown): boolean | null {
  if (typeof value === "boolean") return value;
  if (value === "true") return true;
  if (value === "false") return false;
  return null;
}

/** The value as a chip: a switch for a boolean, chips for a list, a muted word for nothing. */
function Value({ entry: e }: { entry: SettingEntry }) {
  const label = `Value of ${e.key}`;
  const b = asBool(e.value);
  if (b !== null) {
    return (
      <span
        className="settings__value settings__value--bool"
        data-on={b || undefined}
        aria-label={label}
        title={String(b)}
      >
        <span className="settings__switch" aria-hidden="true" />
        {b ? "on" : "off"}
      </span>
    );
  }
  const list = items(e.value);
  if (list && list.length > 0) {
    const shown = list.slice(0, 3);
    return (
      <span className="settings__value settings__value--list" aria-label={label}>
        {shown.map((v, i) => (
          <span key={i} className="settings__item">
            {v}
          </span>
        ))}
        {list.length > shown.length && <span className="settings__item">+{list.length - shown.length}</span>}
      </span>
    );
  }
  const text = formatValue(e.value);
  return (
    <span className="settings__value" data-empty={text.startsWith("(") || undefined} aria-label={label}>
      {text}
    </span>
  );
}

/**
 * One key: its name and what it does, and at the end its value. Opening the
 * row shows its table: the default, who set it, the values it accepts, how to
 * override it, and whether a change waits for a restart.
 */
function Row({ entry: e }: { entry: SettingEntry }) {
  const [open, setOpen] = useState(false);
  const key = shortKey(e.key);
  const set = e.origin !== "default";
  return (
    <li className="settings__row" data-set={set || undefined} data-open={open || undefined}>
      <button type="button" className="settings__row-head" aria-expanded={open} onClick={() => setOpen(!open)}>
        <span className="settings__what">
          <code className="settings__key">{key}</code>
          {e.description && <span className="settings__desc">{e.description}</span>}
        </span>
        <span className="settings__val">
          <Value entry={e} />
        </span>
      </button>
      {open && (
        <dl className="settings__meta">
          <div>
            <dt>Default</dt>
            <dd>
              <code>{e.default || '""'}</code>
            </dd>
          </div>
          {set && (
            <div>
              <dt>Set by</dt>
              <dd>{ORIGIN_LABEL[e.origin] ?? e.origin}</dd>
            </div>
          )}
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
      )}
    </li>
  );
}

/** The line a rendered settings file puts between the keys in force and the ones it documents. */
const AVAILABLE = /^#\s*-+\s*available settings\b.*-+\s*$/i;

/**
 * A settings file, as an editor shows it: its name and a copy button above,
 * line numbers, keys set apart from values, comments muted, and the keys left
 * at their default dimmed under a divider. Select-all (Cmd/Ctrl+A) with the
 * pointer over it, or with it focused, selects the file alone.
 */
function Yaml({ name, text }: { name: string; text?: string }) {
  const ref = useRef<HTMLPreElement>(null);
  const hovered = useRef(false);
  const [copied, setCopied] = useState(false);
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
  const lines = text.replace(/\n$/, "").split("\n");
  const cut = lines.findIndex((l) => AVAILABLE.test(l.trim()));
  const inForce = (cut < 0 ? lines : lines.slice(0, cut)).filter((l) => /^[^\s#][^:#]*:/.test(l)).length;
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable: the file can still be selected */
    }
  };
  return (
    <figure className="settings__file">
      <figcaption className="settings__file-head">
        <span className="settings__file-name">{name}</span>
        <span className="settings__file-note">
          {inForce} key{inForce === 1 ? "" : "s"} set
          {cut >= 0 && " · the rest documented below, at their default"}
        </span>
        <button
          type="button"
          className="button button--icon"
          aria-label={`Copy ${name}`}
          title={copied ? "Copied" : "Copy the file"}
          onClick={() => void copy()}
        >
          <Icon name={copied ? "check" : "copy"} size={14} />
        </button>
      </figcaption>
      <pre
        ref={ref}
        className="settings__yaml"
        aria-label={name}
        tabIndex={0}
        onMouseEnter={() => (hovered.current = true)}
        onMouseLeave={() => (hovered.current = false)}
      >
        <code>
          {lines.map((line, i) =>
            AVAILABLE.test(line.trim()) ? (
              <span key={i} className="yaml__line yaml__divider">
                {line}
                {"\n"}
              </span>
            ) : (
              <YamlLine key={i} line={line} available={cut >= 0 && i > cut} />
            ),
          )}
        </code>
      </pre>
    </figure>
  );
}

/** A scalar's class: a literal (number, boolean, null) or a string. */
function scalarClass(v: string): string {
  const t = v.trim();
  if (/^(true|false|null|~|-?\d+(\.\d+)?)$/.test(t)) return "yaml__lit";
  return "yaml__value";
}

function YamlLine({ line, available }: { line: string; available?: boolean }) {
  const cls = "yaml__line";
  const trimmed = line.trimStart();
  if (trimmed.startsWith("#")) {
    // Under the divider, "# key: value" is a key at its default: still a key.
    // Keys are lowercase; "# Default: …" and "# Values: …" are documentation.
    const k = available ? /^(\s*#\s?)([a-z_][\w.[\]-]*:)(\s.*)?$/.exec(line) : null;
    return (
      <span className={cls}>
        {k ? (
          <>
            <span className="yaml__comment">{k[1]}</span>
            <span className="yaml__key">{k[2]}</span>
            {k[3] && <span className={scalarClass(k[3])}>{k[3]}</span>}
          </>
        ) : (
          <span className="yaml__comment">{line}</span>
        )}
        {"\n"}
      </span>
    );
  }
  // key: value  # comment — the comment is the last " #" outside a quote.
  const m = /^(\s*(?:- )?)([^:#]+?:)(\s.*?)?(\s+#.*)?$/.exec(line);
  if (!m) {
    return (
      <span className={cls}>
        {line}
        {"\n"}
      </span>
    );
  }
  return (
    <span className={cls}>
      {m[1]}
      <span className="yaml__key">{m[2]}</span>
      {m[3] && <span className={scalarClass(m[3])}>{m[3]}</span>}
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
