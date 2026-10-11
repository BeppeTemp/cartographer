import { useEffect, useMemo, useState } from "react";
import { fetchSecrets } from "../api/client";
import type { SecretFile, SecretList } from "../api/types";
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
    : new Date(t).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });
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
}: {
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

  const files = useMemo(() => list?.files ?? [], [list]);
  const unlocked = plain.size > 0;

  async function unlock() {
    const key = identity.trim();
    if (!looksLikeAgeKey(key)) {
      setKeyError("That is not an age secret key: it starts with AGE-SECRET-KEY-1.");
      return;
    }
    setBusy(true);
    setKeyError(null);
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
      if (opened === 0) {
        setKeyError(
          wrong > 0 ? "This key does not open any of the files: it is not one of their recipients." : "Nothing to decrypt.",
        );
        return;
      }
      setPlain(next);
      setRevealed(new Set());
      setIdentity(""); // the key is no longer needed: do not keep it
      if (wrong > 0) setKeyError(`${wrong} file${wrong === 1 ? "" : "s"} could not be opened with this key.`);
    } catch (err) {
      setKeyError(
        err instanceof InvalidKeyError ? err.message : "The key could not be used in this browser (WebCrypto unavailable?).",
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
      <EmptyState
        title="This KB keeps no secrets"
        detail="Files named secrets/*.sops.yaml appear here, with their keys in the clear and their values masked."
      />
    );
  }
  const total = files.reduce((n, f) => n + f.entries.length, 0);

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
      {files.map((f, i) => (
        <SecretFileSection
          key={f.path}
          id={`secrets-file-${i}`}
          file={f}
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
    </Page>
  );
}

function SecretFileSection({
  id,
  file,
  plain,
  revealed,
  titleOf,
  onOpenConcept,
  onToggle,
}: {
  id: string;
  file: SecretFile;
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
  const concept = (cid: string) =>
    onOpenConcept ? (
      <button key={cid} type="button" className="secrets__ref" onClick={() => onOpenConcept(cid)}>
        {titleOf(cid) ?? cid}
      </button>
    ) : (
      <span key={cid}>{titleOf(cid) ?? cid}</span>
    );
  return (
    <section className="secrets__file" aria-labelledby={id}>
      <header className="secrets__head">
        <h2 id={id} className="secrets__path">
          {file.path}
        </h2>
        <span className="secrets__badge">{file.entries.length}</span>
      </header>
      <p className="secrets__meta">
        {file.lastmodified && <span>Modified {modified(file.lastmodified)}</span>}
        <span>
          {file.recipients.length} recipient{file.recipients.length === 1 ? "" : "s"}
        </span>
        {file.recipients.map((r) => (
          <code key={r} className="secrets__recipient" title={r}>
            {r.slice(0, 12)}…{r.slice(-6)}
          </code>
        ))}
        {file.source_of.length > 0 && <span>whole file used by {file.source_of.map(concept)}</span>}
      </p>
      {file.error ? (
        <p className="secrets__error" role="alert">
          Could not be read: {file.error}
        </p>
      ) : (
        <ul className="secrets__rows" aria-label={`Values in ${file.path}`}>
          {file.entries.map((e) => {
            const value = plain.get(e.enc);
            const shown = value !== undefined && revealed.has(e.enc);
            const cut = e.pointer.lastIndexOf("/");
            const label = shown ? "Hide value" : "Reveal value";
            return (
              <li key={e.pointer} className="secrets__row">
                <div className="secrets__what">
                  <code className="secrets__pointer" title={e.pointer}>
                    <span className="secrets__prefix">{e.pointer.slice(0, cut + 1)}</span>
                    {e.pointer.slice(cut + 1)}
                  </code>
                  {e.referenced_by.length > 0 && (
                    <span className="secrets__refs">used by {e.referenced_by.map(concept)}</span>
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
      )}
    </section>
  );
}
