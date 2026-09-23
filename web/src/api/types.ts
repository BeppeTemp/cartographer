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
  tool_prefix?: string;
  capabilities?: Record<string, KBCapability>;
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
}

export type Severity = "info" | "warning" | "error";

export interface LintFinding {
  path: string;
  concept?: string;
  check: string;
  severity: string;
  message: string;
}

export interface LintReport {
  findings: LintFinding[];
  count: number;
  total: number;
  by_severity: Record<string, number>;
  by_check: Record<string, number>;
  severity_min: string;
}
