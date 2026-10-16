import { useEffect, useState } from "react";
import { fetchSettings, fetchWhoami } from "../api/client";
import type { SettingEntry, SettingOrigin, SettingsKB, SettingsView, Whoami, WhoamiRule } from "../api/types";
import { Page, PageHeader, Segmented } from "./Page";
import { ErrorState, Skeleton } from "./States";

/** A value as a person reads it: lists joined, empty said out loud. */
export function formatValue(value: unknown): string {
  if (Array.isArray(value)) return value.length === 0 ? "(none)" : value.join(", ");
  if (value === "" || value === null || value === undefined) return "(empty)";
  return String(value);
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
  tools: { title: "Tools", intro: "Which MCP tools an agent is shown." },
  mcp: { title: "MCP endpoint", intro: "Which browser origins may call the MCP endpoint." },
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
    { title: "Writes & tools", intro: "What agents may write here, and which tools they see." },
    (k) => /^(allow_|write_gate|mcp_allowlist)/.test(k),
  ],
  [
    { title: "Maintenance", intro: "What the server repairs on its own, and when it proposes a doctor session." },
    (k) => /^(auto_repair|repair_on_write|doctor_|usage_stale_days)/.test(k),
  ],
  [
    { title: "Secrets & signing", intro: "How this KB's secrets are decrypted and its artifacts signed." },
    () => true,
  ],
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
  onRetry,
  onFailure,
}: {
  /** Changes when the server moves under the page: the settings are read again. */
  reloadKey: string;
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

  return (
    <Page label="Settings" className="settings">
      <PageHeader
        eyebrow="Settings"
        title="What is in force"
        subtitle={
          <>
            Version {view.version}
            {view.latest_version ? ` · ${view.latest_version} available` : ""}
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
          <Tab id="server" current={!kb} onSelect={setTab} entries={view.server}>
            Server
          </Tab>
          {selected && (
            <Tab id="kb" current={!!kb} onSelect={setTab} entries={selected.entries}>
              KB {selected.name}
            </Tab>
          )}
        </div>
      </div>

      <div role="tabpanel" aria-labelledby={`settings-tab-${kb ? "kb" : "server"}`}>
        {kb ? (
          <KBPanel kb={kb} mode={mode} query={query} />
        ) : (
          <ServerPanel entries={view.server} yaml={view.server_yaml} mode={mode} query={query} />
        )}
      </div>
    </Page>
  );
}

/** A tab, with how many of its keys are set away from their default. */
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
  const set = entries.filter((e) => e.origin !== "default").length;
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
      <span className="settings__tab-count" title={`${set} of ${entries.length} set; the rest keep their default`}>
        {set}/{entries.length}
      </span>
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
        <Yaml
          name="config.yaml"
          text={yaml}
          note="The server's configuration in force, as one file. A value from the environment or a flag says so; every key left at its default is documented and commented out."
        />
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
  const git = kb.git;
  return (
    <>
      {git && (
        <ul className="settings__facts" aria-label={`Replication of ${kb.name}`}>
          <li>{git.has_remote ? (git.remote_url ? `Remote ${git.remote_url}` : "Has a remote") : "No remote"}</li>
          <li>Sync {git.sync ? "on" : "off"}</li>
          {git.push_state && <li>Push {git.push_state}</li>}
          {git.unpushed_commits ? <li>{git.unpushed_commits} unpushed</li> : null}
          {git.behind_commits ? <li>{git.behind_commits} behind</li> : null}
          {git.push_last_error && <li role="alert">Last push error: {git.push_last_error}</li>}
        </ul>
      )}
      {mode === "yaml" ? (
        <Yaml
          name={`${kb.name}/settings.yaml`}
          text={kb.yaml}
          note="This KB's settings in force, as its settings.yaml. A value set by the server's kbs[] entry, the environment or a flag says so; every key left at its default is documented and commented out."
        />
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

/** One group, folded until it is opened or a search finds keys in it: its
 *  title and how many keys it sets are enough to decide whether to open it. */
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
  const set = entries.filter((e) => e.origin !== "default").length;
  return (
    <details className="settings__group" open={open}>
      <summary className="settings__group-head">
        <h2 className="settings__heading">{group.title}</h2>
        <span className="settings__group-count">
          {set ? `${set} set · ` : ""}
          {entries.length} key{entries.length === 1 ? "" : "s"}
        </span>
      </summary>
      {group.intro && <p className="settings__intro">{group.intro}</p>}
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
            <span className="settings__value" aria-label={`Value of ${e.key}`}>
              {formatValue(e.value)}
            </span>
            <span className={`settings__origin settings__origin--${e.origin}`}>{ORIGIN_LABEL[e.origin] ?? e.origin}</span>
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

/** A settings file, highlighted: comments muted, keys set apart from values. */
function Yaml({ name, text, note }: { name: string; text?: string; note: string }) {
  const [copied, setCopied] = useState(false);
  if (!text) return <p className="page-note">This server does not return the YAML view.</p>;
  const copy = () => {
    void navigator.clipboard
      ?.writeText(text)
      .then(() => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => undefined);
  };
  return (
    <section className="settings__file" aria-label={name}>
      <header className="settings__file-head">
        <code>{name}</code>
        <button type="button" className="button button--ghost settings__copy" onClick={copy}>
          {copied ? "Copied" : "Copy"}
        </button>
      </header>
      <p className="settings__intro">{note}</p>
      <pre className="settings__yaml">
        <code>
          {text.replace(/\n$/, "").split("\n").map((line, i) => (
            <YamlLine key={i} line={line} />
          ))}
        </code>
      </pre>
    </section>
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
      you are {name}
      {who.admin
        ? ", administrator"
        : who.permissions.length === 0
          ? ", with no KB on this server"
          : `: ${who.permissions.map((rule) => `in ${rule.kb} you ${describeRule(rule)}`).join("; ")}`}
    </span>
  );
}
