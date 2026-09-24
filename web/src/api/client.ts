import type { Artifact, ArtifactList, Concept, GraphSnapshot, KBSummary, LintReport, Overview } from "./types";

const BASE = "/api/ui/v1";

/**
 * ApiError carries the server's machine-readable code so a panel can tell a
 * bad request from a missing concept without matching on prose. Status 0 means
 * the request never reached the server (offline, DNS, refused connection) --
 * a distinct state from any answer the server could give.
 */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly field?: string;

  constructor(status: number, code: string, message: string, field?: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.field = field;
  }

  get isUnauthorized(): boolean {
    return this.status === 401;
  }

  get isNotFound(): boolean {
    return this.status === 404;
  }

  get isOffline(): boolean {
    return this.status === 0;
  }
}

/**
 * The bearer token lives in this module and nowhere else.
 *
 * In memory by default. "Remember for this tab" opts into sessionStorage,
 * which dies with the tab. localStorage is never used and the token never
 * reaches a URL, a log line or an error body: a token in any of those outlives
 * the session that was given it.
 */
const SESSION_KEY = "cartographer.token";
let token: string | null = null;

export function setToken(value: string | null, remember = false): void {
  token = value;
  if (!remember) return;
  try {
    if (value === null) sessionStorage.removeItem(SESSION_KEY);
    else sessionStorage.setItem(SESSION_KEY, value);
  } catch {
    // A browser with storage disabled keeps the in-memory token; the only
    // cost is retyping it after a reload.
  }
}

/** Picks up a token remembered for this tab. An in-memory token wins: the
 *  boot after a sign-in without "remember" runs this too, and replacing the
 *  token just typed with an empty tab store signed every such user straight
 *  back out (D228). */
export function restoreToken(): string | null {
  if (token) return token;
  try {
    token = sessionStorage.getItem(SESSION_KEY);
  } catch {
    token = null;
  }
  return token;
}

export function hasToken(): boolean {
  return token !== null && token !== "";
}

export function clearToken(): void {
  token = null;
  try {
    sessionStorage.removeItem(SESSION_KEY);
  } catch {
    // Nothing to clear.
  }
}

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json" };
  if (token) headers.Authorization = `Bearer ${token}`;

  let response: Response;
  try {
    response = await fetch(BASE + path, { headers, signal, credentials: "omit" });
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") throw err;
    throw new ApiError(0, "offline", "the Cartographer server is unreachable");
  }

  if (!response.ok) {
    let code = "error";
    let message = `request failed with status ${response.status}`;
    let field: string | undefined;
    try {
      const body = (await response.json()) as { error?: { code?: string; message?: string; field?: string } };
      if (body.error) {
        code = body.error.code ?? code;
        message = body.error.message ?? message;
        field = body.error.field;
      }
    } catch {
      // A non-JSON body (a proxy's error page) keeps the generic message.
    }
    throw new ApiError(response.status, code, message, field);
  }
  return (await response.json()) as T;
}

export function fetchKBs(signal?: AbortSignal): Promise<{ kbs: KBSummary[] }> {
  return get<{ kbs: KBSummary[] }>("/kbs", signal);
}

export function fetchOverview(kb: string, signal?: AbortSignal): Promise<Overview> {
  return get<Overview>(`/kbs/${encodeURIComponent(kb)}/overview`, signal);
}

export function fetchGraph(
  kb: string,
  scope: string | null,
  signal?: AbortSignal,
): Promise<GraphSnapshot> {
  const query = scope ? `?scope=${encodeURIComponent(scope)}` : "";
  return get<GraphSnapshot>(`/kbs/${encodeURIComponent(kb)}/graph${query}`, signal);
}

export function fetchConcept(kb: string, id: string, signal?: AbortSignal): Promise<Concept> {
  return get<Concept>(`/kbs/${encodeURIComponent(kb)}/concept?id=${encodeURIComponent(id)}`, signal);
}

export function fetchLint(
  kb: string,
  severityMin: string,
  scope: string | null,
  signal?: AbortSignal,
): Promise<LintReport> {
  const scoped = scope ? `&scope=${encodeURIComponent(scope)}` : "";
  return get<LintReport>(
    `/kbs/${encodeURIComponent(kb)}/lint?severity_min=${encodeURIComponent(severityMin)}${scoped}`,
    signal,
  );
}

export function fetchArtifacts(kb: string, signal?: AbortSignal): Promise<ArtifactList> {
  return get<ArtifactList>(`/kbs/${encodeURIComponent(kb)}/artifacts`, signal);
}

export function fetchArtifact(kb: string, kind: string, name: string, signal?: AbortSignal): Promise<Artifact> {
  return get<Artifact>(
    `/kbs/${encodeURIComponent(kb)}/artifact?kind=${encodeURIComponent(kind)}&name=${encodeURIComponent(name)}`,
    signal,
  );
}
