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
{{- end -}}
