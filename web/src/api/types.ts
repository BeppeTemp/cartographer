// Mirrors the server's /api/ui/v1 shapes (D226). Kept hand-written rather than
// generated: the surface is five routes, and a generator would be a build step
// nobody can run from `go install`.

export interface KBCapability {
  state: string;
  setting: string;
}

export interface KBSummary {
  name: string;
  status: string;
  ready: boolean;
  capabilities?: Record<string, KBCapability>;
  /** Whether this principal may open the Artifacts panel: artifacts are
   *  whole-KB resources (D238). */
  artifacts: boolean;
  /** Whether this principal may open the Secrets panel (whole-KB, like artifacts). */
  secrets?: boolean;
}

/** An agent client an artifact is materialized for. */
export interface ArtifactClient {
  id: string;
  name: string;
}

export interface ArtifactFile {
  path: string;
  sha256: string;
  size: number;
  executable: boolean;
  /** Detail route only: the UTF-8 text, absent when binary or truncated. */
  content?: string;
  binary?: boolean;
  truncated?: boolean;
}

export interface Artifact {
  kind: string;
  name: string;
  description?: string;
  content_hash?: string;
  /** Absent for instructions and templates, which sync does not sign. */
  signed?: boolean;
  clients: ArtifactClient[];
  files: ArtifactFile[];
  /** Concepts the artifact references explicitly ([[id]] or a written id). */
  concepts?: string[];
  /** Lint findings on this artifact's files (D316). */
  findings?: ArtifactFinding[];
  /**
   * When a client last activated the artifact (D326): ISO 8601, or null when
   * nothing reported. Absent from a server that predates the field.
   */
  last_used?: string | null;
  last_used_provider?: string | null;
  last_used_days_ago?: number | null;
  /** The only signal is a client loading a catalogue that lists it: available, not seen used. */
  last_used_catalog_only?: boolean;
}

/** One artifact lint finding (D316). */
export interface ArtifactFinding {
  path: string;
  check: string;
  severity: string;
  message: string;
}

/** One kind of artifact a KB can hold, as provisioning describes it (D864). */
export interface KindInfo {
  kind: string;
  title: string;
  /** Where its files go in the KB; `<name>` stands for the artifact's name. */
  kb_path: string;
  /** A repository path with an anchor, "docs/sync.md#agents-and-hooks". */
  doc: string;
  /** No client receives it: it stays in the KB. */
  kb_only: boolean;
}

/** A KindInfo with what this KB ships of it: every kind comes, even one the KB has none of. */
export interface ArtifactKindInfo extends KindInfo {
  count: number;
  /** The clients that receive an artifact of this kind; empty for a KB-only kind. */
  clients: ArtifactClient[];
}

export interface ArtifactList {
  artifacts: Artifact[];
  counts: Record<string, number>;
  /**
   * Every kind a KB can hold, in reading order, whatever this KB ships (D864).
   * Absent from a server that predates it: the panel then lists only the
   * kinds that have an artifact.
   */
  kinds?: ArtifactKindInfo[];
  /** Why a skill was left out of what the KB ships. */
  issues: string[];
  /** Every artifact finding by check, including those no listed artifact owns (D316). */
  finding_counts?: Record<string, number>;
  /** The same findings by severity. */
  finding_severities?: Record<string, number>;
}

export interface CollectionSummary {
  name: string;
  title?: string;
  kind: string;
  concepts: number;
  expanded_concepts: number;
}

export interface Overview {
  collections: CollectionSummary[];
  concepts: {
    total: number;
    by_type: Record<string, number>;
    by_status: Record<string, number>;
  };
  lint: {
    total: number;
    by_severity: Record<string, number>;
    by_check: Record<string, number>;
  };
  git?: { has_remote: boolean; remote_url?: string };
}

export interface GraphNode {
  id: string;
  collection?: string;
  /** The frontmatter title, when the concept has one: what the UI names a
   *  node by. The id is the fallback. */
  title?: string;
  type?: string;
  status?: string;
  expanded?: boolean;
  self_link?: boolean;
  in_degree: number;
  out_degree: number;
  /** Global PageRank on the caller's visible graph (D244). */
  pagerank?: number;
  /** Rank of the node's community in GraphSnapshot.communities (D244). */
  community?: number;
}

