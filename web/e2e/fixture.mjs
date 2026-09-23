// The browser suite's fixture KBs (D228).
//
// Output is a pure function of this file: no timestamps, no random ids, no
// environment. The determinism assertion compares layouts computed from
// scratch in two browser contexts, and it can only mean something if the node
// set under it is byte-for-byte the same on every run.
//
// Usage: node e2e/fixture.mjs <out-dir>  →  <out-dir>/{atlas,annex,void}
//
// atlas carries every shape the flows need; annex exists so KB switching has
// somewhere to go; void is a KB with no concepts at all.

import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

/** The narrowed principal sees Entity concepts under infra/ and nothing else. */
export const NARROW = { kb: "atlas", map: "infra", type: "Entity" };

/** A concept the narrowed principal must never learn exists: a Runbook, in
 *  the allowed map, so only the type selector keeps it out. */
export const HIDDEN_IN_MAP = "infra/firewall";
/** And one outside the allowed map altogether. */
export const HIDDEN_OUT_OF_MAP = "apps/proxy";

export const HOSTILE_BODY = [
  "# Proxy",
  "",
  "The reverse proxy in front of every service.",
  "",
  "<script>window.__cartographerXSS = 'script';</script>",
  "",
  '<img src="x" onerror="window.__cartographerXSS = \'onerror\'">',
  "",
  "Upstream: [[infra/gateway]].",
  "",
].join("\n");

const concept = (fm, body) => {
  const lines = Object.entries(fm).map(([k, v]) => `${k}: ${v}`);
  return `---\n${lines.join("\n")}\n---\n${body}`;
};

const map = (title, kind = "map") =>
  `---\ntype: Map\ntitle: ${title}\nkind: ${kind}\nontology_mode: flexible\n---\n# ${title}\n`;

const index = (title, lines = []) =>
  `---\ntype: Index\ntitle: ${title}\n---\n# ${title}\n${lines.length ? "\n" + lines.join("\n") + "\n" : ""}`;

const root = (title) => ({
  "data/index.md": index(title),
  "data/log.md": "# Log\n\n",
});

// What atlas ships to agent clients (D238): one artifact of each kind the
// local server lists. An MCP descriptor is left out on purpose: the local
// server has no MCP allowlist, so it would never be listed.
const artifacts = {
  "skills/review/SKILL.md":
    "---\nname: review\ndescription: Reviews a change before it lands\n---\n# Review\n\nRead the diff, then the tests.\n",
  "skills/review/checklist.txt": "1. tests\n2. docs\n",
  "agents/triage.md": "---\nname: triage\ndescription: Sorts incoming issues\n---\nYou sort issues.\n",
  "hooks/guard/hook.json": '{"event":"SessionStart"}\n',
  "hooks/guard/run.sh": "#!/bin/sh\nexit 0\n",
  "instructions.md": "# House rules\n\nWrite concepts in English.\n",
  "templates/runbook.md": "---\ntype: Runbook\ntitle: Runbook template\n---\n# {{title}}\n",
};

const atlas = {
  ...root("Atlas"),
  ...artifacts,

  "data/infra/_map.md": map("Infrastructure"),
  // A dead entry in a map index: a finding that is about no concept (#320),
  // so the Observatory can only explain that there is no node to reveal.
  "data/infra/index.md": index("Infrastructure", ["- [[infra/gateway]]", "- [[infra/decommissioned-router]]"]),
  "data/infra/log.md": "# Log\n\n",
  // The backlink pair: gateway ↔ dns.
  "data/infra/gateway.md": concept(
    { type: "Entity", title: "Gateway", status: "active" },
    "# Gateway\n\nThe edge router. Resolves through [[infra/dns]].\n",
  ),
  "data/infra/dns.md": concept(
    { type: "Entity", title: "DNS", status: "active" },
    "# DNS\n\nAuthoritative for the lab. Sits behind [[infra/gateway]].\n",
  ),
  // No inbound links anywhere: the orphan.
  "data/infra/legacy-vpn.md": concept(
    { type: "Entity", title: "Legacy VPN", status: "deprecated" },
    "# Legacy VPN\n\nKept for the record.\n",
  ),
  // A link to a target that does not exist: the broken target.
  "data/infra/firewall.md": concept(
    { type: "Runbook", title: "Firewall", status: "draft" },
    "# Firewall\n\nRules live in [[infra/missing-ruleset]]. Protects [[infra/gateway]].\n",
  ),
  // The expanded concept: body at <id>/index.md, one satellite.
  "data/infra/cluster/index.md": concept(
    { type: "Entity", title: "Cluster", status: "active" },
    "# Cluster\n\nThree nodes. See [[infra/cluster/nodes]] and [[infra/dns]].\n",
  ),
  "data/infra/cluster/nodes.md": concept(
    { type: "Entity", title: "Cluster nodes", status: "active" },
    "# Cluster nodes\n\nPart of [[infra/cluster]].\n",
  ),

  "data/apps/_map.md": map("Applications"),
  "data/apps/index.md": index("Applications", ["- [[apps/proxy]]"]),
  "data/apps/log.md": "# Log\n\n",
  "data/apps/proxy.md": concept({ type: "Runbook", title: "Proxy", status: "active" }, HOSTILE_BODY),
  "data/apps/backup.md": concept(
    { type: "Runbook", title: "Backup", status: "active" },
    "# Backup\n\nNightly, through [[apps/proxy]].\n",
  ),

  "data/incidents/_map.md": map("Incidents", "journal"),
  "data/incidents/index.md": index("Incidents", ["- [[incidents/2026-01-10-outage]]"]),
  "data/incidents/log.md": "# Log\n\n",
  "data/incidents/2026-01-10-outage.md": concept(
    { type: "Incident", title: "Gateway outage", status: "resolved" },
    "# Gateway outage\n\n[[infra/gateway]] dropped for an hour.\n",
  ),
};

const annex = {
  ...root("Annex"),
  "data/library/_map.md": map("Library"),
  "data/library/index.md": index("Library", ["- [[library/reading-list]]"]),
  "data/library/log.md": "# Log\n\n",
  "data/library/reading-list.md": concept(
    { type: "Topic", title: "Reading list", status: "active" },
    "# Reading list\n\nStart with [[library/field-guide]].\n",
  ),
  "data/library/field-guide.md": concept(
    { type: "Topic", title: "Field guide", status: "active" },
    "# Field guide\n\nBack to [[library/reading-list]].\n",
  ),
};

const empty = root("Void");

export function writeFixture(outDir) {
  for (const [name, files] of Object.entries({ atlas, annex, void: empty })) {
    for (const [rel, content] of Object.entries(files)) {
      const path = join(outDir, name, rel);
      mkdirSync(dirname(path), { recursive: true });
      writeFileSync(path, content);
    }
  }
}

if (process.argv[1] && import.meta.url === `file://${process.argv[1]}`) {
  const out = process.argv[2];
  if (!out) {
    console.error("usage: node e2e/fixture.mjs <out-dir>");
    process.exit(2);
  }
  writeFixture(out);
}
