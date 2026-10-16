import { useEffect, useMemo, useRef, useState } from "react";
import { fetchSecrets, fetchServices } from "../api/client";
import type { SecretEntry, SecretFile, SecretList, ServiceEntry } from "../api/types";
import { decryptFile, encType, InvalidKeyError, looksLikeAgeKey, WrongKeyError } from "../lib/secrets";
import { Icon } from "./Icon";
import { Count, Page, PageHeader } from "./Page";
import { EmptyState, ErrorState, Skeleton } from "./States";

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
  if (files.length === 0) {
    return (
      <Page label="Secrets" className="secrets">
        {tabs}
        <EmptyState
          title="This KB keeps no secrets"
          detail="Files named secrets/*.sops.yaml appear here, with their keys in the clear and their values masked."
        />
      </Page>
    );
  }
  const total = files.reduce((n, f) => n + f.entries.length, 0);
  const groups = groupByService(files, services);

  return (
    <Page label="Secrets" className="secrets">
      <PageHeader
        eyebrow="Secrets"
        title={
          <>
            <Count>{total}</Count> encrypted value{total === 1 ? "" : "s"} in <Count>{files.length}</Count> file
            {files.length === 1 ? "" : "s"}.
          </>
        }
        subtitle="Key names are cleartext in sops. Values stay masked until you paste an age key; it is used in this page only."
      />
      {tabs}
      <form
        className={unlocked ? "secrets__key secrets__key--open" : "secrets__key"}
        onSubmit={(e) => {
          e.preventDefault();
          if (!unlocked) void unlock();
        }}
      >
        {unlocked ? (
          <>
            <span className="secrets__status">
              <Icon name="unlock" size={16} />
              Unlocked in this browser — the key was dropped after use
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
      {keyError && (
        <p className="secrets__error" role="alert">
          {keyError}
        </p>
      )}
      {groups.map((g, i) => (
        <SecretGroup
          key={g.key}
          id={`secrets-group-${i}`}
          group={g}
          plain={plain}
          revealed={revealed}
          titleOf={titleOf}
          onOpenConcept={onOpenConcept}
          onToggle={(enc) =>
            setRevealed((prev) => {
              const next = new Set(prev);
              if (next.has(enc)) next.delete(enc);
              else next.add(enc);
              return next;
            })
          }
        />
      ))}
      <FilesSection files={files} titleOf={titleOf} onOpenConcept={onOpenConcept} />
    </Page>
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

function SecretGroup({
  id,
  group,
  plain,
  revealed,
  titleOf,
  onOpenConcept,
  onToggle,
}: {
  id: string;
  group: Group;
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
  const service = group.service;
  const name = service ? (service.title ?? service.id) : "Not used by a Service";
  // Under a Service, "used by" names the other concepts only.
  const others = (e: SecretEntry) => e.referenced_by.filter((c) => c !== service?.id);
  return (
    <section className="secrets__file" aria-labelledby={id}>
      <header className="secrets__head">
        <h2 id={id} className="secrets__path secrets__service">
          {service && onOpenConcept ? (
            <button type="button" className="secrets__ref" onClick={() => onOpenConcept(service.id)}>
              {name}
            </button>
          ) : (
            name
          )}
        </h2>
        {service?.status && service.status !== "active" && (
          <span className="secrets__status-tag">{service.status}</span>
        )}
        <span className="secrets__badge">{group.rows.length}</span>
      </header>
      <ul className="secrets__rows" aria-label={`Values of ${name}`}>
        {group.rows.map(({ file, entry: e }) => {
          const value = plain.get(e.enc);
          const shown = value !== undefined && revealed.has(e.enc);
          const cut = e.pointer.lastIndexOf("/");
          const label = shown ? "Hide value" : "Reveal value";
          const used = others(e);
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
    </section>
  );
}

/** The sops files themselves: who can open each, when it changed, which could not be read. */
function FilesSection({
  files,
  titleOf,
  onOpenConcept,
}: {
  files: SecretFile[];
  titleOf(id: string): string | undefined;
  onOpenConcept?(id: string): void;
}) {
  return (
    <section className="secrets__file secrets__files" aria-labelledby="secrets-files">
      <header className="secrets__head">
        <h2 id="secrets-files" className="secrets__path">
          Files
        </h2>
        <span className="secrets__badge">{files.length}</span>
      </header>
      <ul className="secrets__rows" aria-label="Secret files">
        {files.map((file) => (
          <li key={file.path} className="secrets__filerow">
            <code className="secrets__pointer">{file.path}</code>
            {file.error ? (
              <span className="secrets__error" role="alert">
                Could not be read: {file.error}
              </span>
            ) : (
              <span className="secrets__meta">
                <span>
                  {file.entries.length} value{file.entries.length === 1 ? "" : "s"}
                </span>
                {file.lastmodified && <span>modified {modified(file.lastmodified)}</span>}
                <span>
                  {file.recipients.length} recipient{file.recipients.length === 1 ? "" : "s"}
                </span>
                {file.recipients.map((r) => (
                  <code key={r} className="secrets__recipient" title={r}>
                    {r.slice(0, 12)}…{r.slice(-6)}
                  </code>
                ))}
                {file.source_of.length > 0 && (
                  <span>
                    whole file used by{" "}
                    {file.source_of.map((cid) =>
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
              </span>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}
