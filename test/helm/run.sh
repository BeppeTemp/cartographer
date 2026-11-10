#!/usr/bin/env bash
# The Helm chart's checks (make helm-check, job `helm` of ci.yml, and the release workflows
# before a push): helm lint --strict and helm template over every values file of
# deploy/helm/cartographer/ci/, then assertions on what is rendered. Needs helm, jq and
# coreutils, no cluster. The invariants it pins are safety properties of the deploy
# artifact (D1441): one Meridian replica, no Secret object, no literal secret in an
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
# (${args[@]+...} keeps an empty array legal under set -u on bash 3.2, macOS.)
neg() {
  local want=$1 yaml=$2 out args=()
  printf '%s\n' "$yaml" >"$work/neg-case.yaml"
  if [ "${3:-}" != nobase ]; then
    printf 'meridian: {tokens: {existingSecret: {name: t}}, credentials: {enabled: false}}\n' >"$work/neg-base.yaml"
    args=(-f "$work/neg-base.yaml")
  fi
  if out=$(helm template ci "$chart" ${args[@]+"${args[@]}"} -f "$work/neg-case.yaml" 2>&1); then
    fail "negative case accepted, want '$want': $yaml"
  fi
  case $out in *"$want"*) ;; *) fail "negative case '$yaml' failed without '$want': $out" ;; esac
}

echo "→ the chart's files"
bad=$(grep -rliE 'worker' "$chart" || true)
[ -z "$bad" ] || fail "retired name in: $bad"
# The release workflows stamp both with helm package; a hand edit would ship the wrong tag.
grep -qx 'version: 0.0.0-dev' "$chart/Chart.yaml" || fail "Chart.yaml version is not the 0.0.0-dev placeholder"
grep -qx 'appVersion: "v0.0.0-dev"' "$chart/Chart.yaml" || fail "Chart.yaml appVersion is not the v0.0.0-dev placeholder"
schema=$chart/values.schema.json
jq -e '.additionalProperties == false and .properties.meridian.additionalProperties == false' "$schema" >/dev/null \
  || fail "values.schema.json must close the root and meridian objects (additionalProperties: false)"
jq -e '.properties.meridian.properties | (has("replicaCount") or has("replicas") or has("autoscaling") or has("strategy") or has("workloadKind")) | not' "$schema" >/dev/null \
  || fail "values.schema.json declares a replica key for meridian"

