import type { KBStatus } from "../api/types";

/** How the top bar's sync indicator reads (plan 1092). "warning" is a KB with no
 *  second copy anywhere: no remote, or not a repository. */
export type SyncTone = "ok" | "attention" | "problem" | "warning";

export interface SyncState {
  tone: SyncTone;
  /** The short text the button shows. */
  label: string;
  /** One sentence for the popover's headline. */
  summary: string;
}

function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

/**
 * The replication state of a KB from its /status payload, or null when the
 * payload carries none (an older server): the indicator is then not drawn.
 * Precedence: problem > no remote / not versioned > attention > ok.
 */
export function syncState(status: KBStatus | null): SyncState | null {
  const state = status?.push_state;
  if (!status || !state) return null;

  if (state === "failed") {
    return { tone: "problem", label: "Push failed", summary: "The last push to the remote failed." };
  }
  if (state === "degraded") {
    return {
      tone: "problem",
      label: "Writes blocked",
      summary: "Writes are blocked until the KB and its remote agree again.",
    };
  }
  if (state === "disabled") {
    return {
      tone: "warning",
      label: "Not versioned",
      summary: "This KB is not a git repository: it has no history and no copy anywhere.",
    };
  }
  if (state === "no_remote" || status.has_remote === false) {
    return { tone: "warning", label: "No remote", summary: "This KB has no remote: it is not backed up." };
  }

  const ahead = status.unpushed_commits;
  const behind = status.behind_commits;
  if (typeof ahead === "number" && ahead > 0) {
    return {
      tone: "attention",
      label: `${ahead} to push`,
      summary: `${plural(ahead, "commit is", "commits are")} waiting to be pushed.`,
    };
  }
  if (typeof behind === "number" && behind > 0) {
    return {
      tone: "attention",
      label: `${behind} behind`,
      summary: `${plural(behind, "commit is", "commits are")} waiting to be pulled.`,
    };
  }
  if (state === "pending") {
    return { tone: "attention", label: "Push pending", summary: "A push is waiting to run." };
  }
  if (state === "clean" && ahead === 0 && behind === 0) {
    return { tone: "ok", label: "On remote", summary: "Everything is on the remote." };
  }
  return {
    tone: "attention",
    label: "Not verified",
    summary: "The KB cannot confirm that everything is on the remote.",
  };
}

/** A timestamp from the server as the Atlas prints it: English, local time. */
export function formatWhen(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return iso;
  return date.toLocaleString("en", { dateStyle: "medium", timeStyle: "short" });
}
