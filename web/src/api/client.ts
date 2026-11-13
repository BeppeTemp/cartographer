import { setServerNow } from "../lib/serverclock";
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
  ClusterGraph,
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
  FeedList,
  MaintenanceSummary,
  ComponentsResponse,
  Extensions,
  ReportCandidateList,
  ReportDraft,
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

/** The public read-only API: the only prefix a plugin view may read (D1938). */
export const PUBLIC_PREFIX = "/api/v1";

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

async function get<T>(path: string, signal?: AbortSignal, base: string = BASE): Promise<T> {
  // The static demo has no public API: a view's source is a 404 there.
  if (STATIC_MODE && base !== BASE) throw new ApiError(404, "not_found", "not found");
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
    const url = live ? liveURL(path) : STATIC_MODE ? staticURL(staticKey(path)) : base + path;
    response = await fetch(url, { headers, signal, credentials: "same-origin" });
  } catch (err) {
    if (err instanceof DOMException && err.name === "AbortError") throw err;
    throw new ApiError(0, "offline", "Meridian is unreachable");
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

/**
 * An optional leg of a fetch: a failure is `null`, except an abort, which is
 * rethrown so the caller never writes the result of a request it cancelled.
 */
export function optional<T>(p: Promise<T>): Promise<T | null> {
  return p.catch((err: unknown) => {
    if (err instanceof DOMException && err.name === "AbortError") throw err;
    return null;
  });
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

/** What a clustered graph request asks for besides the KB. */
export interface ClusterRequest {
  scope: string | null;
  unfold: string[];
  focus: string | null;
}

export function fetchGraphClusters(kb: string, req: ClusterRequest, signal?: AbortSignal): Promise<ClusterGraph> {
  const params = new URLSearchParams();
  if (req.scope) params.set("scope", req.scope);
  if (req.unfold.length > 0) params.set("unfold", req.unfold.join(","));
  if (req.focus) params.set("focus", req.focus);
  const query = params.toString();
  return get<ClusterGraph>(`/kbs/${encodeURIComponent(kb)}/graph/clusters${query ? `?${query}` : ""}`, signal);
}

export function fetchConcept(kb: string, id: string, signal?: AbortSignal): Promise<Concept> {
  return get<Concept>(`/kbs/${encodeURIComponent(kb)}/concept?id=${encodeURIComponent(id)}`, signal);
}

/** The most findings one lint page holds (the server's own ceiling); demo/export.mjs saves this first request. */
export const LINT_PAGE = 5000;

/**
 * Every finding of the KB, read a page at a time: Health groups and counts them
 * all, but a KB with tens of thousands of findings is not one response.
 */
export async function fetchLint(kb: string, signal?: AbortSignal): Promise<LintReport> {
  const base = `/kbs/${encodeURIComponent(kb)}/lint?limit=${LINT_PAGE}`;
  let report = await get<LintReport>(base, signal);
  while (report.next_cursor) {
    const cursor = report.next_cursor;
    const page = await get<LintReport>(`${base}&cursor=${encodeURIComponent(cursor)}`, signal);
    // A cursor that does not move would loop forever: stop with what arrived.
    report = { ...page, findings: [...report.findings, ...page.findings], next_cursor: page.next_cursor === cursor ? undefined : page.next_cursor };
  }
  return report;
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

/** The components that concern this KB (D1901); 404 for a caller that does not see the whole KB. */
export function fetchComponents(kb: string, signal?: AbortSignal): Promise<ComponentsResponse> {
  return get<ComponentsResponse>(`/kbs/${encodeURIComponent(kb)}/components`, signal).then((res) => {
    setServerNow(res?.server?.now);
    return res;
  });
}

/**
 * The Surveyors' report candidates the caller may see (D1925); `404 feedback_off`
 * when the operator switched feedback off. Server-wide, not per KB.
 */
export function fetchFeedbackCandidates(signal?: AbortSignal): Promise<ReportCandidateList> {
  return get<ReportCandidateList>("/feedback/candidates", signal);
}

/** A redacted draft of one candidate: a GET, pure computation on the server, nothing stored or sent. */
export function fetchFeedbackDraft(id: string, signal?: AbortSignal): Promise<ReportDraft> {
  return get<ReportDraft>(`/feedback/candidates/draft?id=${encodeURIComponent(id)}`, signal);
}

/** The Sources whose last poll failed, newest check first. */
export function fetchFeeds(kb: string, signal?: AbortSignal): Promise<FeedList> {
  return get<FeedList>(`/kbs/${encodeURIComponent(kb)}/feeds`, signal);
}

/** Last and next doctor run (the rest of the summary is not read by the Atlas). */
export function fetchMaintenanceSummary(kb: string, signal?: AbortSignal): Promise<MaintenanceSummary> {
  return get<MaintenanceSummary>(`/kbs/${encodeURIComponent(kb)}/maintenance/summary`, signal);
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

/** Every registered Source whatever its ingest status (the Settings Surveyor tab counts them); the server caps the list at 500. */
export function fetchAllSources(kb: string, signal?: AbortSignal): Promise<SourceList> {
  return get<SourceList>(`/kbs/${encodeURIComponent(kb)}/sources?status=*&limit=500`, signal);
}

export function fetchAssets(kb: string, id: string, signal?: AbortSignal): Promise<AssetList> {
  return get<AssetList>(`/kbs/${encodeURIComponent(kb)}/assets?id=${encodeURIComponent(id)}`, signal);
}

/** The approved views, apps and plugins; a 404 is "no extensions", any other failure rejects. */
export async function fetchExtensions(signal?: AbortSignal): Promise<Extensions> {
  try {
    const body = await get<Partial<Extensions>>("/extensions", signal);
    const list = <T,>(v: T[] | undefined) => (Array.isArray(v) ? v : []);
    return { views: list(body?.views), apps: list(body?.apps), plugins: list(body?.plugins) };
  } catch (err) {
    // Only a 404 (an older server, the static demo) means "no extensions"; any other failure is the caller's to ride out.
    if (err instanceof ApiError && err.isNotFound) return { views: [], apps: [], plugins: [] };
    throw err;
  }
}

/**
 * A view's data: one GET of the declared route as the Atlas's own principal. The server validated the
 * route at start; it is checked again so a hostile /extensions answer cannot make the Atlas read another URL.
 */
export function fetchViewSource(path: string, signal?: AbortSignal): Promise<unknown> {
  const [route = "", query = ""] = path.split("?", 2);
  const ok =
    route.startsWith(PUBLIC_PREFIX + "/") && !/[:\\\s{}#]|\/\/|\.\.|%2e|%2f|%5c/i.test(route) && !/[#\s]/.test(query);
  if (!ok) return Promise.reject(new ApiError(400, "bad_request", "this view names a route the Atlas does not read"));
  return get<unknown>(path, signal, "");
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
