---
topic: deployment-release
---

# D1441 — The Helm chart ships with the app

**Decision.** The Kubernetes artifact is a Helm chart in this repository, `deploy/helm/cartographer/`, at the **same version as the app**: `Chart.yaml` holds the placeholders `0.0.0-dev` and `v0.0.0-dev`, and the release and candidate workflows stamp them at package time from the tag (`helm package --version <semver> --app-version <tag>`, the pattern `server.json` already uses), so no commit and no release-please change is involved. It is published as an OCI artifact at `ghcr.io/beppetemp/charts/cartographer:<semver>`, by `release.yml` (attested, like the images, D1022) and by `preview.yml` (unattested, like the candidate images), after the images it points to. Its invariants are built in, not documented: the server is **one replica by construction** (a literal `replicas: 1`, `strategy: Recreate`, no `replicaCount` or autoscaling key, and a values schema with `additionalProperties: false` that rejects them, so a second server is a second release); **secrets only by reference** (the chart renders no `Secret` object and no secret value: tokens come from one existing Secret as `CARTOGRAPHER_TOKENS` or from `${NAME}` YAML tokens fed by `envFrom`, the credentials directory is one existing Secret mounted read-only with `defaultMode: 0440` next to an `fsGroup`); the data volume is a kept claim, an existing claim or an explicit `emptyDir` opt-in. Readiness is an `exec` that reads the body of `/health`, not an `httpGet` on `/ready`, which answers 401 when auth is on; the token route stays an opt-in. Surveyors are `surveyors` entries, each with its own ConfigMap and a Deployment or a CronJob, an `emptyDir` on `/tmp` and no port, probe or persistent volume. The check, `make helm-check` (`test/helm/run.sh`), is outside `make gate` and runs in a path-scoped `helm` job and before each chart push.

**Why.** The two raw excerpts of `docs/deployment.md` were copied and edited by every operator, and each copy could silently drop what they carry: one replica, a readable credentials Secret for a non-root image, an `audit.key` in a read-only mount. A chart makes those impossible to break by a values file, and a chart whose version is the app's has no skew to explain. Costs: a Helm pin to maintain (`scripts/install-helm.sh`), a new package to publish, and a chart that refuses some values a generic chart would take.

**Alternatives rejected.**
- A separate chart repository and release train: two versions to keep in step and a chart that can name an image that does not exist.
- A chart version decoupled from the app: the same skew, plus an `appVersion` table to read.
- release-please editing `Chart.yaml`: it has no `extra-files` today, and a stamped placeholder needs no commit.
- A `replicaCount` value, a StatefulSet or an HPA: the per-KB lock is an in-process mutex and one process is the sole writer of a working copy (D73); a rolling update would run two writers on one volume.
- Rendering a `Secret` from values: a token in a values file ends up in the release history and in Git.
- Readiness by `httpGet /ready`: the chart listens off loopback, where auth is on and `/ready` is not a public path. The `/health` body carries `ready` from the same `readiness()`.
- Attesting the candidate chart: previews are not releases and nothing verifies them (D1022).
- Helm in `make gate`: the gate stays Go-only and offline (D227).

**Consequences.** Both workflows run `make helm-check` before the push, and `internal/repodocs` pins the order, the stamping, the attestation difference and the placeholders. `wget` is a promised tool of the server image (`test/smoke/image.sh`), because the probe needs it. The chart owns `http`, `data` and `credentials_dir` of the server config. A first publish creates the GHCR package `charts/cartographer`: a maintainer sets it Public, checks that Actions has Write access, and at the first stable release verifies `gh attestation verify oci://…` against a Helm artifact (the docs line is corrected if `gh` cannot resolve it). The `helm` check joins the required set when a maintainer adds it to the ruleset; until then `main-status` watches it.
