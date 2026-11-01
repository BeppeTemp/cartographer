import type {
  Artifact,
  ArtifactList,
  SecretList,
  AssetList,
  ConceptHistory,
  AuditLog,
  GitConflict,
  ServiceList,
  Whoami,
  JournalTail,
  MapList,
  PlaceholderList,
  SourceList,
  SettingsView,
  Concept,
  GraphSnapshot,
  KBSummary,
  LintReport,
  Overview,
  ChangesResponse,
  KBStatus,
  MaintenanceReview,
  RetiredResponse,
  SearchResponse,
  WorkResponse,
  CheckCatalog,
  ReleasesView,
} from "./types";
import {
  LIVE_ORIGIN,
  STATIC_MODE,
  liveHealthURL,
  liveURL,
  resolveSource,
  staticKey,
  staticReleases,
  staticSearch,
  staticURL,
} from "./static";

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

/** The server's version, from the unauthenticated /health beside /ui/; null
 *  when it cannot be read, which the page simply leaves out. */
export async function fetchServerVersion(signal?: AbortSignal): Promise<string | null> {
  try {
    const live = STATIC_MODE && LIVE_ORIGIN && (await resolveSource()) === "live";
    const response = await fetch(
      live
        ? liveHealthURL()
        : STATIC_MODE
          ? staticURL("health")
          : new URL("../health", new URL(import.meta.env.BASE_URL, window.location.href)).pathname,
      {
        headers: { Accept: "application/json" },
        signal,
      },
    );
    if (!response.ok) return null;
    const body = (await response.json()) as { version?: unknown };
    return typeof body.version === "string" && body.version ? body.version : null;
  } catch {
    return null;
  }
}

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  // Decided once per page load, before the first read (D800).
  const live = STATIC_MODE && LIVE_ORIGIN !== "" && (await resolveSource()) === "live";
  if (STATIC_MODE && !live) {
    const found = await staticSearch(path);
    if (found) return found as T;
  }
  const headers: Record<string, string> = { Accept: "application/json" };
  if (token) headers.Authorization = `Bearer ${token}`;

  let response: Response;
  try {
    // same-origin: a proxy session cookie (SSO in front of the UI) must reach the API;
    // never cross-origin, so no third party sees it.
    const url = live ? liveURL(path) : STATIC_MODE ? staticURL(staticKey(path)) : BASE + path;
    response = await fetch(url, { headers, signal, credentials: "same-origin" });
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

export function fetchGraph(kb: string, scope: string | null, signal?: AbortSignal): Promise<GraphSnapshot> {
  const query = scope ? `?scope=${encodeURIComponent(scope)}` : "";
  return get<GraphSnapshot>(`/kbs/${encodeURIComponent(kb)}/graph${query}`, signal);
}

export function fetchConcept(kb: string, id: string, signal?: AbortSignal): Promise<Concept> {
  return get<Concept>(`/kbs/${encodeURIComponent(kb)}/concept?id=${encodeURIComponent(id)}`, signal);
}

export function fetchLint(kb: string, scope: string | null, signal?: AbortSignal): Promise<LintReport> {
  const scoped = scope ? `?scope=${encodeURIComponent(scope)}` : "";
  return get<LintReport>(`/kbs/${encodeURIComponent(kb)}/lint${scoped}`, signal);
}

/** includeArchived asks for archived pages too; a search leaves them out by default (D820). */
export function fetchSearch(
  kb: string,
  query: string,
  signal?: AbortSignal,
  includeArchived = false,
): Promise<SearchResponse> {
  const archived = includeArchived ? "&include_archived=1" : "";
  return get<SearchResponse>(
    `/kbs/${encodeURIComponent(kb)}/search?q=${encodeURIComponent(query)}&limit=20${archived}`,
    signal,
  );
}

/** What the KB retired, where it went and why (D820). */
export function fetchRetired(kb: string, signal?: AbortSignal): Promise<RetiredResponse> {
  return get<RetiredResponse>(`/kbs/${encodeURIComponent(kb)}/retired`, signal);
}

export function fetchChanges(kb: string, since: string, signal?: AbortSignal): Promise<ChangesResponse> {
  return get<ChangesResponse>(
    `/kbs/${encodeURIComponent(kb)}/changes?since=${encodeURIComponent(since)}&limit=500`,
    signal,
  );
}

/** The caller's open work, up to the route's cap (D302), with the kind of each contradiction. */
export function fetchWork(kb: string, signal?: AbortSignal): Promise<WorkResponse> {
  return get<WorkResponse>(`/kbs/${encodeURIComponent(kb)}/work?limit=200&fields=contradiction_kind`, signal);
}

/** Every lint check the server runs, by category (D365). */
export function fetchChecks(kb: string, signal?: AbortSignal): Promise<CheckCatalog> {
  return get<CheckCatalog>(`/kbs/${encodeURIComponent(kb)}/checks`, signal);
}

/** What a doctor session decides beyond lint: kb_review's proposals (D704). */
export function fetchMaintenanceReview(kb: string, signal?: AbortSignal): Promise<MaintenanceReview> {
  return get<MaintenanceReview>(`/kbs/${encodeURIComponent(kb)}/maintenance/review`, signal);
}

/** An opaque token that changes whenever the KB's concepts do (D337). */
export function fetchRevision(kb: string, signal?: AbortSignal): Promise<{ revision: string }> {
  return get<{ revision: string }>(`/kbs/${encodeURIComponent(kb)}/revision`, signal);
}

/** When each visible concept entered the KB (its first commit), by id. */
export function fetchBirths(kb: string, signal?: AbortSignal): Promise<{ births: Record<string, string> }> {
  return get<{ births: Record<string, string> }>(`/kbs/${encodeURIComponent(kb)}/births`, signal);
}

export function fetchStatus(kb: string, signal?: AbortSignal): Promise<KBStatus> {
  return get<KBStatus>(`/kbs/${encodeURIComponent(kb)}/status`, signal);
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

export function fetchSecrets(kb: string, signal?: AbortSignal): Promise<SecretList> {
  return get<SecretList>(`/kbs/${encodeURIComponent(kb)}/secrets`, signal);
}

export function fetchPlaceholders(kb: string, signal?: AbortSignal): Promise<PlaceholderList> {
  return get<PlaceholderList>(`/kbs/${encodeURIComponent(kb)}/placeholders`, signal);
}

export function fetchMaps(kb: string, signal?: AbortSignal): Promise<MapList> {
  return get<MapList>(`/kbs/${encodeURIComponent(kb)}/maps`, signal);
}

export function fetchHistory(kb: string, id: string, signal?: AbortSignal): Promise<ConceptHistory> {
  return get<ConceptHistory>(`/kbs/${encodeURIComponent(kb)}/history?id=${encodeURIComponent(id)}`, signal);
}

export function fetchJournal(kb: string, signal?: AbortSignal): Promise<JournalTail> {
  return get<JournalTail>(`/kbs/${encodeURIComponent(kb)}/journal`, signal);
}

/** The sources registered and not ingested yet: the Work page's "to ingest" column. */
export function fetchPendingSources(kb: string, signal?: AbortSignal): Promise<SourceList> {
  // The server's maximum (source_list caps at 500; its default of 50 would cut
  // the column without saying so).
  return get<SourceList>(`/kbs/${encodeURIComponent(kb)}/sources?status=pending&limit=500`, signal);
}

export function fetchAssets(kb: string, id: string, signal?: AbortSignal): Promise<AssetList> {
  return get<AssetList>(`/kbs/${encodeURIComponent(kb)}/assets?id=${encodeURIComponent(id)}`, signal);
}

export function fetchSettings(signal?: AbortSignal): Promise<SettingsView> {
  return get<SettingsView>("/settings", signal);
}

export function fetchServices(kb: string, signal?: AbortSignal): Promise<ServiceList> {
  return get<ServiceList>(`/kbs/${encodeURIComponent(kb)}/services`, signal);
}

/** Whole-KB: a narrowed principal gets a 404, which callers leave out of the page. */
export function fetchConflicts(kb: string, signal?: AbortSignal): Promise<GitConflict[]> {
  return get<GitConflict[]>(`/kbs/${encodeURIComponent(kb)}/conflicts`, signal);
}

/** Admin only: any other principal gets a 404. */
export function fetchAudit(kb: string, signal?: AbortSignal): Promise<AuditLog> {
  return get<AuditLog>(`/kbs/${encodeURIComponent(kb)}/audit`, signal);
}

export function fetchWhoami(signal?: AbortSignal): Promise<Whoami> {
  return get<Whoami>("/whoami", signal);
}

/** The release history (D862). The static demo has no server to ask GitHub on
 *  its behalf, so its snapshot answers "disabled" locally; a live demo origin
 *  answers like any server. */
export async function fetchReleases(signal?: AbortSignal): Promise<ReleasesView> {
  if (STATIC_MODE && !(LIVE_ORIGIN !== "" && (await resolveSource()) === "live")) return staticReleases();
  return get<ReleasesView>("/releases", signal);
}
