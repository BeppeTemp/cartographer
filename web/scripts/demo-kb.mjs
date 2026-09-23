// A publishable demo Knowledge Base for Atlas screenshots and the 3D benchmark.
//
// Output is a pure function of the arguments (seeded PRNG, no clock), with
// invented, neutral content: nothing in it names a real system, host or
// person, so screenshots taken over it can go in the README (D228, D234).
//
// Shape: Maps of clustered concepts, a few high-degree hubs, links that cross
// Maps, orphans, and one small component with no link to the rest — every
// shape the graph has to draw well.
//
// Usage: node scripts/demo-kb.mjs <out-dir> [concepts=400] [seed=7]
//        → <out-dir>/demo, ready for `cartographer serve --kb <out-dir>/demo`

import { mkdirSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

const [out, countArg = "400", seedArg = "7"] = process.argv.slice(2);
if (!out) {
  console.error("usage: node scripts/demo-kb.mjs <out-dir> [concepts] [seed]");
  process.exit(2);
}
const COUNT = Math.max(20, Number(countArg));
let state = Number(seedArg) >>> 0;
const rand = () => {
  state = (Math.imul(state, 1664525) + 1013904223) >>> 0;
  return state / 2 ** 32;
};
const pick = (list) => list[Math.floor(rand() * list.length)];

const MAPS = [
  ["cartography", "Cartography", ["projection", "meridian", "survey", "contour", "scale", "legend", "datum", "bearing"]],
  ["botany", "Botany", ["fern", "lichen", "moss", "pine", "oak", "sedge", "root", "seed"]],
  ["astronomy", "Astronomy", ["orbit", "comet", "nebula", "parallax", "zenith", "eclipse", "quasar", "transit"]],
  ["music", "Music", ["cadence", "fugue", "motif", "timbre", "tempo", "chord", "canon", "rondo"]],
  ["geology", "Geology", ["strata", "basalt", "fault", "glacier", "delta", "moraine", "quartz", "fold"]],
  ["language", "Language", ["syntax", "idiom", "phoneme", "lexicon", "dialect", "grammar", "metre", "glyph"]],
  ["weather", "Weather", ["front", "monsoon", "cirrus", "gust", "drizzle", "isobar", "squall", "haze"]],
  ["craft", "Craft", ["loom", "kiln", "chisel", "lathe", "glaze", "weave", "joinery", "forge"]],
];
const TYPES = ["Topic", "Topic", "Entity", "Entity", "Note", "Runbook"];
const STATUS = ["active", "active", "active", "draft", "deprecated"];

const files = {};
const put = (path, text) => (files[path] = text);
const fm = (fields) =>
  `---\n${Object.entries(fields)
    .map(([k, v]) => `${k}: ${v}`)
    .join("\n")}\n---\n`;

put("data/index.md", `${fm({ type: "Index", title: "Demo atlas" })}# Demo atlas\n`);
put("data/log.md", "# Log\n\n");

// Concepts, spread over the Maps; the island is kept apart at the end.
const ISLAND = 6;
const concepts = [];
for (let i = 0; i < COUNT - ISLAND; i++) {
  const [slug, , words] = MAPS[i % MAPS.length];
  const word = words[Math.floor(i / MAPS.length) % words.length];
  const n = Math.floor(i / (MAPS.length * words.length));
  concepts.push({ map: slug, id: `${slug}/${word}${n ? `-${n + 1}` : ""}`, word });
}
for (let i = 0; i < ISLAND; i++) concepts.push({ map: "craft", id: `craft/island-${i + 1}`, word: `island ${i + 1}` });

const byMap = new Map(MAPS.map(([slug]) => [slug, []]));
for (const c of concepts.slice(0, COUNT - ISLAND)) byMap.get(c.map).push(c);

// Hubs: the first concept of each Map, linked from many of its own.
const hubs = MAPS.map(([slug]) => byMap.get(slug)[0]);
const links = new Map(concepts.map((c) => [c.id, new Set()]));
for (const c of concepts.slice(0, COUNT - ISLAND)) {
  if (rand() < 0.04) continue; // an orphan: no outbound link
  const own = byMap.get(c.map);
  const hub = hubs[MAPS.findIndex(([s]) => s === c.map)];
  if (hub !== c && rand() < 0.55) links.get(c.id).add(hub.id);
  for (let k = 0; k < 1 + Math.floor(rand() * 3); k++) {
    const other = pick(own);
    if (other !== c) links.get(c.id).add(other.id);
  }
  if (rand() < 0.12) links.get(c.id).add(pick(hubs).id); // a bridge between Maps
}
// The island: a ring with a spoke, linked to nothing outside it.
const island = concepts.slice(COUNT - ISLAND);
island.forEach((c, i) => links.get(c.id).add(island[(i + 1) % ISLAND].id));
links.get(island[0].id).add(island[3].id);

for (const [slug, title] of MAPS) {
  put(`data/${slug}/_map.md`, `${fm({ type: "Map", title, kind: "map", ontology_mode: "flexible" })}# ${title}\n`);
  put(`data/${slug}/index.md`, `${fm({ type: "Index", title })}# ${title}\n`);
  put(`data/${slug}/log.md`, "# Log\n\n");
}
for (const c of concepts) {
  const title = c.word.replace(/^\w/, (ch) => ch.toUpperCase());
  const out = [...links.get(c.id)];
  const body = [
    `# ${title}`,
    "",
    `${title} is a demo concept in the ${c.map} map, written for screenshots and benchmarks.`,
    "",
    out.length ? `Related: ${out.map((id) => `[[${id}]]`).join(", ")}.` : "It links to nothing yet.",
    "",
  ].join("\n");
  put(`data/${c.id}.md`, fm({ type: pick(TYPES), title, status: pick(STATUS) }) + body);
}

const root = join(out, "demo");
rmSync(root, { recursive: true, force: true });
for (const [path, text] of Object.entries(files)) {
  const full = join(root, path);
  mkdirSync(dirname(full), { recursive: true });
  writeFileSync(full, text);
}
console.log(`demo-kb: ${concepts.length} concepts in ${MAPS.length} maps -> ${root}`);