/** One community of the visible graph, computed by the server (D244). */
export interface SnapshotCommunity {
  /** 0-based: 0 is the largest. */
  rank: number;
  size: number;
  /** The member a legend names the community after. */
  anchor: string;
  /** The anchor's title: a Map's snapshot rarely holds the anchor's node. */
  anchor_title?: string;
  /** 1..12, or 0 for singletons and the long tail. */
  slot: number;
}

export interface GraphEdge {
  source: string;
  target: string;
}

export interface BrokenTarget {
  source: string;
  target: string;
}

export interface GraphSnapshot {
  nodes: GraphNode[];
  edges: GraphEdge[];
  broken?: BrokenTarget[];
  truncated?: boolean;
  total_nodes: number;
  total_edges: number;
  total_broken?: number;
  limit: number;
  /** Covers the whole visible graph, not only the returned nodes. */
  communities?: SnapshotCommunity[];
}

export interface OutlineEntry {
  level: number;
  title: string;
  bytes: number;
}

export interface Concept {
  id: string;
  title: string;
  collection: string;
  frontmatter: Record<string, unknown>;
  body: string;
  body_bytes: number;
  outline: OutlineEntry[];
  content_hash: string;
  outbound: string[];
  inbound: string[];
  broken: string[];
  /** Skills, agents and hooks that reference this concept explicitly; absent
   *  for a principal that cannot see the whole KB. */
  used_by?: { kind: string; name: string }[];
  /** The visible concepts this one replaced (they name it as superseded_by). */
  supersedes?: string[];
  /** The concept that replaced this one; "" when none, or when the caller cannot see it. */
  superseded_by?: string;
}

export type Severity = "info" | "warning" | "error";

export interface LintFinding {
  path: string;
  concept?: string;
  check: string;
  severity: string;
  message: string;
  /** Who acts on it (D365): the background repair, or the doctor session. */
  handler?: "auto" | "doctor";
}

export interface LintReport {
  findings: LintFinding[];
  count: number;
  total: number;
  by_severity: Record<string, number>;
  by_check: Record<string, number>;
  /** Where each reported check can be accepted with lint_ignore (D313). */
  acceptability?: Record<string, "concept" | "map" | "artifact" | "none">;
  severity_min: string;
}

/** GET /kbs/{kb}/search: the search tool's own response. */
export interface SearchHit {
  id: string;
  title?: string;
  snippet?: string;
  score: number;
  /** The page's status is archived (D322); a default search leaves these out. */
  archived?: boolean;
}
export interface SearchResponse {
  query: string;
  count: number;
  /** null when nothing matched. */
  results: SearchHit[] | null;
  /** Nothing live matched, so the hits are archived pages (D322). */
  archived_fallback?: boolean;
}

/** GET /kbs/{kb}/changes: changes_since's own response. */
export interface ConceptChange {
  id: string;
  change: string;
  last_at: string;
  /** The newest change in the window that was not the server's upkeep:
   *  when the content last changed. Absent when only upkeep touched it. */
  last_edit_at?: string;
  /** Created inside the window, whatever its newest change was. */
  added?: boolean;
  authors?: string[];
  ops?: string[];
  reasons?: string[];
}
export interface ChangesResponse {
  since: string;
  commit_count: number;
  truncated?: boolean;
  concepts: ConceptChange[];
}

/** GET /kbs/{kb}/work: work_list for the caller (D302). */
export interface WorkItem {
  text: string;
  section?: string;
  line: number;
}
export interface WorkEntry {
  id: string;
  title?: string;
  type?: string;
  map?: string;
  status?: string;
  open_phase: boolean;
  timestamp?: string;
  age_days?: number;
  stale: boolean;
  items: WorkItem[];
}
export interface WorkResponse {
  total: number;
  open_concepts: number;
  by_status: Record<string, number>;
  by_map: Record<string, number>;
  open_items: number;
  entries: WorkEntry[];
  next_offset?: number;
}

/** GET /kbs/{kb}/status: the parts of kb_status the home reads. Both are
 *  omitted by the server when empty. */
export interface KBStatus {
  open_gaps?: {
    total: number;
    by_kind?: Record<string, number>;
    recent?: { id: string; title?: string; kind?: string; subjects?: string[] }[];
  };
  /** resolved: the same search now finds something (D319); sorted last. */
  search_misses?: { query: string; count: number; last_seen?: string; resolved?: boolean }[];
  stale_count?: number;
}

