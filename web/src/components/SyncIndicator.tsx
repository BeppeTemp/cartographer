import { useEffect, useId, useRef, useState } from "react";
import { formatWhen, syncState, type SyncTone } from "../lib/syncstate";
import { useSyncStatus } from "../lib/syncstatus";
import { Icon, type IconName } from "./Icon";

interface Props {
  kb: string;
  /** The live tick (D337): a write or a pull moved the KB. */
  live: number;
}

// Not colour alone: each tone has its own glyph as well as its own word.
const TONE_ICON: Record<SyncTone, IconName> = {
  ok: "check",
  attention: "info",
  problem: "alert",
  warning: "alert",
};

/**
 * The top bar's answer to "is everything on the remote?" for the selected KB
 * (plan 1092): a state word with a glyph, and a popover with the details. It is
 * drawn only when the server says (a 404 or an older server draws nothing).
 */
export function SyncIndicator({ kb, live }: Props) {
  const { status, hidden } = useSyncStatus(kb, true, live);
  const state = syncState(status);
  const [open, setOpen] = useState(false);
  const [announced, setAnnounced] = useState("");
  const rootRef = useRef<HTMLDivElement | null>(null);
  const buttonRef = useRef<HTMLButtonElement | null>(null);
  const panelId = useId();

  // The live region speaks when the tone changes, not when a count ticks and
  // not on the first load.
  const tone = state?.tone ?? null;
  const label = state?.label ?? "";
  const lastTone = useRef<SyncTone | null>(null);
  useEffect(() => {
    if (tone && lastTone.current && lastTone.current !== tone) setAnnounced(`Sync: ${label}`);
    lastTone.current = tone;
  }, [tone, label]);

  useEffect(() => {
    if (!open) return;
    const onPointer = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    };
    // Capture phase: the shell's own Escape (clear the selection, App.tsx)
    // sits on document too and was registered first, so only a capturing
    // listener that stops the event keeps Escape to the popover alone.
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      setOpen(false);
      buttonRef.current?.focus();
    };
    document.addEventListener("pointerdown", onPointer);
    document.addEventListener("keydown", onKey, true);
    return () => {
      document.removeEventListener("pointerdown", onPointer);
      document.removeEventListener("keydown", onKey, true);
    };
  }, [open]);

  // A different KB starts with the popover closed.
  useEffect(() => setOpen(false), [kb]);

  if (hidden || !status || !state) return null;

  const ahead = status.unpushed_commits;
  const behind = status.behind_commits;
  // No remote, no counts: the rows would only say "Unknown".
  const counted = status.push_state !== "no_remote" && status.push_state !== "disabled" && status.has_remote !== false;
  const count = (n: number | null | undefined) => (typeof n === "number" ? String(n) : "Unknown");

  return (
    <div ref={rootRef} className="sync">
      <button
        ref={buttonRef}
        type="button"
        className={`sync__button sync__button--${state.tone}`}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        aria-label={`Sync: ${state.label}`}
        title={state.summary}
        onClick={() => setOpen((v) => !v)}
      >
        <span className="sync__glyph" aria-hidden="true">
          <Icon name={TONE_ICON[state.tone]} size={14} />
        </span>
        <span className="sync__text">{state.label}</span>
      </button>
      <span className="sr-only" role="status">
        {announced}
      </span>
      {open && (
        <div id={panelId} className="sync__popover" role="dialog" aria-label="Sync details">
          <p className={`sync__headline sync__headline--${state.tone}`}>
            <strong>{state.label}.</strong> {state.summary}
          </p>
          <dl className="sync__details">
            <dt>State</dt>
            <dd>{status.push_state}</dd>
            {status.branch !== undefined && (
              <>
                <dt>Branch</dt>
                <dd>
                  {status.branch || "Detached HEAD"}
                  {status.branch_source && <span className="sync__note"> ({status.branch_source})</span>}
                </dd>
              </>
            )}
            {counted && (
              <>
                <dt>To push</dt>
                <dd>{count(ahead)}</dd>
                <dt>Behind</dt>
                <dd>
                  {count(behind)} <span className="sync__note">as of the last fetch</span>
                </dd>
              </>
            )}
            <dt>Last push</dt>
            <dd>{status.last_push_at ? formatWhen(status.last_push_at) : "Unknown since the server restarted"}</dd>
            <dt>Last fetch</dt>
            <dd>{status.last_sync_in_at ? formatWhen(status.last_sync_in_at) : "Not fetched since the server started"}</dd>
            {status.remote_url && (
              <>
                <dt>Remote</dt>
                <dd className="sync__url">{status.remote_url}</dd>
              </>
            )}
            {status.push_last_error && (
              <>
                <dt>Last error</dt>
                <dd className="sync__error">{status.push_last_error}</dd>
              </>
            )}
          </dl>
        </div>
      )}
    </div>
  );
}
