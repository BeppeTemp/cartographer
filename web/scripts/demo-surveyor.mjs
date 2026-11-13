#!/usr/bin/env node
// Prepares the "Surveyor at work" film (web/e2e/surveyor.spec.ts) beside a
// running server over a demo KB: the KB side of a feed campaign, a Surveyor
// config, a scripted model and two exec recipes. Placeholders only: `kb-a`
// style names, example.com hosts (D259).
//
//   Feed "Field notes"   -> a recipe (exec kind, no model) lists two notes with
//                           their text inline; the scripted model shapes a unit
//                           for each and then writes the page it describes.
//   Feed "Observatory log" -> a recipe that fails: the feed ends in error, which
//                           Work and Settings show.
//
// Usage: demo-surveyor.mjs <tmp-dir> <server-url> [<kb> <map> <link>]   (record-hero.sh's HERO_PREPARE)
//   <map>   an existing map the new pages go into; <link> an existing, live concept in it
//   (defaults: the demo KB of demo-kb.mjs)
// It prints KEY=VALUE lines the caller exports (SURVEYOR_CONFIG, SURVEYOR_STUB,
// SURVEYOR_NOTES) and PID=<n> for each background process it started (the
// scripted model), which the caller stops. The same file is the recipes:
//   node demo-surveyor.mjs recipe notes|log   (protocol v2, docs/surveyor-recipes.md)

import { spawn } from "node:child_process";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const SELF = fileURLToPath(import.meta.url);

/** The two notes the feed lists: id (slug of the page), title and text. */
const NOTES = [
  { slug: "transit-log", title: "Transit of the inner planet", text: "The inner planet crossed the disc of the star at 21:40, a transit of 3 hours. Contact times were logged from the north dome." },
  { slug: "parallax-station", title: "Parallax at the northern station", text: "Two stations 40 km apart measured the same star against the background; the shift gives its distance." },
];

if (process.argv[2] === "recipe") {
  recipe(process.argv[3]);
} else {
  await prepare(process.argv.slice(2));
}

function recipe(which) {
  const req = JSON.parse(readFileSync(0, "utf8"));
  if (req.protocol !== 2) process.exit(64);
  const out = (o) => console.log(JSON.stringify(o));
  if (req.verb === "spec") {
    out({ spec: { title: which === "notes" ? "Field notes" : "Observatory log", description: "A fixture feed.", shapes: ["feed"], verbs: ["spec", "list"] } });
  } else if (req.verb === "list") {
    if (which === "log") {
      console.error("observatory log: the service answered 503 Service Unavailable (log.example.com)");
      process.exit(1);
    }
    for (const n of NOTES) out({ entry: { key: n.slug, change: "added", title: n.title, revision: "r1", content: n.text } });
    out({ next_cursor: "r1" });
  } else {
    process.exit(64);
  }
}

async function mcp(url, kb, name, args) {
  const res = await fetch(`${url}/mcp?kb=${kb}`, {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/call", params: { name, arguments: args } }),
  });
  const body = await res.json();
  if (body.error || body.result?.isError) throw new Error(`demo-surveyor: ${name} failed: ${JSON.stringify(body.error ?? body.result)}`);
  return JSON.parse(body.result.content[0].text);
}

function freePort() {
  return new Promise((ok) => {
    const s = createServer();
    s.listen(0, "127.0.0.1", () => {
      const { port } = s.address();
      s.close(() => ok(port));
    });
  });
}

