# Decision records

This directory records the *why* behind non-obvious choices. It is historical
context, not the product contract or a backlog:

- current behavior lives in the topic pages linked from [the documentation index](index.md);
- planned work, bugs and delivery status live in GitHub issues;
- completed user-visible work is summarized by releases and `CHANGELOG.md`.

## Topics

| Topic | Decision records | Current-state documentation |
|---|---|---|
| Cross-cutting architecture | [Architecture](decisions/architecture.md) | [Overview](overview.md) |
| KB model and imports | [Data plane](decisions/data-plane.md) | [Data plane](data-plane.md) |
| MCP tools, search and lint | [Control plane](decisions/control-plane.md) | [Control plane](control-plane.md) |
| HTTP, stdio and authorization | [Transport and authorization](decisions/transport-auth.md) | [Transport and authorization](transport-auth.md) |
| Commits, synchronization and conflicts | [Concurrency and git](decisions/concurrency-git.md) | [Concurrency](concurrency.md) |
| Skills, services and secrets | [Skills, services and secrets](decisions/skills-services-secrets.md) | [Skills, services and secrets](skills-services-secrets.md) |
| Provisioned artifacts | [Synchronization and provisioning](decisions/sync-provisioning.md) | [Synchronization](sync.md) |
| CLI, TUI and providers | [Client and configurator](decisions/client-configurator.md) | [Configurator](configurator.md) |
| Configuration, deployment and releases | [Deployment and release](decisions/deployment-release.md) | [Deployment](deployment.md) |
| Repository and documentation policy | [Project governance](decisions/project-governance.md) | [Contributing](https://github.com/BeppeTemp/cartographer/blob/main/CONTRIBUTING.md) |

## Finding a decision

Search the directory instead of maintaining a second title/status index:

```bash
rg '^## (AD|D)[0-9]+|keyword' docs/decisions/
```

Every implementation decision has one canonical topic file and a stable
`<a id="dNN"></a>` anchor. Link directly to
`docs/decisions/<topic>.md#dNN`.

## Adding a decision

1. Survey open `plan` issues before choosing an ID. A plan title reserves its
   D number until it is implemented or explicitly abandoned.
2. Choose the next number above both the decision files and existing plan issue
   titles. Never infer it from a single topic file.
3. The plan issue is the design handoff. Add the final D entry only in the
   implementation PR, in the one topic file that owns the choice.
4. Record the decision, rationale and consequences. Put current behavior in the
   corresponding topic page and future work in a GitHub issue.
5. Do not add status tables, milestone lists or duplicate entries. Cross-topic
   decisions have one owner and are linked from other pages when useful.
