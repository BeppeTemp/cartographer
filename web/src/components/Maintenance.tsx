import { useEffect, useState } from "react";
import { fetchMaintenanceQuestions, fetchMaintenanceSummary } from "../api/client";
import type { MaintenanceQuestions, MaintenanceRun, MaintenanceSummary } from "../api/types";

/**
 * What keeps the KB in repair (D323): the server's own background repairs, the
 * last doctor session, and the questions the doctor deferred to a person. Like
 * the rest of the Atlas it reads and never writes: a repair is undone, and a
 * question answered, from an agent session or the CLI, so each row carries the
 * text to copy rather than a button that acts.
 */
export function Maintenance({ kb, live = 0, onOpen }: { kb: string; live?: number; onOpen(conceptId: string): void }) {
  const [summary, setSummary] = useState<MaintenanceSummary | null>(null);
  const [questions, setQuestions] = useState<MaintenanceQuestions | null>(null);
  const [summaryError, setSummaryError] = useState<string | null>(null);
  const [questionsError, setQuestionsError] = useState<string | null>(null);
  const [copied, setCopied] = useState<string | null>(null);

  // A new KB or window starts blank; a live refetch (D336) keeps what is
  // on screen until the fresh answer replaces it.
  useEffect(() => {
    setSummary(null);
    setQuestions(null);
    setSummaryError(null);
    setQuestionsError(null);
  }, [kb]);

  useEffect(() => {
    const controller = new AbortController();
    const fail = (set: (m: string) => void) => (err: unknown) => {
      if (err instanceof DOMException && err.name === "AbortError") return;
      set(err instanceof Error ? err.message : String(err));
    };
    fetchMaintenanceSummary(kb, controller.signal).then(setSummary).catch(fail(setSummaryError));
    fetchMaintenanceQuestions(kb, controller.signal).then(setQuestions).catch(fail(setQuestionsError));
    return () => controller.abort();
  }, [kb, live]);

  async function copy(key: string, text: string) {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(key);
    } catch {
      // No clipboard (an insecure origin, a denied permission): the text is on
      // screen, selectable, and that is the fallback.
      setCopied(null);
    }
  }

  const open = questions?.questions ?? [];
  return (
    <section className="activity maintenance" aria-label="Maintenance">
      <header className="activity__head">
        <div>
          <p className="observatory__eyebrow">Maintenance</p>
          <h1 className="observatory__title">{headline(summary, open.length)}</h1>
        </div>
      </header>
      <p className="sr-only" role="status" aria-live="polite">
        {copied ? "Copied to the clipboard." : ""}
      </p>

      <h2 className="work__group-title">Summary</h2>
      {summaryError ? (
        <p className="activity__note">Could not read the maintenance summary: {summaryError}</p>
      ) : !summary ? (
        <p className="activity__note">Reading the maintenance log…</p>
      ) : (
        <dl className="maintenance__summary">
          <div>
            <dt>Background repair</dt>
            <dd>{autoRepairLine(summary)}</dd>
          </div>
          <div>
            <dt>Last background run</dt>
            <dd>{runLine(summary.last_auto_repair)}</dd>
          </div>
          <div>
            <dt>Last doctor session</dt>
            <dd>{summary.last_doctor ?? "never"}</dd>
          </div>
          <div>
            <dt>Next doctor session</dt>
            <dd>
              {summary.doctor_interval_days === 0
                ? "not proposed (doctor_interval is 0)"
                : (summary.next_doctor ?? "proposed as soon as the KB has debt")}
            </dd>
          </div>
        </dl>
      )}

      <h2 className="work__group-title">Questions for you</h2>
      {questionsError ? (
        <p className="activity__note">Could not read the questions: {questionsError}</p>
      ) : !questions ? (
        <p className="activity__note">Reading the questions…</p>
      ) : open.length === 0 ? (
        <p className="activity__note">No open question: the doctor has nothing waiting on you.</p>
      ) : (
        <ul className="activity__list" aria-label="Open questions">
          {open.map((q) => (
            <li key={q.id} className="maintenance__row">
              <button type="button" className="activity__row" onClick={() => onOpen(q.id)}>
                <span className="activity__row-title">{q.title ?? q.id}</span>
                <span className="activity__row-meta">
                  {q.id}
                  {q.involves?.length ? ` · involves ${q.involves.join(", ")}` : ""}
                </span>
              </button>
              <button
                type="button"
                className="button maintenance__copy"
                aria-label={`Copy ID of ${q.title ?? q.id}`}
                onClick={() => copy(`q:${q.id}`, q.id)}
              >
                Copy ID
              </button>
            </li>
          ))}
        </ul>
      )}
      <p className="work__note">
        Answer from an agent session: <code>concept_patch</code> the answer into the question, then set its
        resolution_status to resolved.
      </p>

      <h2 className="work__group-title">Recent repairs (30 days)</h2>
      {!summary ? null : summary.repairs.length === 0 ? (
        <p className="activity__note">No repair commit in the last 30 days.</p>
      ) : (
        <ul className="activity__list" aria-label="Recent repairs">
          {summary.repairs.map((r) => (
            <li key={r.sha} className="maintenance__row maintenance__row--repair">
              <div className="activity__row maintenance__repair">
                <span className="activity__row-title">{r.subject}</span>
                <span className="activity__row-meta">
                  <code>{r.sha.slice(0, 7)}</code> · {r.at.slice(0, 10)} · {r.files} file{r.files === 1 ? "" : "s"} ·{" "}
                  {r.background ? "background" : "by hand"}
                </span>
                <code className="maintenance__revert">{r.revert}</code>
              </div>
              <button
                type="button"
                className="button maintenance__copy"
                aria-label={`Copy revert command for ${r.sha.slice(0, 7)}`}
                onClick={() => copy(`r:${r.sha}`, r.revert)}
              >
                Copy revert command
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function headline(summary: MaintenanceSummary | null, questions: number): string {
  if (!summary) return "Reading the maintenance log…";
  const q = questions === 1 ? "One question waits" : `${questions} questions wait`;
  const repaired = summary.repairs.length;
  const r = repaired === 0 ? "nothing was repaired" : `${repaired} repair${repaired === 1 ? "" : "s"} landed`;
  return `${questions === 0 ? "No question waits" : q} for you; ${r} in 30 days.`;
}

function autoRepairLine(s: MaintenanceSummary): string {
  const a = s.auto_repair;
  if (a.checks.length === 0) return "off: auto_repair is explicitly empty";
  if (a.interval_days === 0) return "off: doctor_auto_interval is 0";
  const every = a.interval_days === 1 ? "every day" : `every ${a.interval_days} days`;
  return `on, ${every}: ${a.checks.join(", ")}${a.default ? " (the default list)" : ""}`;
}

function runLine(run: MaintenanceRun | null): string {
  if (!run) return "none yet";
  const when = run.at.slice(0, 16).replace("T", " ") + " UTC";
  if (run.skipped) return `${when}: skipped, ${run.skipped}`;
  const applied = (run.checks ?? []).reduce((n, c) => n + c.applied, 0);
  const failed = (run.checks ?? []).filter((c) => c.error).length;
  return `${when}: ${applied} concept${applied === 1 ? "" : "s"} repaired${failed ? `, ${failed} check${failed === 1 ? "" : "s"} failed` : ""}`;
}
