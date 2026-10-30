# Kubernetes system lister

The reference recipe of `shape: system` ([worker-recipes.md](../../../docs/worker-recipes.md) §System recipes): a
Python 3 script (standard library only) that prints one entry per namespace of a cluster, through `kubectl`. It is an
example in the repository, not part of the binary.

Files: `lister.py` (the recipe's command), `instructions.md` (its instruction text), `recipe.yaml` (the
`recipes.<name>` block), `testdata/view/` (synthetic fixtures: namespaces `team-a` and `team-b`).

## What it does

- **Preflight.** It asks `kubectl auth can-i` and answers `read_only: true` only if the identity cannot do `*` on `*`,
  cannot `get secrets`, cannot `create`, `update`, `patch` or `delete` pods, deployments or namespaces, and **can**
  `list namespaces`. The `identity` is the context's user name; `detail` names the first failed question.
- **List.** One entry per namespace, `key` = `namespace/<name>`. Per namespace one line per object, sorted:
  Deployments, StatefulSets, DaemonSets, CronJobs (replicas or schedule, containers `name:image`, ports, labels),
  Services, Ingresses, PersistentVolumeClaims and ConfigMaps (**names only**). Cluster-scoped objects are left out,
  because the `view` role does not grant them.
- **Never emitted**: Secrets (never requested), ConfigMap data, environment values, annotations, `managedFields`, `uid`,
  `resourceVersion`, `generation`, creation timestamps, any `status` field. A namespace over the request's
  `max_content_bytes` fails by name, never a silent cut. `include` is a list of globs on the key.
- It **never uses the request's `source.locator`** to choose a context, namespace, file or command; the identity and
  the endpoint come from the environment below.

## Try it without a cluster

```sh
echo '{"mode":"preflight","source":{}}' | LISTER_FIXTURES_DIR=testdata/view python3 lister.py
echo '{"mode":"list","source":{"include":[]},"max_content_bytes":65536}' | LISTER_FIXTURES_DIR=testdata/view python3 lister.py
```

With `LISTER_FIXTURES_DIR` set, `kubectl` is not run: the answer to each command is the file named after its
argument vector (`*` written as `all`), for example `get_namespaces_-o_json.json` or `auth_can-i_get_secrets.txt`. A
change of the command line changes the file asked for, so the tests pin the commands. `recipe.yaml` never lists that
variable.

## Try it on a cluster

1. Give the lister a **read-only identity**: a dedicated ServiceAccount bound to the `view` ClusterRole (it does not
   include Secrets) and a kubeconfig that uses it. The preflight is a guard against a mistake here, not a security
   boundary: the script is yours.
2. Put `lister.py` where the worker can run it (a derived image with Python 3 and `kubectl`, or a mounted read-only
   volume) and add `recipe.yaml` to the worker config, with `instructions.md` in the config directory. Set `KUBECTL`,
   `KUBE_CONTEXT` and `KUBECONFIG` in the worker's environment.
3. Register the system once: `source_register` with `source_shape: system`, `recipe: k8s-dev` and
   `locator: kubernetes://dev-cluster` (a label, nothing selects a context from it), list the Source in an
   IngestPlan's `sources`, and set `feed_interval` with `concept_patch`.

The first poll creates one unit per namespace; later polls create units only for namespaces whose objects changed.
