---
topic: architecture
---

# D1867 — Meridian and Compass name the server and the client

**Decision.** The four components of the one binary (D1439) are Meridian (the server), Compass (the client), the Atlas and the Surveyor. Meridian replaces "the server" and Compass "the client" when the sentence names the component; "MCP server", third-party servers, agent clients and MCP/HTTP clients keep their plain words. This supersedes the nickname rule of D1442 ("`server` and `client` stay plain words, and no other nickname is introduced"), amends the "Server/Client" naming of D1439 and the `server` values key of D1441.

**Why.** Two named components and two plain ones read as an inconsistent family in docs, the Atlas, the CLI help and the chart. The first change applies the names to the Helm chart, which is unreleased: its values key `server` becomes `meridian`, the `app.kubernetes.io/component` label value becomes `meridian`, and the template and `ci/` files are renamed. The Kubernetes objects keep their release-derived names, because the Service name is the in-cluster URL injected into a Surveyor entry. The chart check that forbade the names is removed; `worker` stays a retired name.

**Alternatives rejected.** Keeping `server` in the chart: the chart is first published in 0.29, so renaming later would break users. Renaming the Kubernetes objects: they derive from the release name and the Service name is a contract. Command aliases or a deprecation hint: nothing released carried the names.

**Consequences.** Released commands (`serve`, `connect`, `sync`, `status`, `client`), flags, config keys (`server_url`, `server_name`, `web.enabled`), image names, routes, wire fields, the Settings tab ids and Go and TypeScript identifiers do not change. A values file with a `server:` key is rejected by the schema as an additional property. The second change applies the names to the CLI help; the third to the Atlas labels (the Settings tabs read Compass, Meridian and Surveyor), with the bundle rebuilt.
