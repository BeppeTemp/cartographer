// A synthetic, deterministic "team KB": ~110 notes in 5 communities.
export const COMMUNITIES = ["infra", "services", "incidents", "decisions", "runbooks"] as const;

const NAMES: Record<(typeof COMMUNITIES)[number], string[]> = {
  infra: ["ci", "k8s", "vpc", "dns", "queue", "cache", "registry", "secrets", "vault", "ingress", "backup", "cdn", "logging", "metrics", "tracing", "terraform", "runners", "storage", "mesh", "bastion", "cron", "certs"],
  services: ["api", "auth", "billing", "search", "gateway", "worker", "web", "mobile", "notify", "export", "import", "reports", "scheduler", "profile", "catalog", "orders", "payments", "webhooks", "admin", "sync", "media", "audit"],
  incidents: ["2025-03-outage", "slow-login", "disk-full", "cert-expiry", "queue-lag", "bad-deploy", "dns-flap", "oom-worker", "stale-cache", "retry-storm", "db-failover", "rate-limit", "cold-start", "clock-skew", "leaked-key", "noisy-alert", "lost-webhook", "pool-exhausted", "slow-query", "vpn-drop", "bad-migration", "quota-hit"],
  decisions: ["adopt-k8s", "monorepo", "trunk-based", "postgres", "feature-flags", "sso", "semver", "on-call", "sla", "tracing-stack", "queue-choice", "api-versioning", "retention", "cost-cap", "blue-green", "lint-gate", "docs-as-code", "secrets-vault", "rfc-process", "region-split", "log-format", "oncall-rota"],
  runbooks: ["deploy", "rollback", "rotate-secrets", "restore-backup", "scale-workers", "drain-node", "failover", "onboard", "oncall-handoff", "cert-renew", "reindex", "purge-cache", "hotfix", "release", "incident-comms", "access-review", "migrate-db", "pause-queue", "cut-over", "postmortem", "capacity", "offboard"],
};

export interface KbNode {
  id: string;
  community: number;
  weight: number;
}
export interface KbLink {
  source: string;
  target: string;
}

function mulberry32(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export function buildKb(): { nodes: KbNode[]; links: KbLink[]; degree: (id: string) => number } {
  const rnd = mulberry32(20261015);
  const nodes: KbNode[] = [];
  const byComm: string[][] = COMMUNITIES.map(() => []);
  COMMUNITIES.forEach((c, ci) => {
    for (const name of NAMES[c]) {
      const id = `${c}/${name}`;
      byComm[ci]!.push(id);
      nodes.push({ id, community: ci, weight: 0 });
    }
  });
  const seen = new Set<string>();
  const links: KbLink[] = [];
  const add = (a: string, b: string) => {
    const key = a < b ? `${a}|${b}` : `${b}|${a}`;
    if (a === b || seen.has(key)) return;
    seen.add(key);
    links.push({ source: a, target: b });
  };
  const pick = (xs: string[]) => xs[Math.floor(rnd() * xs.length)]!;
  byComm.forEach((ids, ci) => {
    ids.forEach((id, i) => {
      if (i > 0) add(id, ids[Math.floor(rnd() * Math.min(i, 5))]!); // a tree, hubs near the front
      if (i > 3 && rnd() < 0.5) add(id, pick(ids));
      if (rnd() < 0.22) add(id, pick(byComm[(ci + 1 + Math.floor(rnd() * 4)) % 5]!));
    });
  });
  // Links the story leans on.
  add("services/api", "infra/ci");
  add("runbooks/deploy", "infra/ci");
  add("runbooks/deploy", "services/api");

  const deg = new Map<string, number>();
  for (const l of links) {
    deg.set(l.source, (deg.get(l.source) ?? 0) + 1);
    deg.set(l.target, (deg.get(l.target) ?? 0) + 1);
  }
  const max = Math.max(...deg.values());
  for (const n of nodes) n.weight = (deg.get(n.id) ?? 0) / max;
  return { nodes, links, degree: (id) => deg.get(id) ?? 0 };
}
