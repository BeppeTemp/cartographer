import { useEffect, useMemo, useRef, useState } from "react";
import { ApiError, fetchFeedbackCandidates, fetchFeedbackDraft } from "../api/client";
import type { ReportCandidate, ReportDraft } from "../api/types";
import { PageSection } from "./Page";
import { age, useResilient } from "./Surveyors";

/** `404 feedback_off`: the operator switched reports off, so nothing is shown. Any other failure is ridden out. */
const feedbackOff = (err: ApiError) => err.status === 404 && err.code === "feedback_off";

/**
 * Candidates live in the server's memory and change without a commit (a
 * Surveyor whose tool calls fail writes nothing), so the list is read again on
 * this timer while the tab is visible, whatever the live counter does.
 */
export const CANDIDATES_REFRESH = { ms: 60_000 };

/**
 * The Surveyors' report candidates (D1925) that concern the KB, read-only. The
 * route is server-wide and the server filters it by what the caller sees whole;
 * a candidate that names another KB is left out here, and the list starts blank
 * when the KB changes. A 5xx or a 404 that is not `feedback_off` (a restart, an
 * older server) keeps the last list and tries again with back-off; it is also
 * read again every CANDIDATES_REFRESH.ms.
 */
export function useReportCandidates(kb: string | null, live: number): ReportCandidate[] {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (!kb) return;
    // Never poll a hidden tab (docs/deployment.md): the next tick after it shows again reads.
    const timer = window.setInterval(() => {
      if (document.visibilityState === "visible") setTick((n) => n + 1);
    }, CANDIDATES_REFRESH.ms);
    return () => window.clearInterval(timer);
  }, [kb]);
  const list = useResilient(
    kb,
    live + tick,
    (_kb, signal) => fetchFeedbackCandidates(signal).then((res) => (Array.isArray(res?.candidates) ? res.candidates : [])),
    feedbackOff,
  );
  return useMemo(() => (list ?? []).filter((c) => !c.kb || c.kb === kb), [list, kb]);
}

const KIND_TEXT: Record<string, string> = {
  tool_error: "tool error",
  protocol_mismatch: "protocol mismatch",
  contract_mismatch: "contract mismatch",
};