/** GET /kbs/{kb}/maintenance/summary (D323). */
export interface MaintenanceRepair {
  sha: string;
  at: string;
  subject: string;
  reason?: string;
  files: number;
  /** Made by the server's own heartbeat, not by a kb_repair someone ran. */
  background: boolean;
  /** The command that undoes it: shown, never run, by the Atlas. */
  revert: string;
}
export interface MaintenanceRun {
  at: string;
  skipped?: string;
  checks?: { check: string; applied: number; skipped?: number; commit?: string; error?: string }[];
}
/** One daily sample of the whole-KB lint totals (D370). */
export interface LintSample {
  at: string;
  total: number;
  by_severity: Record<string, number>;
  by_handler: Record<string, number>;
}
export interface MaintenanceSummary {
  auto_repair: { enabled: boolean; default: boolean; checks: string[]; interval_days: number };
  last_auto_repair: MaintenanceRun | null;
  last_doctor?: string;
  next_doctor?: string;
  doctor_interval_days: number;
  /** The background repair's runs of the last 30 days, newest first (D365). */
  runs?: MaintenanceRun[];
  /** One sample a day of the lint totals, oldest first, 90 days at most (D370). */
  lint_history?: LintSample[];
  repairs: MaintenanceRepair[];
}

/** GET /kbs/{kb}/maintenance/review (D704): the doctor's agenda beyond lint. */
export interface ReviewItem {
  kind: string;
  concepts: string[];
  evidence: string;
  suggested_action: string;
  term?: string;
  template_slug?: string;
  weight: number;
}
export interface MaintenanceReview {
  items: ReviewItem[];
}

/** GET /kbs/{kb}/checks: every lint check the server runs (D365). */
export interface CheckCatalog {
  categories: string[];
  checks: {
    name: string;
    category: string;
    severity: string;
    fixable: boolean;
    auto: boolean;
    /** False when the check cannot run on this KB (D371): `reason` says why, and a zero would not mean clean. */
    active?: boolean;
    reason?: string;
  }[];
}

/** One sops-encrypted value: the ciphertext and what the browser needs to open it. */
export interface SecretEntry {
  /** RFC 6901 pointer, list indices included. */
  pointer: string;
  /** The additional data sops authenticated the value with. */
  aad_path: string;
  /** The raw ENC[AES256_GCM,...] string. */
  enc: string;
  referenced_by: string[];
}

export interface SecretFile {
  path: string;
  entries: SecretEntry[];
  recipients: string[];
  age_stanzas: string[];
  lastmodified?: string;
  source_of: string[];
  error?: string;
}

export interface SecretList {
  files: SecretFile[];
  /** The secrets kind's descriptor (D864); absent from a server that predates it. */
  kind?: KindInfo;
}

/** One key of GET /kbs/{kb}/placeholders: a {{repo:key}} or {{path:key}}
 *  placeholder the KB declares in paths.yaml (D263) and/or cites. */
export interface PlaceholderKey {
  kind: "repo" | "path";
  key: string;
  /** Declaration fields, present only when `declared`. `remote` is a repo
   *  key's normalized "host/owner/name". */
  description?: string;
  default?: string;
  remote?: string;
  /** paths.yaml declares the key. Always false for a caller that cannot see
   *  the whole KB: the registry is withheld from it. */
  declared: boolean;
  /** Concepts (ids) the caller can see that cite the key, sorted. */
  concepts: string[];
  /** Artifacts ("kind/name") citing the key, sorted; empty for a caller that
   *  cannot see the whole KB. */
  artifacts: string[];
  /** Other KBs the caller sees whole that declare the same key with a
   *  different default. Omitted when there is none. */
  conflicts?: { kb: string; default: string }[];
}

/** GET /kbs/{kb}/placeholders: sorted by kind, then key. Declared keys that
 *  nothing cites are listed with empty `concepts` and `artifacts`. */
export interface PlaceholderList {
  /** paths.yaml exists (and the caller may read it). */
  registry: boolean;
  keys: PlaceholderKey[];
}

/** One Contradiction concept (GET /kbs/{kb}/contradictions), as the
 *  contradiction_report tool lists it. */
export interface Contradiction {
  id: string;
  title?: string;
  /** The concepts the contradiction is about. */
  involves?: string[];
  contradiction_kind?: string;
  /** "open" unless the concept says otherwise. */
  resolution_status: string;
}

