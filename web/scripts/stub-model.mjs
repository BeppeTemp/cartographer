// A scripted model for the Surveyor film: a tiny HTTP server that speaks the
// Anthropic Messages API (POST /v1/messages, non-streaming) and answers with the
// turns of a committed script, in order: a turn with a `when` is taken by the
// first request that holds its text, so two units the Surveyor takes in either
// order (work_list falls back to the id on equal timestamps) each find their
// own turns. It is the Node twin of internal/surveyor/surveyortest.Fake: a
// request past the end of the script, or one that no unserved turn matches,
// fails loudly (HTTP 400 and a line on stderr) instead of improvising, so a
// Surveyor that changed its prompts breaks the film's run and not just its look.
//
// The wire shape is the one internal/surveyor/anthropic.go builds and parses
// (anthropic_test.go pins it): the request carries `messages`, the answer
// `content` blocks, `stop_reason` and `usage`.
//
// Script (JSON): {"turns": [{"when": "<text the request must hold>",
//   "text": "<assistant text>", "tool_calls": [{"name": "...", "input": {...}}]}]}
// A turn with tool_calls stops with `tool_use`; one without ends the turn.
//
// Usage: node stub-model.mjs <script.json> <port>   (prints "listening <port>")
//        GET /state answers {"served": n, "total": n, "failed": n}

import { readFileSync } from "node:fs";
import { createServer } from "node:http";

const [scriptPath, portArg] = process.argv.slice(2);
if (!scriptPath || !portArg) {
  console.error("usage: node stub-model.mjs <script.json> <port>");
  process.exit(2);
}
const { turns } = JSON.parse(readFileSync(scriptPath, "utf8"));
if (!Array.isArray(turns) || turns.length === 0) {
  console.error("stub-model: the script has no turns");
  process.exit(2);
}

const used = new Set();
let served = 0;
let failed = 0;

const fail = (res, message) => {
  failed++;
  console.error(`stub-model: ${message}`);
  res.writeHead(400, { "content-type": "application/json" });
  res.end(JSON.stringify({ type: "error", error: { type: "invalid_request_error", message } }));
};

const server = createServer((req, res) => {
  if (req.method === "GET" && req.url === "/state") {
    res.writeHead(200, { "content-type": "application/json" });
    res.end(JSON.stringify({ served, total: turns.length, failed }));
    return;
  }
  if (req.method !== "POST" || req.url !== "/v1/messages") return fail(res, `unexpected ${req.method} ${req.url}`);
  const chunks = [];
  req.on("data", (c) => chunks.push(c));
  req.on("end", () => {
    const body = Buffer.concat(chunks).toString("utf8");
    if (process.env.STUB_LOG) console.error(`stub-model: request ${served + 1}: ...${JSON.stringify(JSON.parse(body).messages.at(-1)).slice(-500)}`);
    // The request is JSON: a needle with a quote or a newline is escaped the same way here.
    const holds = (t) => !t.when || body.includes(JSON.stringify(t.when).slice(1, -1));
    // The first unserved turn the request holds: a turn with a `when` waits for its
    // own request, so units that the Surveyor takes in an order the clock decides
    // (equal timestamps fall back to the id) each find their turns, in order.
    const idx = turns.findIndex((t, i) => !used.has(i) && holds(t));
    if (idx < 0) {
      if (used.size === turns.length) return fail(res, `request ${served + 1}: the script has ${turns.length} turns and no turn left`);
      const next = turns.find((_, i) => !used.has(i));
      return fail(res, `request ${served + 1}: no unserved turn matches; the first unserved expects the text ${JSON.stringify(next.when)}, which the request does not hold`);
    }
    const turn = turns[idx];
    used.add(idx);
    served++;
    const content = [];
    if (turn.text) content.push({ type: "text", text: turn.text });
    (turn.tool_calls ?? []).forEach((c, i) => content.push({ type: "tool_use", id: `toolu_stub_${served}_${i}`, name: c.name, input: c.input ?? {} }));
    res.writeHead(200, { "content-type": "application/json" });
    res.end(
      JSON.stringify({
        id: `msg_stub_${served}`,
        type: "message",
        role: "assistant",
        content,
        stop_reason: turn.tool_calls?.length ? "tool_use" : "end_turn",
        usage: { input_tokens: turn.input_tokens ?? 900, output_tokens: turn.output_tokens ?? 160 },
      }),
    );
  });
});

server.listen(Number(portArg), "127.0.0.1", () => console.log(`listening ${portArg}`));
for (const sig of ["SIGINT", "SIGTERM"]) process.on(sig, () => server.close(() => process.exit(0)));
