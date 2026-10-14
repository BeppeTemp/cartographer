// Forwards every request to the Cloud Run service (D751). Cloud Run routes by
// Host, so a proxied CNAME would reach it with the wrong one: the Worker
// rewrites only the host and passes method, path, query and body through, and
// returns the response untouched, streaming included (MCP Streamable HTTP).
//
// The hostname is authless (D789): the Worker sets the read-only token on every
// request, replacing whatever the client sent, so a client that cannot send a
// header (a claude.ai or ChatGPT connector, a phone) connects with the URL
// alone. /.well-known/ answers 404: the server's RFC 9728 metadata names the
// *.run.app URL as authorization server, where none runs, and a client that
// finds it starts an OAuth flow that cannot finish.
//
// UPSTREAM (plain_text) and DEMO_TOKEN (secret_text) are bindings set by Terraform.
export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    if (url.pathname.startsWith("/.well-known/")) {
      return new Response("Not Found", { status: 404 });
    }
    url.hostname = new URL(env.UPSTREAM).hostname;
    url.protocol = "https:";
    url.port = "";
    const upstream = new Request(url, request);
    upstream.headers.set("Authorization", `Bearer ${env.DEMO_TOKEN}`);
    return fetch(upstream);
  },
};