/** A link from the server is followed only when it is a web address: never a `javascript:` or `data:` URL. */
const webURL = (u: string | undefined): string | null => (u && /^https?:\/\//i.test(u) ? u : null);

function describe(c: ReportCandidate): string {
  const kind = KIND_TEXT[c.kind] ?? c.kind.replace(/_/g, " ");
  return `${kind}${c.tool ? ` · ${c.tool}` : ""} · ${c.code.replace(/_/g, " ")}`;
}

function draftError(err: unknown): string {
  if (err instanceof ApiError && err.status === 404) {
    return err.code === "feedback_off"
      ? "Feedback reports are switched off on this server."
      : "This candidate is no longer held (the server may have restarted). It leaves the list on the next refresh.";
  }
  return "The draft could not be built. Try again.";
}

type DraftState =
  | { status: "idle" }
  | { status: "loading" }
  | { status: "error"; message: string }
  | { status: "ready"; draft: ReportDraft };

/**
 * The redacted draft of one candidate. The text is shown as the server built it
 * (read-only); nothing is sent from here: the only way out is a link the person
 * follows themselves.
 */
function DraftView({ draft }: { draft: ReportDraft }) {
  const [copied, setCopied] = useState<"" | "done" | "failed">("");
  const form = webURL(draft.url);
  const search = webURL(draft.search_url);
  const copy = () => {
    // A browser without clipboard access (an insecure origin) leaves the text selectable.
    const write = navigator.clipboard?.writeText(draft.body);
    if (!write) {
      setCopied("failed");
      return;
    }
    write.then(
      () => setCopied("done"),
      () => setCopied("failed"),
    );
  };
  return (
    <div className="candidates__draft" role="group" aria-label="Draft report">
      {search && (
        <p className="page-note">
          Check for a duplicate first:{" "}
          <a href={search} target="_blank" rel="noopener noreferrer">
            search the existing issues
          </a>
          .
        </p>
      )}
      <input className="candidates__title" readOnly aria-label="Report title" value={draft.title} />
      <textarea className="candidates__body" readOnly aria-label="Report body" rows={14} value={draft.body} />
      {!draft.body_in_url && (
        <p className="page-note">
          The link carries the title only: paste this text into the form.{" "}
          <button type="button" className="button" onClick={copy}>
            Copy the text
          </button>{" "}
          <span role="status">{copied === "done" ? "Copied." : copied === "failed" ? "Select the text and copy it." : ""}</span>
        </p>
      )}
      {form && (
        <p>
          <a className="button" href={form} target="_blank" rel="noopener noreferrer">
            Open the report form
          </a>
        </p>
      )}
      <p className="page-note">Nothing has been sent: review the text, then submit it yourself.</p>
    </div>
  );
}

function CandidateItem({ c }: { c: ReportCandidate }) {
  const [state, setState] = useState<DraftState>({ status: "idle" });
  const pending = useRef<AbortController | null>(null);
  useEffect(() => () => pending.current?.abort(), []);
  const draft = () => {
    pending.current?.abort();
    const controller = new AbortController();
    pending.current = controller;
    setState({ status: "loading" });
    fetchFeedbackDraft(c.id, controller.signal)
      .then((d) => setState({ status: "ready", draft: d }))
      .catch((err: unknown) => {
        if (controller.signal.aborted || (err instanceof DOMException && err.name === "AbortError")) return;
        setState({ status: "error", message: draftError(err) });
      });
  };
  return (
    <li className="surveyors__row candidates__row">
      <span className="surveyors__name">{describe(c)}</span>
      <span className="surveyors__meta">
        seen {c.count} {c.count === 1 ? "time" : "times"}, last seen {age(c.last_at)}
      </span>
      {state.status === "ready" ? (
        <button type="button" className="surveyors__link" onClick={() => setState({ status: "idle" })}>
          Hide the draft
        </button>
      ) : (
        <button type="button" className="surveyors__link" disabled={state.status === "loading"} onClick={draft}>
          Draft report
        </button>
      )}
      {state.status === "error" && (
        <span className="surveyors__error" role="alert">
          {state.message}
        </span>
      )}
      {state.status === "ready" && <DraftView draft={state.draft} />}
    </li>
  );
}

/**
 * Activity's "Report candidates": what the Surveyors noticed that a person
 * would report to the Cartographer project, per Surveyor, with a "Draft report"
 * action. Nothing renders (no header, no empty state) without a candidate for
 * the KB, when the read failed, or when the operator switched reports off.
 */
export function ReportCandidatesSection({ kb, live = 0 }: { kb: string; live?: number }) {
  const candidates = useReportCandidates(kb, live);
  if (candidates.length === 0) return null;
  const bySurveyor = new Map<string, ReportCandidate[]>();
  for (const c of candidates) bySurveyor.set(c.surveyor, [...(bySurveyor.get(c.surveyor) ?? []), c]);
  return (
    <PageSection title="Report candidates" id="activity-report-candidates" count={candidates.length}>
      <p className="page-note">
        What a Surveyor noticed that may be worth reporting. A draft is built on the server with every real name
        removed; nothing is sent.
      </p>
      {[...bySurveyor].map(([surveyor, list]) => (
        <ul key={surveyor} className="surveyors__list" aria-label={`Report candidates of ${surveyor}`}>
          {list.map((c) => (
            <CandidateItem key={`${kb}/${c.id}`} c={c} />
          ))}
        </ul>
      ))}
    </PageSection>
  );
}

/** Work's badge: how many report candidates the Surveyors hold for the KB. Absent without any. */
export function ReportCandidatesBadge({ kb, live = 0 }: { kb: string; live?: number }) {
  const n = useReportCandidates(kb, live).length;
  if (n === 0) return null;
  return (
    <p className="page-note candidates__badge" role="status">
      {n} report {n === 1 ? "candidate" : "candidates"} from the Surveyors, listed in Activity.
    </p>
  );
}
