// Forwards every request to the Cloud Run service (D751). Cloud Run routes by
// Host, so a proxied CNAME would reach it with the wrong one: the Worker
// rewrites only the host and passes method, path, query, headers and body
// through, and returns the response untouched, streaming included (MCP
// Streamable HTTP). UPSTREAM is a plain_text binding set by Terraform.
export default {
  async fetch(request, env) {
    const url = new URL(request.url);
    url.hostname = new URL(env.UPSTREAM).hostname;
    url.protocol = "https:";
    url.port = "";
    return fetch(new Request(url, request));
  },
};
