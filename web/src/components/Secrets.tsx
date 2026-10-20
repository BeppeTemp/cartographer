import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { fetchSecrets, fetchServices } from "../api/client";
import type { SecretEntry, SecretFile, SecretList, ServiceEntry } from "../api/types";
import { decryptFile, encType, InvalidKeyError, looksLikeAgeKey, WrongKeyError } from "../lib/secrets";
import { DocsLink } from "./DocsLink";
import { Icon } from "./Icon";
import { Count, Page, PageHeader } from "./Page";
import { useMediaQuery } from "./Sheet";
import { ErrorState, Skeleton } from "./States";

const MASK = "••••••••";

/** The sops timestamp as the other pages say dates; the raw text when it does not parse. */
function modified(iso: string): string {
  const t = Date.parse(iso);
  return Number.isNaN(t)
    ? iso
    : new Date(t).toLocaleDateString("en", { day: "numeric", month: "short", year: "numeric" });
}

/**
 * The Secrets panel: the KB's sops files, their structure in the clear (key
 * names are never encrypted by sops) and, once the reader pastes an age
 * identity, their values. Decryption happens in this page: the identity is
 * read in a field, used once and dropped; only the decrypted values stay in
 * component state, keyed by ciphertext, until Lock, until the panel closes or
 * the KB changes. Nothing is stored, logged or sent.
 */