async function prepare([tmp, url, kb = "demo", map = "astronomy", link = "astronomy/comet-3"]) {
  if (!url) {
    console.error("usage: demo-surveyor.mjs <tmp-dir> <server-url> [<kb> <map> <link>]");
    process.exit(2);
  }
  const dir = join(resolve(tmp), "surveyor");
  mkdirSync(join(dir, "recipes"), { recursive: true });

  // The KB side: a Source per feed, the plan that lists them, and the journals they live in.
  await mcp(url, kb, "map_create", { name: "sources", title: "Sources", kind: "journal" });
  // The ingest journal names the fields the write gate would otherwise rename to
  // their standard synonym (sources and source are synonyms of provenance).
  await mcp(url, kb, "map_create", {
    name: "ingest",
    title: "Ingest",
    kind: "journal",
    required_fields: { IngestPlan: ["status", "sources"], IngestUnit: ["status", "source"] },
  });
  // Units are created by the poll, which asks for no index entry: a generated index lists them.
  await mcp(url, kb, "map_update", { map: "ingest", index: "generated" });
  const feeds = [
    ["Field notes", "https://notes.example.com/field", "field-notes"],
    ["Observatory log", "https://log.example.com/observatory", "observatory-log"],
  ];
  const ids = [];
  for (const [title, locator, rec] of feeds) {
    const r = await mcp(url, kb, "source_register", { title, source_kind: "documentation", locator, source_shape: "feed", recipe: rec, timestamp: "2026-01-01" });
    ids.push(r.id);
  }
  await mcp(url, kb, "concept_write", {
    id: "ingest/field-notes",
    frontmatter: { type: "IngestPlan", title: "Field notes", status: "open", sources: ids, maps: [map] },
    body: `Plan: one page per note, in ${map}. Link each page to [[${link}]].`,
  });

  // The recipes: this file again, as the executable of the exec kind.
  for (const [name, which] of [["field-notes", "notes"], ["observatory-log", "log"]]) {
    writeFileSync(join(dir, "recipes", `${name}.md`), `Entries are notes of an observatory. One unit per note; the page goes in ${map}.\n`);
  }
  const stubPort = await freePort();
  const script = {
    turns: [
      ...NOTES.map((n) => ({
        when: n.title,
        text: JSON.stringify({ targets: [`${map}/${n.slug}`], extract: `The ${n.title.toLowerCase()} and what it shows`, verify: "The page states the observation and links to a related concept", executor: "any", body: `Write ${map}/${n.slug} from the note's text and link it to [[${link}]].` }),
      })),
      ...NOTES.flatMap((n) => [
        {
          when: n.title,
          text: `Writing the page for ${n.title}.`,
          tool_calls: [{ name: "concept_write", input: { id: `${map}/${n.slug}`, frontmatter: { type: "Note", title: n.title }, body: `# ${n.title}\n\n${n.text}\n\nSee [[${link}]].\n`, index_entry: true } }],
        },
        { when: n.title, text: `${n.title} is written and linked.` },
      ]),
    ],
  };
  writeFileSync(join(dir, "model-script.json"), JSON.stringify(script, null, 2));
  const recipeYaml = (name, which, line) =>
    `  ${name}:\n    instructions_file: recipes/${name}.md\n    exec:\n      command: node\n      args: ["${SELF}", "recipe", "${which}"]\n`;
  writeFileSync(
    join(dir, "surveyor.yaml"),
    `name: field-notes
server_url: ${url}/mcp
token_env: SURVEYOR_TOKEN
kbs: [${kb}]
models:
  - name: scripted
    provider: anthropic
    base_url: http://127.0.0.1:${stubPort}
    model: scripted-model
    api_key_env: STUB_API_KEY
    tier: large
    budget: {daily_tokens: 1000000, run_tokens: 500000}
schedule:
  window: {start: "00:00", end: "23:59", timezone: UTC}
tasks:
  - kind: poll
    model: scripted
  - kind: ingest
    model: scripted
recipes:
${recipeYaml("field-notes", "notes")}${recipeYaml("observatory-log", "log")}`,
  );

  // The scripted model runs on until the caller stops it.
  const stub = spawn(process.execPath, [join(SELF, "..", "stub-model.mjs"), join(dir, "model-script.json"), String(stubPort)], {
    stdio: ["ignore", "ignore", "inherit"],
    detached: true,
  });
  stub.unref();
  for (let i = 0; i < 40; i++) {
    try {
      await fetch(`http://127.0.0.1:${stubPort}/state`);
      break;
    } catch {
      await new Promise((r) => setTimeout(r, 100));
    }
  }
  console.log(`PID=${stub.pid}`);
  console.log(`SURVEYOR_CONFIG=${join(dir, "surveyor.yaml")}`);
  console.log(`SURVEYOR_STUB=http://127.0.0.1:${stubPort}`);
  console.log(`SURVEYOR_NOTES=${NOTES.map((n) => `${map}/${n.slug}`).join(",")}`);
}
