#!/usr/bin/env bash
# The Helm chart's checks (make helm-check, job `helm` of ci.yml, and the release workflows
# before a push): helm lint --strict and helm template over every values file of
# deploy/helm/cartographer/ci/, then assertions on what is rendered. Needs helm, jq and
# coreutils, no cluster. The invariants it pins are safety properties of the deploy
# artifact (D1441): one server replica, no Secret object, no literal secret in an
# environment, the credentials Secret mounted 0440 with an fsGroup, the quoted auth mode.
set -euo pipefail

cd "$(dirname "$0")/../.."
chart=deploy/helm/cartographer
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fail() { echo "helm-check: $*" >&2; exit 1; }
command -v helm >/dev/null || fail "helm is not on PATH (scripts/install-helm.sh <dir> installs the pinned release)"
command -v jq >/dev/null || fail "jq is not on PATH"

# render <label> <release> <helm args...>: the output is split into one file per document
# under $work/<label>/, named by number; kind_files and has read them back.
render() {
  local label=$1 rel=$2
  shift 2
  mkdir -p "$work/$label"
  helm template "$rel" "$chart" "$@" >"$work/$label.yaml" || fail "helm template failed for $label"
  awk -v d="$work/$label" '/^---$/ { n++; next } n { print > (d "/doc-" n ".yaml") }' "$work/$label.yaml"
}
# docs <label> <kind>: the files of that kind.
docs() { grep -lx "kind: $2" "$work/$1"/doc-*.yaml 2>/dev/null || true; }
# one <label> <kind> [<component>]: the single document of that kind (and component).
one() {
  local f found=()
  for f in $(docs "$1" "$2"); do
    if [ -z "${3:-}" ] || grep -qx "    app.kubernetes.io/component: $3" "$f"; then found+=("$f"); fi
  done
  [ "${#found[@]}" = 1 ] || fail "$1: want exactly one $2${3:+ of component $3}, found ${#found[@]}"
  echo "${found[0]}"
}
# must <label> <file> <ERE> <what>, mustnt: grep -E on one rendered file.
must() { grep -Eq -- "$3" "$2" || fail "$1: $4"; }
mustnt() { ! grep -Eq -- "$3" "$2" || fail "$1: $4"; }

# neg <substring> <yaml> [nobase]: helm template must exit non-zero with the substring.
# The base values give a valid token source, so each case breaks exactly one thing.
neg() {
  local want=$1 yaml=$2 out args=()
  printf '%s\n' "$yaml" >"$work/neg-case.yaml"
  if [ "${3:-}" != nobase ]; then
    printf 'server: {tokens: {existingSecret: {name: t}}, credentials: {enabled: false}}\n' >"$work/neg-base.yaml"
    args=(-f "$work/neg-base.yaml")
  fi
  if out=$(helm template ci "$chart" "${args[@]}" -f "$work/neg-case.yaml" 2>&1); then
    fail "negative case accepted, want '$want': $yaml"
  fi
  case $out in *"$want"*) ;; *) fail "negative case '$yaml' failed without '$want': $out" ;; esac
}

echo "→ the chart's files"
bad=$(grep -rliE 'worker|meridian|compass' "$chart" || true)
[ -z "$bad" ] || fail "retired names in: $bad"
# The release workflows stamp both with helm package; a hand edit would ship the wrong tag.
grep -qx 'version: 0.0.0-dev' "$chart/Chart.yaml" || fail "Chart.yaml version is not the 0.0.0-dev placeholder"
grep -qx 'appVersion: "v0.0.0-dev"' "$chart/Chart.yaml" || fail "Chart.yaml appVersion is not the v0.0.0-dev placeholder"
schema=$chart/values.schema.json
jq -e '.additionalProperties == false and .properties.server.additionalProperties == false' "$schema" >/dev/null \
  || fail "values.schema.json must close the root and server objects (additionalProperties: false)"
jq -e '.properties.server.properties | (has("replicaCount") or has("replicas") or has("autoscaling") or has("strategy") or has("workloadKind")) | not' "$schema" >/dev/null \
  || fail "values.schema.json declares a replica key for the server"