export function Secrets({
  kb,
  reloadKey,
  titleOf = () => undefined,
  onOpenConcept,
  onRetry,
  onFailure,
  tabs,
}: {
  /** The section tabs (Artifacts · Secrets), drawn under the header. */
  tabs?: React.ReactNode;
  kb: string;
  /** Changes when the KB moves under the page: the files are read again. */
  reloadKey: string;
  titleOf?(id: string): string | undefined;
  onOpenConcept?(id: string): void;
  onRetry(): void;
  onFailure(err: unknown): boolean;
}) {
  const [list, setList] = useState<SecretList | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  // Decrypted values by ciphertext: still right after a refetch that left the
  // value alone, and meaningless to anything but this page.
  const [plain, setPlain] = useState<Map<string, string>>(new Map());
  const [revealed, setRevealed] = useState<Set<string>>(new Set());
  const [identity, setIdentity] = useState("");
  const [keyError, setKeyError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // The tile whose detail is open: "service:<id>" or "file:<path>".
  const [selected, setSelected] = useState<string | null>(null);
  const narrow = useMediaQuery("(max-width: 1023px)");
  // The KB on screen, for a decryption that finishes after a switch.
  const kbNow = useRef(kb);
  kbNow.current = kb;

  // A KB switch drops the key material and the values of the previous one.
  useEffect(() => {
    setList(null);
    setPlain(new Map());
    setRevealed(new Set());
    setIdentity("");
    setKeyError(null);
    setSelected(null);
    setLoading(true);
  }, [kb]);

  useEffect(() => {
    const controller = new AbortController();
    fetchSecrets(kb, controller.signal)
      .then((data) => {
        setList(data);
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kb, reloadKey]);

  // The Services (concepts of type Service): the secrets are grouped by the
  // one that uses them. A failure leaves one group for every value.
  const [services, setServices] = useState<ServiceEntry[]>([]);
  useEffect(() => {
    const controller = new AbortController();
    fetchServices(kb, controller.signal)
      .then((data) => setServices(data.services))
      .catch(() => setServices([]));
    return () => controller.abort();
  }, [kb, reloadKey]);

  const files = useMemo(() => list?.files ?? [], [list]);

  // A refresh after an edit re-encrypts the values it touched (sops draws a
  // new IV): the plaintext of a ciphertext no longer served is dropped, and
  // with nothing left the panel is locked again and asks for the key.
  useEffect(() => {
    const served = new Set(files.flatMap((f) => f.entries.map((e) => e.enc)));
    setPlain((prev) => {
      const kept = new Map([...prev].filter(([enc]) => served.has(enc)));
      return kept.size === prev.size ? prev : kept;
    });
  }, [files]);
  const unlocked = plain.size > 0;

  async function unlock() {
    const key = identity.trim();
    if (!looksLikeAgeKey(key)) {
      setKeyError("That is not an age secret key: it starts with AGE-SECRET-KEY-1.");
      return;
    }
    setBusy(true);
    setKeyError(null);
    const forKB = kb;
    try {
      const next = new Map<string, string>();
      let opened = 0;
      let wrong = 0;
      for (const f of files.filter((x) => !x.error)) {
        try {
          const got = await decryptFile(key, f);
          opened++;
          for (const e of f.entries) {
            const v = got.get(e.pointer);
            if (v !== undefined) next.set(e.enc, v);
          }
        } catch (err) {
          if (err instanceof WrongKeyError) wrong++;
          else throw err;
        }
      }
      // Switched to another KB meanwhile: these values are not its own.
      if (kbNow.current !== forKB) return;
      if (opened === 0) {
        setKeyError(
          wrong > 0
            ? "This key does not open any of the files: it is not one of their recipients."
            : "Nothing to decrypt.",
        );
        return;
      }
      if (next.size === 0) {
        setKeyError("The key opens the files, but none of their values could be read.");
        return;
      }
      setPlain(next);
      setRevealed(new Set());
      setIdentity(""); // the key is no longer needed: do not keep it
      if (wrong > 0) setKeyError(`${wrong} file${wrong === 1 ? "" : "s"} could not be opened with this key.`);
    } catch (err) {
      setKeyError(
        err instanceof InvalidKeyError
          ? err.message
          : "The key could not be used in this browser (WebCrypto unavailable?).",
      );
    } finally {
      setBusy(false);
    }
  }

  function lock() {
    setPlain(new Map());
    setRevealed(new Set());
    setKeyError(null);
  }

  if (loading && !list) return <Skeleton lines={6} label="Loading secrets" />;
  if (error) return <ErrorState error={error} onRetry={onRetry} />;
  if (!list) return null;
  const total = files.reduce((n, f) => n + f.entries.length, 0);
  // Every Service is a tile, one with no value too: it says what to declare.
  const used = groupByService(files, services);
  const groups: Group[] = [
    ...services.map((s) => used.find((g) => g.key === s.id) ?? { key: s.id, service: s, rows: [] }),
    ...used.filter((g) => !g.service),
  ];
  const toggle = (enc: string) =>
    setRevealed((prev) => {
      const next = new Set(prev);
      if (next.has(enc)) next.delete(enc);
      else next.add(enc);
      return next;
    });
  // The selection names a tile of either section; one that no longer exists
  // after a refetch closes the detail.
  const chosenGroup = selected?.startsWith("service:")
    ? groups.find((g) => `service:${g.key}` === selected)
    : undefined;
  const chosenFile = selected?.startsWith("file:") ? files.find((f) => `file:${f.path}` === selected) : undefined;
  const pick = (id: string) => setSelected(selected === id ? null : id);

  const keyForm = files.some((f) => !f.error) && (
    <form
      className={unlocked ? "secrets__key secrets__key--open" : "secrets__key"}
      onSubmit={(e) => {
        e.preventDefault();
        if (!unlocked) void unlock();
      }}
    >
      {unlocked ? (
        <>
          <span className="secrets__status" title="The key was dropped after use">
            <Icon name="unlock" size={16} />
            Unlocked in this browser
          </span>
          <button type="button" className="button secrets__lock" onClick={lock}>
            <Icon name="lock" size={14} />
            Lock
          </button>
        </>
      ) : (
        <>
          <Icon name="lock" size={16} />
          <input
            className="secrets__input"
            type="password"
            name="age-identity"
            autoComplete="off"
            autoCorrect="off"
            autoCapitalize="off"
            spellCheck={false}
            placeholder="AGE-SECRET-KEY-1…"
            aria-label="Age secret key"
            value={identity}
            onChange={(e) => setIdentity(e.target.value)}
          />
          <button
            type="submit"
            className="button button--primary secrets__lock"
            disabled={busy || identity.trim() === ""}
          >
            {busy ? "Decrypting…" : "Decrypt"}
          </button>
        </>
      )}
    </form>
  );

  const catalog = (
    <Page label="Secrets" className="catalog secrets">
      <PageHeader
        eyebrow="Artifacts"
        title={
          files.length === 0 ? (
            "This KB keeps no secrets."
          ) : (
            <>
              <Count>{total}</Count> secret{total === 1 ? "" : "s"} in <Count>{files.length}</Count> file
              {files.length === 1 ? "" : "s"}.
            </>
          )
        }
        subtitle="Key names are cleartext in sops. Values stay masked until you paste an age key; it is used in this page only."
        actions={keyForm || undefined}
      />
      {tabs}
      {keyError && (
        <p className="secrets__error" role="alert">
          {keyError}
        </p>
      )}
      <nav aria-label="Secrets by Service and by file">
        <section className="catalog__group" aria-label="Services">
          <header className="catalog__group-head">
            <h2 className="catalog__group-title">
              <SecretMark kind="service" />
              Services <span className="page-section__count">{services.length}</span>
            </h2>
            <p className="catalog__about">
              The pages of type Service, each with the values it uses through <code>secret_refs</code> or{" "}
              <code>secrets_source</code>. An agent that reads one gets its values resolved.
            </p>
          </header>
          {groups.length > 0 && (
            <ul className="catalog__tiles">
              {groups.map((g) => {
                const id = `service:${g.key}`;
                const name = g.service ? (g.service.title ?? g.service.id) : "No Service";
                const from = [...new Set(g.rows.map((r) => r.file.path.replace(/^secrets\//, "")))];
                return (
                  <li key={id}>
                    <button
                      type="button"
                      className="catalog__tile"
                      data-muted={!g.service || undefined}
                      aria-current={selected === id ? "true" : undefined}
                      style={{ "--kind": MARK.service.hue } as CSSProperties}
                      onClick={() => pick(id)}
                    >
                      <span className="catalog__tile-head">
                        <span className="catalog__tile-name">{name}</span>
                        {g.service?.status && g.service.status !== "active" && (
                          <span className="secrets__status-tag">{g.service.status}</span>
                        )}
                      </span>
                      <span className="catalog__tile-desc">
                        {!g.service
                          ? "Values no Service uses yet"
                          : from.length
                            ? `from ${from.join(", ")}`
                            : "No value yet: declare its secret_refs"}
                      </span>
                      <span className="catalog__tile-foot">
                        <span>
                          {g.rows.length} value{g.rows.length === 1 ? "" : "s"}
                        </span>
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </section>
        <section className="catalog__group" aria-label="Files">
          <header className="catalog__group-head">
            <h2 className="catalog__group-title">
              <SecretMark kind="file" />
              Files <span className="page-section__count">{files.length}</span>
            </h2>
            <p className="catalog__about">
              The sops files that hold the values, encrypted for their recipients.
              {list.kind ? (
                <>
                  {" "}
                  Files go in <code>{list.kind.kb_path}</code>. <DocsLink doc={list.kind.doc} />
                </>
              ) : (
                " Files named secrets/*.sops.yaml appear here, with their keys in the clear and their values masked."
              )}
            </p>
          </header>
          {files.length > 0 && (
            <ul className="catalog__tiles">
              {files.map((f) => {
                const id = `file:${f.path}`;
                return (
                  <li key={id}>
                    <button
                      type="button"
                      className="catalog__tile"
                      aria-current={selected === id ? "true" : undefined}
                      style={{ "--kind": f.error ? "var(--sev-error)" : MARK.file.hue } as CSSProperties}
                      onClick={() => pick(id)}
                    >
                      <span className="catalog__tile-head">
                        <span className="catalog__tile-name">{f.path.replace(/^secrets\//, "")}</span>
                        {f.error && <span className="secrets__flag">unreadable</span>}
                      </span>
                      <span className="catalog__tile-desc" title={f.error}>
                        {f.error
                          ? f.error
                          : `${f.recipients.length} recipient${f.recipients.length === 1 ? "" : "s"}${
                              f.source_of.length ? ` · whole file used by ${f.source_of.length}` : ""
                            }`}
                      </span>
                      <span className="catalog__tile-foot">
                        {!f.error && (
                          <span>
                            {f.entries.length} value{f.entries.length === 1 ? "" : "s"}
                          </span>
                        )}
                        {f.lastmodified && <span>modified {modified(f.lastmodified)}</span>}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </section>
      </nav>
    </Page>
  );

  const close = () => setSelected(null);
  const back = narrow ? (
    <button type="button" className="button artifacts__back" onClick={close}>
      Back to secrets
    </button>
  ) : (
    <button type="button" className="artifacts__close" aria-label="Close" title="Close" onClick={close}>
      ×
    </button>
  );
  const detail =
    chosenGroup || chosenFile ? (
      <article className="artifacts__detail" aria-label={chosenFile ? `File ${chosenFile.path}` : "Service"}>
        {back}
        {chosenGroup && (
          <>
            <header className="artifacts__head">
              <p className="page__eyebrow">
                <SecretMark kind="service" />
                Service
              </p>
              <h2 className="artifacts__name">
                {chosenGroup.service && onOpenConcept ? (
                  <button type="button" className="secrets__ref" onClick={() => onOpenConcept(chosenGroup.service!.id)}>
                    {chosenGroup.service.title ?? chosenGroup.service.id}
                  </button>
                ) : (
                  (chosenGroup.service?.title ?? chosenGroup.service?.id ?? "No Service")
                )}
              </h2>
              {!chosenGroup.service && <p className="artifacts__desc">Values that no page of type Service uses.</p>}
            </header>
            <ValueRows
              rows={chosenGroup.rows}
              except={chosenGroup.service?.id}
              plain={plain}
              revealed={revealed}
              titleOf={titleOf}
              onOpenConcept={onOpenConcept}
              onToggle={toggle}
            />
          </>
        )}
        {chosenFile && (
          <>
            <header className="artifacts__head">
              <p className="page__eyebrow">
                <SecretMark kind="file" />
                Secret file
              </p>
              <h2 className="artifacts__name">{chosenFile.path.replace(/^secrets\//, "")}</h2>
              <p className="artifacts__path">{chosenFile.path}</p>
            </header>
            {chosenFile.error ? (
              <p className="secrets__error" role="alert">
                Could not be read: {chosenFile.error}
              </p>
            ) : (
              <>
                <div className="secrets__meta">
                  {chosenFile.lastmodified && <span>modified {modified(chosenFile.lastmodified)}</span>}
                  <span>
                    {chosenFile.recipients.length} recipient{chosenFile.recipients.length === 1 ? "" : "s"}
                  </span>
                  {chosenFile.recipients.map((r) => (
                    <code key={r} className="secrets__recipient" title={r}>
                      {r.slice(0, 12)}…{r.slice(-6)}
                    </code>
                  ))}
                </div>
                {chosenFile.source_of.length > 0 && (
                  <p className="secrets__refs">
                    whole file used by{" "}
                    {chosenFile.source_of.map((cid) =>
                      onOpenConcept ? (
                        <button key={cid} type="button" className="secrets__ref" onClick={() => onOpenConcept(cid)}>
                          {titleOf(cid) ?? cid}
                        </button>
                      ) : (
                        <span key={cid}>{titleOf(cid) ?? cid}</span>
                      ),
                    )}
                  </p>
                )}
                <ValueRows
                  rows={chosenFile.entries.map((entry) => ({ file: chosenFile, entry }))}
                  plain={plain}
                  revealed={revealed}
                  titleOf={titleOf}
                  onOpenConcept={onOpenConcept}
                  onToggle={toggle}
                />
              </>
            )}
          </>
        )}
      </article>
    ) : null;

  if (narrow) {
    return (
      <section className="artifacts artifacts--narrow" aria-label="Secrets">
        {detail ?? catalog}
      </section>
    );
  }
  return (
    <section className={detail ? "artifacts artifacts--open" : "artifacts"} aria-label="Secrets">
      {catalog}
      {detail}
    </section>
  );
}

/** The two sections' marks, in the glyph-and-hue language of the artifact kinds. */
const MARK = {
  service: { glyph: "✦", hue: "var(--hue-6)" },
  file: { glyph: "▤", hue: "var(--hue-7)" },
} as const;

function SecretMark({ kind }: { kind: keyof typeof MARK }) {
  return (
    <span className="kind-mark" aria-hidden="true" style={{ "--kind": MARK[kind].hue } as CSSProperties}>
      {MARK[kind].glyph}
    </span>
  );
}

/** One value of a sops file, where it lives. */
interface Row {
  file: SecretFile;
  entry: SecretEntry;
}

interface Group {
  key: string;
  /** The Service the values belong to; absent for the values no Service uses. */
  service?: ServiceEntry;
  rows: Row[];
}

/**
 * The values grouped by the Service that uses them — through a secret_refs
 * pointer or a secrets_source file — in the Services' order, then the values
 * no Service uses. A value two Services use appears under both.
 */
export function groupByService(files: SecretFile[], services: ServiceEntry[]): Group[] {
  const rows = files.flatMap((file) => file.entries.map((entry) => ({ file, entry })));
  const uses = (r: Row, id: string) => r.entry.referenced_by.includes(id) || r.file.source_of.includes(id);
  const groups: Group[] = services
    .map((service) => ({ key: service.id, service, rows: rows.filter((r) => uses(r, service.id)) }))
    .filter((g) => g.rows.length > 0);
  const ids = services.map((s) => s.id);
  const rest = rows.filter((r) => !ids.some((id) => uses(r, id)));
  if (rest.length > 0) groups.push({ key: "", rows: rest });
  return groups;
}

/** The values of a Service or of a file, masked until the key opened them. */
function ValueRows({
  rows,
  except,
  plain,
  revealed,
  titleOf,
  onOpenConcept,
  onToggle,
}: {
  rows: Row[];
  /** The Service the rows are shown under: "used by" names the other concepts only. */
  except?: string;
  plain: Map<string, string>;
  revealed: Set<string>;
  titleOf(id: string): string | undefined;
  onOpenConcept?(id: string): void;
  onToggle(enc: string): void;
}) {
  const [copied, setCopied] = useState<string | null>(null);
  const copy = async (enc: string, value: string) => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(enc);
      window.setTimeout(() => setCopied((c) => (c === enc ? null : c)), 1500);
    } catch {
      /* clipboard unavailable: the value can still be selected */
    }
  };
  return (
    <ul className="secrets__rows" aria-label="Values">
      {rows.map(({ file, entry: e }) => {
        const value = plain.get(e.enc);
        const shown = value !== undefined && revealed.has(e.enc);
        const cut = e.pointer.lastIndexOf("/");
        const label = shown ? "Hide value" : "Reveal value";
        const used = e.referenced_by.filter((c) => c !== except);
        return (
          <li key={`${file.path}#${e.pointer}`} className="secrets__row">
            <div className="secrets__what">
              <code className="secrets__pointer" title={`${file.path}#${e.pointer}`}>
                <span className="secrets__prefix">
                  {file.path.replace(/^secrets\//, "")}#{e.pointer.slice(0, cut + 1)}
                </span>
                {e.pointer.slice(cut + 1)}
              </code>
              {used.length > 0 && (
                <span className="secrets__refs">
                  used by{" "}
                  {used.map((cid) =>
                    onOpenConcept ? (
                      <button key={cid} type="button" className="secrets__ref" onClick={() => onOpenConcept(cid)}>
                        {titleOf(cid) ?? cid}
                      </button>
                    ) : (
                      <span key={cid}>{titleOf(cid) ?? cid}</span>
                    ),
                  )}
                </span>
              )}
            </div>
            <span className="secrets__type">{encType(e.enc)}</span>
            <span className="secrets__value" aria-label={`Value of ${e.pointer}`}>
              {shown ? value : MASK}
            </span>
            <span className="secrets__actions">
              <button
                type="button"
                className="button button--icon"
                disabled={value === undefined}
                aria-pressed={shown}
                aria-label={`${label} of ${e.pointer}`}
                title={label}
                onClick={() => onToggle(e.enc)}
              >
                <Icon name={shown ? "eye-off" : "eye"} size={16} />
              </button>
              <button
                type="button"
                className="button button--icon"
                disabled={value === undefined}
                aria-label={`Copy value of ${e.pointer}`}
                title={copied === e.enc ? "Copied" : "Copy value"}
                onClick={() => value !== undefined && void copy(e.enc, value)}
              >
                <Icon name={copied === e.enc ? "check" : "copy"} size={16} />
              </button>
            </span>
          </li>
        );
      })}
    </ul>
  );
}