/** GET /kbs/{kb}/contradictions?status=: gap-kind contradictions excluded. */
export interface ContradictionList {
  contradictions: Contradiction[];
}

/** One Map or Journal contract, as the map_list tool returns it. */
export interface MapContract {
  name: string;
  title?: string;
  kind?: string;
  ontology_mode?: string;
  concept_types?: string[];
  /** Checks the map accepts as a whole (D306). */
  lint_ignore?: string[];
  /** Whole-KB callers only (0 otherwise). */
  expanded_concepts: number;
  /** Effective stale_open threshold in days (D346). */
  stale_after?: number;
  /** stale_after is the default for the map's kind, not set in _map.md. */
  stale_after_defaulted?: boolean;
  /** Allowed values per frontmatter field (D343); a by-type list replaces the
   *  map-wide one for that type. */
  field_values?: Record<string, string[]>;
  field_values_by_type?: Record<string, Record<string, string[]>>;
  /** The rest of the _map.md contract, as map_list returns it; every key is
   *  omitted when the contract does not set it. */
  description?: string;
  required_fields?: string[];
  required_fields_by_type?: Record<string, string[]>;
  forbidden_fields?: string[];
  require_index_entry?: boolean;
  templates?: string[];
  default_template?: string;
  /** Effective: a Map that declares templates is strict without saying so (D371). */
  require_template?: boolean;
  template_sections?: boolean;
  open_field?: string;
  open_statuses?: string[];
  open_markers?: string[];
  /** A journal's effective harvest_after in days (D322); absent when
   *  `harvest_off` says harvest_after: 0 switched it off (D347). */
  harvest_after?: number;
  harvest_after_defaulted?: boolean;
  harvest_off?: boolean;
  promote_to?: string;
  procedure_headings?: string[];
  /** This Map defines the KB's terms. */
  glossary?: boolean;
  work_map?: string;
  /** "generated": the server maintains the concept list in index.md. */
  index?: string;
  /** Canonical value -> the synonyms the server treats as it. */
  value_synonyms?: Record<string, string[]>;
  machine_path_allow_prefixes?: string[];
  thresholds?: MapThresholds;
  /** Entries of _map.md the server tolerated but could not read. */
  malformed?: { key: string; reason: string }[];
}

/** The cost and page-name keys a contract sets (D301, D313, D315); a key left
 *  out runs at the built-in default. `title_max_length: 0` turns the length
 *  rule off. */
export interface MapThresholds {
  repeated_fact_min?: number;
  hotspot_in_degree?: number;
  hotspot_bytes?: number;
  oversize_bytes?: number;
  oversize_concepts?: number;
  title_max_length?: number;
  forbidden_title_terms?: string[];
}

/** GET /kbs/{kb}/maps. */
export interface MapList {
  maps: MapContract[];
}

/** One commit that changed a concept (GET /kbs/{kb}/history), newest first,
 *  following renames. */
export interface ConceptRevision {
  sha: string;
  /** RFC3339, UTC. */
  at: string;
  author: string;
  subject: string;
  /** The file's path at that commit. */
  path: string;
  agent?: string;
  moved_to?: string;
  moved_from?: string;
  reason?: string;
  accepted_findings?: string[];
}

/** GET /kbs/{kb}/history?id=&limit=. `note` is "no git history" when the KB
 *  keeps none (then `revisions` is empty). */
export interface ConceptHistory {
  id: string;
  revisions: ConceptRevision[];
  truncated: boolean;
  note?: string;
}

/** GET /kbs/{kb}/journal?n=: the last n entries of the root log.md, as
 *  markdown ("" when there are none). Whole-KB callers only. */
export interface JournalTail {
  text: string;
}

/** One Source concept of the ledger (GET /kbs/{kb}/sources), oldest first. */
export interface SourceEntry {
  id: string;
  title?: string;
  source_kind?: string;
  /** "pending", "ingested" or "skipped". */
  ingest_status: string;
  timestamp?: string;
  /** How many visible concepts cite it in their provenance. */
  cited_by: number;
  cited_by_ids: string[];
}

/** GET /kbs/{kb}/sources?status=&limit= (status: pending by default, or "*"). */
export interface SourceList {
  sources: SourceEntry[];
}

/** One non-Markdown file of an expanded concept. */
export interface AssetEntry {
  /** Relative to the concept's folder. */
  path: string;
  size: number;
  sha256: string;
  executable: boolean;
  /** Over the asset size limit: listed, but neither readable nor writable. */
  oversized?: boolean;
}