echo "→ lint and render every ci/ values file"
for f in "$chart"/ci/*.yaml; do
  helm lint --strict -f "$f" "$chart" >/dev/null || fail "helm lint --strict failed for $f"
done

# Every release: the invariants that do not depend on the values.
check_common() {
  local label=$1 f
  [ -z "$(docs "$label" StatefulSet)$(docs "$label" HorizontalPodAutoscaler)" ] || fail "$label: a StatefulSet or an HPA"
  [ -z "$(docs "$label" Secret)" ] || fail "$label: the chart renders a Secret"
  # An environment entry named like a secret is a reference, never a literal.
  for f in "$work/$label"/doc-*.yaml; do
    ! grep -B1 -hE '^ +value:' "$f" | grep -iqE 'name:.*(TOKEN|KEY|SECRET|PASSWORD)' || fail "$label: a literal secret value in an environment ($f)"
  done
  for f in $(docs "$label" Deployment) $(docs "$label" CronJob); do
    grep -qE 'component: surveyor-' "$f" || continue
    mustnt "$label" "$f" '^ +ports:|Probe:|persistentVolumeClaim|^ +secret:' "a Surveyor with a port, a probe or a persistent volume ($f)"
    must "$label" "$f" 'emptyDir: \{\}' "a Surveyor without its temp emptyDir ($f)"
  done
}

# A release with Meridian: one Deployment, its ConfigMap and its Service.
check_meridian() {
  local label=$1 d p
  d=$(one "$label" Deployment meridian)
  must "$label" "$d" '^  replicas: 1$' "Meridian is not one replica"
  must "$label" "$d" '^    type: Recreate$' "Meridian strategy is not Recreate"
  must "$label" "$d" '^        fsGroup: 2000$' "no fsGroup"
  must "$label" "$d" '^        fsGroupChangePolicy: OnRootMismatch$' "no fsGroupChangePolicy"
  must "$label" "$d" '^      automountServiceAccountToken: false$' "Meridian mounts the API token"
  must "$label" "$d" 'path: /health$' "no /health probe"
  must "$label" "$d" 'containerPort: 39273$' "the container port is not 39273"
  if grep -q 'name: credentials$' "$d"; then
    must "$label" "$d" '^            defaultMode: 0440$' "the credentials Secret is not mounted 0440"
    must "$label" "$d" 'mountPath: /etc/cartographer-credentials$' "the credentials mount path"
  fi
  p=$(one "$label" ConfigMap meridian)
  must "$label" "$p" '^    http: ":39273"$' "config.yaml http is not the quoted :39273"
  must "$label" "$p" '^    data: /data$' "config.yaml data is not /data"
  must "$label" "$p" '^      mode: "on"$' 'config.yaml auth mode is not the quoted string "on"'
  p=$(one "$label" Service meridian)
  must "$label" "$p" '^      port: 39273$' "the Service does not expose 39273"
  must "$label" "$p" 'app.kubernetes.io/component: meridian$' "the Service does not select Meridian"
}

# A release without Meridian (meridian.enabled=false): Surveyor objects only.
check_surveyor_only() {
  local label=$1 f n=0
  [ -z "$(docs "$label" Service)$(docs "$label" Ingress)$(docs "$label" PersistentVolumeClaim)" ] || fail "$label: a Service, an Ingress or a claim in a Surveyor-only release"
  for f in "$work/$label"/doc-*.yaml; do
    ! grep -qx '    app.kubernetes.io/component: meridian' "$f" || fail "$label: an object of component meridian ($f)"
    n=$((n + 1))
  done
  [ "$n" -gt 0 ] || fail "$label: renders nothing"
  for f in $(docs "$label" Deployment); do
    grep -qE 'component: surveyor-' "$f" || fail "$label: a Deployment that is not a Surveyor one ($f)"
  done
}

# The loop takes every file ending in -values.yaml: a new ci/ file must keep that suffix
# or it is never rendered. A file named surveyor-only* has no Meridian; all others do.
for f in "$chart"/ci/*-values.yaml; do
  name=$(basename "$f" -values.yaml)
  render "$name" ci -f "$f"
  check_common "$name"
  case $name in
    surveyor-only*) check_surveyor_only "$name" ;;
    *) check_meridian "$name" ;;
  esac
done

# What differs per case.
d=$(one meridian-minimal Deployment meridian)
must meridian-minimal "$d" 'name: CARTOGRAPHER_TOKENS$' "source (a) sets no CARTOGRAPHER_TOKENS"
must meridian-minimal "$d" 'claimName: ci-cartographer-data$' "the default claim is not mounted"
mustnt meridian-minimal "$d" 'name: credentials$' "credentials.enabled=false still mounts the Secret"
must meridian-minimal "$d" "wget -q -O - http://127.0.0.1:39273/health \| grep -q '\"ready\":true'" "the default readiness is not the /health body"
p=$(one meridian-minimal PersistentVolumeClaim)
must meridian-minimal "$p" 'helm.sh/resource-policy: keep$' "the claim is not kept on uninstall"

d=$(one meridian-full Deployment meridian)
must meridian-full "$d" 'secretName: "cartographer-credentials"$' "the credentials Secret is not the named one"
must meridian-full "$d" 'claimName: cartographer-data$' "the existing claim is not mounted"
must meridian-full "$d" 'name: PROBE_TOKEN$' "the probe token is not a Secret reference"
must meridian-full "$d" 'http://127.0.0.1:39273/ready$' "the token readiness does not call /ready"
[ -z "$(docs meridian-full PersistentVolumeClaim)" ] || fail "meridian-full: a claim next to existingClaim"
must meridian-full "$(one meridian-full Ingress)" 'secretName: wiki-tls$' "the Ingress has no TLS"

d=$(one meridian-yaml-tokens Deployment meridian)
mustnt meridian-yaml-tokens "$d" 'CARTOGRAPHER_TOKENS' "source (b) sets CARTOGRAPHER_TOKENS, which would replace the YAML tokens"
must meridian-yaml-tokens "$d" 'name: "cartographer-token-env"$' "extraEnvFrom is not an envFrom secretRef"
must meridian-yaml-tokens "$(one meridian-yaml-tokens ConfigMap meridian)" 'artifacts: r$' "the Surveyor role lost artifacts: r"

# Surveyors only: the injected server_url is meridian.url plus /mcp.
c=$(one surveyor-only ConfigMap surveyor-docs-night)
must surveyor-only "$c" 'server_url: https://wiki.example.com/mcp$' "no server_url from meridian.url"
mustnt surveyor-only "$c" 'allow_insecure_http' "an https meridian.url got allow_insecure_http"
one surveyor-only CronJob >/dev/null
one surveyor-only Deployment surveyor-inventory >/dev/null
c=$(one surveyor-only-http ConfigMap surveyor-docs-night)
must surveyor-only-http "$c" 'server_url: http://meridian.internal.example.com:39273/mcp$' "no server_url from the http meridian.url"
must surveyor-only-http "$c" 'allow_insecure_http: true$' "an http meridian.url without allow_insecure_http"

# The Atlas switch: the chart owns web.enabled in Meridian's config (serve.go mounts /ui/
# only when it is on, see the webEnabled branch there).
c=$(one meridian-minimal ConfigMap meridian)
must meridian-minimal "$c" '^    web:$' "no web block in the Meridian config"
must meridian-minimal "$c" '^      enabled: true$' "the Atlas is not on by default"
c=$(one meridian-no-atlas ConfigMap meridian)
must meridian-no-atlas "$c" '^    web:$' "no web block in the Meridian config"
must meridian-no-atlas "$c" '^      enabled: false$' "atlas.enabled=false did not turn the Atlas off"

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
for label in meridian-minimal meridian-full surveyor-scheduled; do
  for f in "$work/$label"/doc-*.yaml; do
    mustnt "$label" "$f" 'automountServiceAccountToken: true' "the API token is mounted"
  done
done

echo "→ every refusal"
neg "no token source" '{}' nobase
neg "mutually exclusive" '{meridian: {extraEnvFrom: [e], config: {auth: {tokens: ["${A}"]}}}}'
neg "whole-value" '{meridian: {tokens: {existingSecret: {name: ""}}, extraEnvFrom: [e], config: {auth: {tokens: [{token: literal}]}}}}'
neg "needs meridian.extraEnvFrom" '{meridian: {tokens: {existingSecret: {name: ""}}, config: {auth: {tokens: ["${A}"]}}}}'
neg "meridian.credentials.existingSecret.name is required" '{meridian: {credentials: {enabled: true}}}'
neg "exclusive" '{meridian: {persistence: {existingClaim: c, emptyDir: true}}}'
neg "owned by the chart" '{meridian: {config: {http: ":1"}}}'
neg "owned by the chart" '{meridian: {config: {data: /x}}}'
neg "owned by the chart" '{meridian: {config: {credentials_dir: /x}}}'
neg "owned by the chart" '{meridian: {config: {web: {enabled: false}}}}'
neg "meridian.ingress.hosts" '{meridian: {ingress: {enabled: true}}}'
neg "replicaCount" '{meridian: {replicaCount: 2}}'
neg "autoscaling" '{meridian: {autoscaling: {enabled: true}}}'
neg "additional properties 'server' not allowed" '{server: {replicaCount: 2}}'
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
neg "collides with the Surveyor config" "{surveyors: [{name: a, files: {surveyor.yaml: t}, $sv}]}"
neg "52 characters" "{surveyors: [{name: $(printf 'x%.0s' $(seq 40)), $sv}]}"
neg "typo" '{surveyors: [{name: a, mode: deployment, existingSecret: e, config: {kbs: []}, typo: 1}]}'

# Meridian disabled (nobase: there is no token source to add).
so='surveyors: [{name: a, mode: deployment, existingSecret: e, config: {kbs: [kb-a]}}]'
neg "renders nothing" '{meridian: {enabled: false, url: "https://m.example.com"}}' nobase
neg "meridian.url is required" "{meridian: {enabled: false}, $so}" nobase
neg "/meridian/url" "{meridian: {enabled: false, url: ftp://m.example.com}, $so}" nobase
neg "meridian.url applies to" '{meridian: {url: "https://m.example.com", tokens: {existingSecret: {name: t}}, credentials: {enabled: false}}}' nobase
for k in 'ingress: {enabled: true}' 'tokens: {existingSecret: {name: t}}' 'persistence: {existingClaim: c}' 'persistence: {emptyDir: true}' \
  'config: {kbs: [{remote: "https://git.example.com/user/kb-a.git"}]}' 'config: {auth: {tokens: ["${A}"]}}' 'extraEnvFrom: [e]' 'credentials: {existingSecret: {name: c}}'; do
  neg "is ignored when meridian.enabled=false" "{meridian: {enabled: false, url: \"https://m.example.com\", $k}, $so}" nobase
done

# Explicit zeros are honoured (default would turn them into 1 / 3 / 1 / 30), and a files text whose
# first line is deeper than the next ones is still valid YAML (a bare | block would break).
cat >"$work/zero-values.yaml" <<'V'
meridian: {tokens: {existingSecret: {name: t}}, credentials: {enabled: false}}
surveyors:
  - name: paused
    mode: deployment
    replicas: 0
    terminationGracePeriodSeconds: 0
    existingSecret: e
    files:
      odd.txt: "    deeper first line\nshallow second\n"
    config: {kbs: [kb-a]}
  - name: nightly
    mode: cronjob
    schedule: "1 * * * *"
    successfulJobsHistoryLimit: 0
    failedJobsHistoryLimit: 0
    existingSecret: e
    config: {kbs: [kb-a]}
V
render zeros ci -f "$work/zero-values.yaml"
d=$(one zeros Deployment surveyor-paused)
must zeros "$d" '^  replicas: 0$' "replicas: 0 is not rendered as 0"
must zeros "$d" 'terminationGracePeriodSeconds: 0$' "terminationGracePeriodSeconds: 0 is not rendered as 0"
must zeros "$d" 'readOnlyRootFilesystem: true$' "the Surveyor container root is writable"
j=$(one zeros CronJob)
must zeros "$j" '^  successfulJobsHistoryLimit: 0$' "successfulJobsHistoryLimit: 0 is not rendered as 0"
must zeros "$j" '^  failedJobsHistoryLimit: 0$' "failedJobsHistoryLimit: 0 is not rendered as 0"
c=$(one zeros ConfigMap surveyor-paused)
must zeros "$c" '^  odd.txt: \|[0-9]*-?$' "a files text is not a YAML block scalar"
must zeros "$c" '^        deeper first line$' "the deeper first line lost its indentation"
must zeros "$c" '^    shallow second$' "the second line is not kept"

echo "helm-check: ok"
