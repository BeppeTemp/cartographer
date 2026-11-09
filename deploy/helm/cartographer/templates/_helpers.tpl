{{/* Names. Every object derives from the full name, so two releases in one namespace never collide. */}}
{{- define "cartographer.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "cartographer.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/* Labels. The selector set is name, instance and component only: it is stable across upgrades. */}}
{{- define "cartographer.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cartographer.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{- define "cartographer.labels" -}}
{{ include "cartographer.selectorLabels" . }}
helm.sh/chart: {{ printf "%s-%s" .root.Chart.Name .root.Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
app.kubernetes.io/version: {{ .root.Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
{{- end -}}

{{- define "cartographer.serverImage" -}}
{{- $i := .Values.server.image -}}
{{- printf "%s:%s" $i.repository (default .Chart.AppVersion $i.tag) -}}
{{- end -}}

{{/* A literal token reference: the whole value is ${NAME}, read from the environment at startup. */}}
{{- define "cartographer.isTokenRef" -}}
{{- if and (kindIs "string" .) (regexMatch "^\\$\\{[A-Za-z_][A-Za-z0-9_]*\\}$" .) -}}yes{{- end -}}
{{- end -}}

{{/* The Surveyor entries that take the injected server URL (they set none of their own). */}}
{{- define "cartographer.injectsServerURL" -}}
{{- $yes := false -}}
{{- range .Values.surveyors -}}
{{- if not (hasKey (default dict .config) "server_url") -}}{{- $yes = true -}}{{- end -}}
{{- end -}}
{{- if $yes -}}yes{{- end -}}
{{- end -}}

{{/* Every template-time refusal lives here; the output is empty. */}}
{{- define "cartographer.validate" -}}
{{- $s := .Values.server -}}
{{- $cfg := $s.config -}}
{{- range $k := list "http" "data" "credentials_dir" -}}
{{- if hasKey $cfg $k -}}
{{- fail (printf "server.config.%s is owned by the chart (it is coupled to the container port, the probes and the mounts): remove it from your values" $k) -}}
{{- end -}}
{{- end -}}
{{- $yamlTokens := dig "auth" "tokens" (list) $cfg -}}
{{- $fromSecret := $s.tokens.existingSecret.name -}}
{{- if and $fromSecret $yamlTokens -}}
{{- fail "server.tokens.existingSecret and server.config.auth.tokens are mutually exclusive: the CARTOGRAPHER_TOKENS environment variable replaces every token the YAML declares. Use one source" -}}
{{- end -}}
{{- if not (or $fromSecret $yamlTokens) -}}
{{- fail "no token source: set server.tokens.existingSecret.name (a Secret whose key holds the whole CARTOGRAPHER_TOKENS string, `kubectl create secret generic <name> --from-literal=tokens=<token>`) or declare server.config.auth.tokens as ${NAME} references with server.extraEnvFrom. An environment CARTOGRAPHER_TOKENS replaces YAML tokens, so the two sources are exclusive" -}}
{{- end -}}
{{- if $yamlTokens -}}
{{- if not $s.extraEnvFrom -}}
{{- fail "server.config.auth.tokens needs server.extraEnvFrom: a token is a ${NAME} reference read from the environment, and the variable comes from an existing Secret listed there (a literal would be rendered into a ConfigMap)" -}}
{{- end -}}
{{- range $yamlTokens -}}
{{- $t := . -}}
{{- if kindIs "map" . -}}{{- $t = .token -}}{{- end -}}
{{- if not (include "cartographer.isTokenRef" $t) -}}
{{- fail "every server.config.auth.tokens entry must be a whole-value ${NAME} reference (a bare string or the `token:` field): a literal token would be rendered into a ConfigMap. Put the secret in an existing Secret and list it in server.extraEnvFrom" -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if and $s.credentials.enabled (not $s.credentials.existingSecret.name) -}}
{{- fail "server.credentials.existingSecret.name is required: the Secret holds audit.key (`openssl rand -hex 32`, the server cannot generate it on a read-only mount) and the KBs' key files (default.ssh, known_hosts, <kb>.token); or set server.credentials.enabled=false" -}}
{{- end -}}
{{- if and $s.persistence.existingClaim $s.persistence.emptyDir -}}
{{- fail "server.persistence.existingClaim and server.persistence.emptyDir are exclusive: choose one" -}}
{{- end -}}
{{- if and $s.ingress.enabled (not $s.ingress.hosts) -}}
{{- fail "server.ingress.enabled needs server.ingress.hosts: name at least one host" -}}
{{- end -}}
{{- $seen := dict -}}
{{- range $i, $sv := .Values.surveyors -}}
{{- $n := default "" $sv.name -}}
{{- if not (regexMatch "^[a-z0-9-]{1,40}$" $n) -}}
{{- fail (printf "surveyors[%d].name is required and must match [a-z0-9-], 1 to 40 characters" $i) -}}
{{- end -}}
{{- if hasKey $seen $n -}}
{{- fail (printf "surveyors[%d].name %q is used twice: a name is unique in the list" $i $n) -}}
{{- end -}}
{{- $_ := set $seen $n true -}}
{{- if gt (len (printf "%s-surveyor-%s" (include "cartographer.fullname" $) $n)) 52 -}}
{{- fail (printf "surveyors[%d].name %q makes the object name longer than 52 characters (the CronJob limit): shorten it or set a shorter fullnameOverride" $i $n) -}}
{{- end -}}
{{- if not (has $sv.mode (list "deployment" "cronjob")) -}}
{{- fail (printf "surveyors[%d].mode is required: deployment or cronjob" $i) -}}
{{- end -}}
{{- if eq $sv.mode "cronjob" -}}
{{- if not $sv.schedule -}}{{- fail (printf "surveyors[%d] is a cronjob and needs a schedule (a cron expression)" $i) -}}{{- end -}}
{{- if hasKey $sv "replicas" -}}{{- fail (printf "surveyors[%d].replicas applies to mode deployment only" $i) -}}{{- end -}}
{{- else if $sv.schedule -}}
{{- fail (printf "surveyors[%d].schedule applies to mode cronjob only" $i) -}}
{{- end -}}
{{- if not $sv.config -}}
{{- fail (printf "surveyors[%d].config is required: the Surveyor YAML as a map" $i) -}}
{{- end -}}
{{- if hasKey $sv.config "name" -}}
{{- fail (printf "surveyors[%d].config.name is owned by the chart: it is set from surveyors[%d].name" $i $i) -}}
{{- end -}}
{{- if not $sv.existingSecret -}}
{{- fail (printf "surveyors[%d].existingSecret is required: the Secret whose keys become the environment (the token and the keys the config names with token_env and api_key_env)" $i) -}}
{{- end -}}
{{- range $f, $_ := ($sv.files | default dict) -}}
{{- if eq $f "surveyor.yaml" -}}
{{- fail (printf "surveyors[%d].files key \"surveyor.yaml\" collides with the Surveyor config the chart renders under that name: pick another file name" $i) -}}
{{- end -}}
{{- if contains "/" $f -}}
{{- fail (printf "surveyors[%d].files key %q holds a /: a ConfigMap key cannot, and the config refers to the file by its plain name" $i $f) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/* The image of a Surveyor entry: surveyorImage, each field overridable by the entry's image. */}}
{{- define "cartographer.surveyorImage" -}}
{{- $d := .root.Values.surveyorImage -}}
{{- $o := default dict .sv.image -}}
{{- printf "%s:%s" (default $d.repository $o.repository) (default (default .root.Chart.AppVersion $d.tag) $o.tag) -}}
{{- end -}}

{{/* The pod template of a Surveyor entry, for a Deployment (cron false) or a CronJob (cron true).
     No port, no probe, no persistent volume: the config and one emptyDir on /tmp (exec recipes
     and git reads need a writable temp directory). */}}
{{- define "cartographer.surveyorPod" -}}
{{- $sv := .sv -}}
{{- $root := .root -}}
{{- $obj := printf "%s-surveyor-%s" (include "cartographer.fullname" $root) $sv.name -}}
automountServiceAccountToken: {{ if $sv.serviceAccountName }}true{{ else }}false{{ end }}
{{- with $sv.serviceAccountName }}
serviceAccountName: {{ . | quote }}
{{- end }}
{{- with $root.Values.imagePullSecrets }}
imagePullSecrets:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- if .cron }}
restartPolicy: Never
{{- end }}
terminationGracePeriodSeconds: {{ ternary $sv.terminationGracePeriodSeconds 30 (hasKey $sv "terminationGracePeriodSeconds") }}
securityContext:
  seccompProfile:
    type: RuntimeDefault
containers:
  - name: surveyor
    image: {{ include "cartographer.surveyorImage" (dict "root" $root "sv" $sv) | quote }}
    imagePullPolicy: {{ default $root.Values.surveyorImage.pullPolicy (default dict $sv.image).pullPolicy }}
    args:
      - --config=/etc/cartographer/surveyor.yaml
      {{- if .cron }}
      - --once
      {{- end }}
    securityContext:
      allowPrivilegeEscalation: false
      readOnlyRootFilesystem: true
      capabilities:
        drop: [ALL]
    envFrom:
      - secretRef:
          name: {{ $sv.existingSecret | quote }}
    {{- with $sv.resources }}
    resources:
      {{- toYaml . | nindent 6 }}
    {{- end }}
    volumeMounts:
      - name: config
        mountPath: /etc/cartographer
        readOnly: true
      - name: tmp
        mountPath: /tmp
volumes:
  - name: config
    configMap:
      name: {{ $obj }}
  - name: tmp
    emptyDir: {}
{{- with $sv.nodeSelector }}
nodeSelector:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with $sv.affinity }}
affinity:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with $sv.tolerations }}
tolerations:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end -}}