/** GET /kbs/{kb}/assets?id=: 400 for a concept that is not expanded. */
export interface AssetList {
  id: string;
  assets: AssetEntry[];
}

/** Which layer set a setting's effective value (D743). */
export type SettingOrigin = "default" | "file" | "env" | "flag" | "kb";

export interface SettingEntry {
  key: string;
  /** Redacted by the server for a secret: "(set)" or "(unset)". */
  value: unknown;
  origin: SettingOrigin;
  description: string;
  default: string;
  /** The schema's kind of value, and the accepted values in free text. */
  type?: string;
  values?: string;
  env?: string;
  flag?: string;
  restart?: boolean;
  secret?: boolean;
}

export interface SettingsKBGit {
  has_remote: boolean;
  sync: boolean;
  push_state?: string;
  push_last_error?: string;
  unpushed_commits?: number;
  behind_commits?: number;
  last_push_at?: string;
  last_sync_in_at?: string;
  remote_url?: string;
}

export interface SettingsKB {
  name: string;
  entries: SettingEntry[];
  /** The KB's settings in force as its settings.yaml, commented by the schema (D818). */
  yaml?: string;
  /** Absent for a caller that cannot see the whole KB. */
  git?: SettingsKBGit;
}

export interface SettingsView {
  version: string;
  latest_version?: string;
  update_channel?: string;
  server: SettingEntry[];
  /** The server's configuration in force as one commented YAML file (D818). */
  server_yaml?: string;
  kbs: SettingsKB[];
  /** The viewer's own clients, as each machine last reported them (D818). */
  clients?: SettingsClient[];
}

/** One machine's client settings, reported by its last sync (D818). */
export interface SettingsClient {
  machine: string;
  version: string;
  server_url: string;
  entries: SettingEntry[];
  /** The machine's .cartographer.yaml as it is on disk; "" when it has none. */
  yaml: string;
  received_at: string;
  /** The connected agent clients (absent from an older client). */
  providers?: ClientProvider[];
  /** The placeholder keys the last sync met, as they resolve on the machine. */
  placeholders?: ClientPlaceholder[];
}

/** One connected agent client of a machine (D818). */
export interface ClientProvider {
  name: string;
  /** "provider": every KB under the home directory; "workspace": per bound folder. */
  scope: string;
  /** For a workspace-scoped client (issue 737): "workspace" when a bound folder's
   *  session sees only its KBs, "none" when the global entry still exposes every KB. */
  isolation?: "workspace" | "none";
  workspaces?: { path: string; remote?: string; kbs: string[] }[];
}

/** One placeholder key as it resolves on a machine (`cartographer paths list`). */
export interface ClientPlaceholder {
  key: string;
  kbs?: string[];
  path?: string;
  reason?: string;
  description?: string;
  ignored?: boolean;
}

/** One Service concept (GET /kbs/{kb}/services), filtered to what the caller sees. */
export interface ServiceEntry {
  id: string;
  title?: string;
  status?: string;
}

export interface ServiceList {
  services: ServiceEntry[];
}

/** One open git rebase conflict (GET /kbs/{kb}/conflicts, conflicts_list). Whole-KB callers only. */
export interface GitConflict {
  /** The concept id, or the git path of a reserved file (kind "reserved"). */
  concept_id: string;
  path: string;
  kind?: string;
  files?: string[];
  detected_at?: string;
  resolution_strategy?: string;
}

/** One audit entry (GET /kbs/{kb}/audit). Admin only. */
export interface AuditEntry {
  at: string;
  phase?: string;
  principal?: string;
  transport?: string;
  tool: string;
  read_only?: boolean;
  resources?: Record<string, string>;
  outcome: string;
  duration_ms?: number;
  commit_sha?: string;
}

export interface AuditLog {
  /** False when the server keeps no audit log. */
  enabled: boolean;
  state?: string;
  entries: AuditEntry[];
}

/** One KB rule of the caller's policy (GET /whoami). Absent maps = every map. */
export interface WhoamiRule {
  kb: string;
  write: boolean;
  maps?: string[];
  journals?: string[];
  types?: string[];
}

/** GET /whoami: the principal behind the request and its scopes. */
export interface Whoami {
  id: string;
  admin: boolean;
  permissions: WhoamiRule[];
}
