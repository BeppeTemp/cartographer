# Cartographer Helm chart

Meridian, the Cartographer server, its embedded Atlas and Surveyors, one per perimeter: each is optional, but a
release holds at least one of Meridian and a Surveyor (`meridian.enabled: false` renders Surveyors only, against an
external Meridian at `meridian.url`; `atlas.enabled: false` turns the Atlas off). The chart is published with every release at the app's version:

```sh
helm install cartographer oci://ghcr.io/beppetemp/charts/cartographer --version <version> -f values.yaml
```

Installing from this directory defaults the image tag to the `v0.0.0-dev` placeholder, which does not
exist: set `meridian.image.tag` (and `surveyorImage.tag`). `values.yaml` is the reference for every key;
`helm show values` prints it. `helm template` prints the raw manifests for anyone who wants them.

## What it never does

- **One Meridian replica, always.** The per-KB lock is an in-process mutex and one process is the sole
  writer of a working copy. The Deployment has `replicas: 1` and the `Recreate` strategy, there is no
  replica, autoscaling or workload-kind value, and the values schema rejects such a key. Several Meridians
  are several releases.
- **No secret value, no `Secret` object.** Every secret is a reference to a Secret you create first.
- No Compass, no standalone Atlas, no ServiceAccount, RBAC, NetworkPolicy or PodDisruptionBudget.

## Secrets to create

```sh
# Token source (a): the whole CARTOGRAPHER_TOKENS string (a bare token is an admin).
kubectl create secret generic cartographer-tokens --from-literal=tokens="$(openssl rand -hex 32)"
# The credentials directory: audit.key is required (the mount is read-only, Meridian cannot generate it).
kubectl create secret generic cartographer-credentials \
  --from-file=default.ssh=./id_ed25519 --from-file=known_hosts=./known_hosts \
  --from-literal=audit.key="$(openssl rand -hex 32)"
```

Back `audit.key` up: the audit log's signatures depend on it. A change in a Secret does not restart the
pod; restart it after rotating a token (`kubectl rollout restart`).

## Scenarios

Each has a values file in `ci/`, rendered and checked by `make helm-check` in the repository.

| Scenario | Values |
|---|---|
| Kubernetes minimal: tokens, one HTTPS KB, the default claim | `ci/meridian-minimal-values.yaml` |
| Kubernetes full: credentials, existing claim, Ingress with TLS, probe token | `ci/meridian-full-values.yaml` |
| Partitioned: one release per Meridian, each with its KBs | `ci/partitioned-a-values.yaml`, `ci/partitioned-b-values.yaml` |
| YAML tokens (source b) and the role a Surveyor token needs | `ci/meridian-yaml-tokens-values.yaml` |
| Surveyor scheduled, as a CronJob | `ci/surveyor-scheduled-values.yaml` |
| Surveyor with an exec recipe, a derived image and a service account | `ci/surveyor-exec-values.yaml` |
| Surveyors only, against an external Meridian over HTTPS | `ci/surveyor-only-values.yaml` |
| Surveyors only, against a plain-HTTP Meridian (`allow_insecure_http` injected) | `ci/surveyor-only-http-values.yaml` |
| Meridian without the Atlas | `ci/meridian-no-atlas-values.yaml` |

## Meridian

**Config.** `meridian.config` is the Meridian YAML, rendered to the ConfigMap. The chart owns `http`, `data`
and `credentials_dir`: setting one fails at template time. Keep `auth.mode` a quoted string. The file is
read once, so a change restarts the pod. The mount is read-only, so Meridian never rewrites it; a
removed key is ignored with a log line, and `helm upgrade` is the realignment. `kb clone` and `kb rename`
run inside the pod cannot persist into the ConfigMap: declare KBs in `meridian.config.kbs`.

**Tokens**, exactly one source. (a) `meridian.tokens.existingSecret`: the key holds the whole
`CARTOGRAPHER_TOKENS` string, and `meridian.config.auth.tokens` must be empty, because the variable
replaces every YAML token. (b) `meridian.config.auth.tokens`, each `token` the whole-value reference
`${NAME}`, with `meridian.extraEnvFrom` naming the Secrets that hold `NAME`. A literal token is refused: it
would be rendered into a ConfigMap.

**Readiness.** The default is an `exec` that reads the body of `/health` (`"ready":true`), because
`/ready` needs a token when auth is on. To use `/ready`, set `meridian.probes.readiness.tokenSecret` to a
Secret key holding one of Meridian's own tokens with a read scope on one KB. Liveness and startup are
`httpGet /health`; the startup allowance for the cold clone is five minutes. `terminationGracePeriodSeconds`
is 60: the shutdown takes up to 10 s of HTTP drain plus 10 s per KB, so raise it with the number of KBs.

**Persistence.** By default a claim `<fullname>-data` that survives `helm uninstall`. Installing the
release again fails in Helm on ownership: set `meridian.persistence.existingClaim: <fullname>-data`.
`meridian.persistence.emptyDir: true` is an explicit opt-in that loses the audit log, the conflict
registries, `usage.json` and every clone at each restart. `meridian.credentials.enabled: false` mounts no
credentials Secret and Meridian keeps `audit.key` on the data volume.

## Surveyors

`surveyors` is a list, one entry per perimeter: `name`, `mode` (`deployment` or `cronjob` with a
`schedule`, run with `--once`), `config` (the Surveyor YAML, no secret in it), `existingSecret` (every
key of that Secret becomes an environment variable) and the optional keys documented in `values.yaml`.
`files` adds text files next to the config, which `instructions_file` names by plain name. An entry has no
port, probe or persistent volume: its ConfigMap and one `emptyDir` on `/tmp`.

- Without a `server_url` the chart injects the Meridian URL and, for an `http` one, `allow_insecure_http: true`,
  so the token crosses the network in clear (the install notes say so): the in-cluster Service URL when
  `meridian.enabled` is true, `meridian.url` when it is false (a Surveyors-only release has no Service to
  point at). To avoid it, set `server_url` to the Ingress's HTTPS URL, or `meridian.url` to an HTTPS one.
- `image` overrides the Surveyor image with a derived one (an exec recipe's tools); it must keep the
  entrypoint `cartographer surveyor`, because the chart passes only arguments.
- `serviceAccountName` also mounts the API token on that entry's pod, for a system source. The account
  and its read-only role are yours.
- A CronJob tick outside the config's window or idle trigger runs nothing and exits 0: the schedule only
  decides when the process starts.

## Upgrade and uninstall

The image tag is the chart's `appVersion`, so an upgrade is a new chart version:
`helm upgrade <release> oci://ghcr.io/beppetemp/charts/cartographer --version <version>`. Under a GitOps
controller the version is merged and applied later: read the Deployment's image back before trusting
`kubectl rollout status`. `helm uninstall` keeps the data claim.