echo "→ lint and render every ci/ values file"
for f in "$chart"/ci/*.yaml; do
  helm lint --strict -f "$f" "$chart" >/dev/null || fail "helm lint --strict failed for $f"
done

# Every release: the invariants that do not depend on the values.
check_release() {
  local label=$1 f d p
  [ -z "$(docs "$label" StatefulSet)$(docs "$label" HorizontalPodAutoscaler)" ] || fail "$label: a StatefulSet or an HPA"
  [ -z "$(docs "$label" Secret)" ] || fail "$label: the chart renders a Secret"
  # An environment entry named like a secret is a reference, never a literal.
  for f in "$work/$label"/doc-*.yaml; do
    ! grep -B1 -hE '^ +value:' "$f" | grep -iqE 'name:.*(TOKEN|KEY|SECRET|PASSWORD)' || fail "$label: a literal secret value in an environment ($f)"
  done
  d=$(one "$label" Deployment server)
  must "$label" "$d" '^  replicas: 1$' "the server is not one replica"
  must "$label" "$d" '^    type: Recreate$' "the server strategy is not Recreate"
  must "$label" "$d" '^        fsGroup: 2000$' "no fsGroup"
  must "$label" "$d" '^        fsGroupChangePolicy: OnRootMismatch$' "no fsGroupChangePolicy"
  must "$label" "$d" '^      automountServiceAccountToken: false$' "the server mounts the API token"
  must "$label" "$d" 'path: /health$' "no /health probe"
  must "$label" "$d" 'containerPort: 39273$' "the container port is not 39273"
  if grep -q 'name: credentials$' "$d"; then
    must "$label" "$d" '^            defaultMode: 0440$' "the credentials Secret is not mounted 0440"
    must "$label" "$d" 'mountPath: /etc/cartographer-credentials$' "the credentials mount path"
  fi
  p=$(one "$label" ConfigMap server)
  must "$label" "$p" '^    http: ":39273"$' "config.yaml http is not the quoted :39273"
  must "$label" "$p" '^    data: /data$' "config.yaml data is not /data"
  must "$label" "$p" '^      mode: "on"$' 'config.yaml auth mode is not the quoted string "on"'
  p=$(one "$label" Service server)
  must "$label" "$p" '^      port: 39273$' "the Service does not expose 39273"
  must "$label" "$p" 'app.kubernetes.io/component: server$' "the Service does not select the server"
  for f in $(docs "$label" Deployment) $(docs "$label" CronJob); do
    grep -qE 'component: surveyor-' "$f" || continue
    mustnt "$label" "$f" '^ +ports:|Probe:|persistentVolumeClaim|^ +secret:' "a Surveyor with a port, a probe or a persistent volume ($f)"
    must "$label" "$f" 'emptyDir: \{\}' "a Surveyor without its temp emptyDir ($f)"
  done
}

for f in "$chart"/ci/*-values.yaml; do
  name=$(basename "$f" -values.yaml)
  render "$name" ci -f "$f"
  check_release "$name"
done

# What differs per case.
d=$(one server-minimal Deployment server)
must server-minimal "$d" 'name: CARTOGRAPHER_TOKENS$' "source (a) sets no CARTOGRAPHER_TOKENS"
must server-minimal "$d" 'claimName: ci-cartographer-data$' "the default claim is not mounted"
mustnt server-minimal "$d" 'name: credentials$' "credentials.enabled=false still mounts the Secret"
must server-minimal "$d" "wget -q -O - http://127.0.0.1:39273/health \| grep -q '\"ready\":true'" "the default readiness is not the /health body"
p=$(one server-minimal PersistentVolumeClaim)
must server-minimal "$p" 'helm.sh/resource-policy: keep$' "the claim is not kept on uninstall"

d=$(one server-full Deployment server)
must server-full "$d" 'secretName: "cartographer-credentials"$' "the credentials Secret is not the named one"
must server-full "$d" 'claimName: cartographer-data$' "the existing claim is not mounted"
must server-full "$d" 'name: PROBE_TOKEN$' "the probe token is not a Secret reference"
must server-full "$d" 'http://127.0.0.1:39273/ready$' "the token readiness does not call /ready"
[ -z "$(docs server-full PersistentVolumeClaim)" ] || fail "server-full: a claim next to existingClaim"
must server-full "$(one server-full Ingress)" 'secretName: wiki-tls$' "the Ingress has no TLS"

d=$(one server-yaml-tokens Deployment server)
mustnt server-yaml-tokens "$d" 'CARTOGRAPHER_TOKENS' "source (b) sets CARTOGRAPHER_TOKENS, which would replace the YAML tokens"
must server-yaml-tokens "$d" 'name: "cartographer-token-env"$' "extraEnvFrom is not an envFrom secretRef"
must server-yaml-tokens "$(one server-yaml-tokens ConfigMap server)" 'artifacts: r$' "the Surveyor role lost artifacts: r"

# The partitioned pair: two releases, no object in common.
render pa rel-a -f "$chart/ci/partitioned-a-values.yaml"
render pb rel-b -f "$chart/ci/partitioned-b-values.yaml"
ids() { for f in "$work/$1"/doc-*.yaml; do echo "$(grep -m1 '^kind:' "$f") $(grep -m1 '^  name:' "$f")"; done | sort; }
[ -z "$(comm -12 <(ids pa) <(ids pb))" ] || fail "the partitioned releases share an object: $(comm -12 <(ids pa) <(ids pb))"

# Surveyors.
s=$(one surveyor-scheduled CronJob)
must surveyor-scheduled "$s" '^  concurrencyPolicy: Forbid$' "the CronJob allows overlapping runs"
must surveyor-scheduled "$s" '^ +- --once$' "a CronJob Surveyor without --once"
must surveyor-scheduled "$s" 'restartPolicy: Never$' "the CronJob restarts"
must surveyor-scheduled "$s" 'backoffLimit: 0$' "the CronJob retries"
must surveyor-scheduled "$s" 'automountServiceAccountToken: false$' "a Surveyor without a service account mounts the API token"
must surveyor-scheduled "$s" 'name: "docs-night-env"$' "existingSecret is not an envFrom"
c=$(one surveyor-scheduled ConfigMap surveyor-docs-night)
must surveyor-scheduled "$c" 'server_url: http://ci-cartographer:39273/mcp$' "no injected server_url"
must surveyor-scheduled "$c" 'allow_insecure_http: true$' "no injected allow_insecure_http"
must surveyor-scheduled "$c" '^    name: docs-night$' "the perimeter name is not injected"
must surveyor-scheduled "$c" '^  recipe-notes.md: \|$' "files are not in the ConfigMap"

d=$(one surveyor-exec Deployment surveyor-cluster-inventory)
must surveyor-exec "$d" '^      automountServiceAccountToken: true$' "the entry with a serviceAccountName does not mount the API token"
must surveyor-exec "$d" 'serviceAccountName: "surveyor-inventory"$' "serviceAccountName is not applied"
must surveyor-exec "$d" 'image: "registry.example.com/user/surveyor-kubectl:1.0.0"$' "the image override is not applied"
mustnt surveyor-exec "$d" '^ +- --once$' "a Deployment Surveyor with --once"
must surveyor-exec "$d" '^    type: Recreate$' "a Surveyor Deployment is not Recreate"
c=$(one surveyor-exec ConfigMap surveyor-cluster-inventory)
must surveyor-exec "$c" 'server_url: https://wiki.example.com/mcp$' "the entry's own server_url is not kept"
mustnt surveyor-exec "$c" 'allow_insecure_http' "an entry with its own server_url got allow_insecure_http"
# Only the entry with a serviceAccountName may mount the API token.
for label in server-minimal server-full surveyor-scheduled; do
  for f in "$work/$label"/doc-*.yaml; do
    mustnt "$label" "$f" 'automountServiceAccountToken: true' "the API token is mounted"
  done
done

echo "→ every refusal"
neg "no token source" '{}' nobase
neg "mutually exclusive" '{server: {extraEnvFrom: [e], config: {auth: {tokens: ["${A}"]}}}}'
neg "whole-value" '{server: {tokens: {existingSecret: {name: ""}}, extraEnvFrom: [e], config: {auth: {tokens: [{token: literal}]}}}}'
neg "needs server.extraEnvFrom" '{server: {tokens: {existingSecret: {name: ""}}, config: {auth: {tokens: ["${A}"]}}}}'
neg "server.credentials.existingSecret.name is required" '{server: {credentials: {enabled: true}}}'
neg "exclusive" '{server: {persistence: {existingClaim: c, emptyDir: true}}}'
neg "owned by the chart" '{server: {config: {http: ":1"}}}'
neg "owned by the chart" '{server: {config: {data: /x}}}'
neg "owned by the chart" '{server: {config: {credentials_dir: /x}}}'
neg "server.ingress.hosts" '{server: {ingress: {enabled: true}}}'
neg "replicaCount" '{server: {replicaCount: 2}}'
neg "autoscaling" '{server: {autoscaling: {enabled: true}}}'
neg "worker" '{worker: {enabled: true}}'
sv='mode: deployment, existingSecret: e, config: {kbs: [kb-a]}'
neg "surveyors[0].name is required" "{surveyors: [{$sv}]}"
neg "surveyors[0].name is required" "{surveyors: [{name: Bad_Name, $sv}]}"
neg "is used twice" "{surveyors: [{name: a, $sv}, {name: a, $sv}]}"
neg "surveyors[0].mode is required" '{surveyors: [{name: a, existingSecret: e, config: {kbs: []}}]}'
neg "mode" "{surveyors: [{name: a, mode: job, existingSecret: e, config: {kbs: []}}]}"
neg "needs a schedule" '{surveyors: [{name: a, mode: cronjob, existingSecret: e, config: {kbs: []}}]}'
neg "replicas applies to mode deployment only" '{surveyors: [{name: a, mode: cronjob, schedule: "1 * * * *", replicas: 2, existingSecret: e, config: {kbs: []}}]}'
neg "schedule applies to mode cronjob only" "{surveyors: [{name: a, schedule: '1 * * * *', $sv}]}"
neg "config is required" '{surveyors: [{name: a, mode: deployment, existingSecret: e}]}'
neg "config.name is owned by the chart" '{surveyors: [{name: a, mode: deployment, existingSecret: e, config: {name: x}}]}'
neg "existingSecret is required" '{surveyors: [{name: a, mode: deployment, config: {kbs: []}}]}'
neg "holds a /" "{surveyors: [{name: a, files: {\"d/x\": t}, $sv}]}"
neg "52 characters" "{surveyors: [{name: $(printf 'x%.0s' $(seq 40)), $sv}]}"
neg "typo" '{surveyors: [{name: a, mode: deployment, existingSecret: e, config: {kbs: []}, typo: 1}]}'

echo "helm-check: ok"
