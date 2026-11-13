// The external app of the extension spec (D1938): a server on a loopback port
// of its own, so it is another origin than the Atlas that links to it.
//
// It answers every path with a small page and records the method, URL and
// headers of each request it receives. `GET /__requests` serves that record
// back as JSON and is not itself recorded, so a test can read what the browser
// sent when it followed the Atlas's link.
//
// Usage: node e2e/app-origin.mjs <port>

import { createServer } from "node:http";

const port = Number(process.argv[2]);
if (!Number.isInteger(port) || port <= 0) {
  console.error("usage: node app-origin.mjs <port>");
  process.exit(2);
}

/** @type {{ method: string | undefined, url: string | undefined, headers: Record<string, unknown> }[]} */
const seen = [];

const server = createServer((req, res) => {
  if (req.url === "/__requests") {
    res.writeHead(200, { "content-type": "application/json", "cache-control": "no-store" });
    res.end(JSON.stringify(seen));
    return;
  }
  // Node lower-cases header names: a test looks for "authorization" and "referer".
  seen.push({ method: req.method, url: req.url, headers: { ...req.headers } });
  res.writeHead(200, { "content-type": "text/html; charset=utf-8", "cache-control": "no-store" });
  res.end("<!doctype html><html><head><title>The external board</title></head><body><h1>The external board</h1></body></html>");
});

server.listen(port, "127.0.0.1", () => console.log(`app origin listening on 127.0.0.1:${port}`));
