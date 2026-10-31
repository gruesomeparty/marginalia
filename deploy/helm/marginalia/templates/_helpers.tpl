{{- define "marginalia.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "marginalia.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "marginalia.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "marginalia.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "marginalia.selectorLabels" -}}
app.kubernetes.io/name: {{ include "marginalia.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "marginalia.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "marginalia.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "marginalia.image" -}}
{{- if .Values.image.digest }}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest }}
{{- else }}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}
{{- end }}

{{/* publicURL, checked: the host refuses anything but an origin, and so does the chart, at template time rather than in a crash loop. */}}
{{- define "marginalia.publicURL" -}}
{{- $u := required "publicURL is required: the origin reviewers and agents reach the host at, e.g. https://review.example.com" .Values.publicURL }}
{{- if not (regexMatch "^https?://[^/?#]+/?$" $u) }}
{{- fail (printf "publicURL %q must be a scheme and host only, e.g. https://review.example.com" $u) }}
{{- end }}
{{- trimSuffix "/" $u }}
{{- end }}

{{- define "marginalia.host" -}}
{{- include "marginalia.publicURL" . | regexFind "//[^/:]+" | trimPrefix "//" }}
{{- end }}

{{- define "marginalia.claimName" -}}
{{- default (include "marginalia.fullname" .) .Values.persistence.existingClaim }}
{{- end }}

{{- define "marginalia.store" -}}
{{- if .Values.store }}
{{- .Values.store }}
{{- else }}
{{- printf "file://%s" .Values.persistence.mountPath }}
{{- end }}
{{- end }}

{{/*
The paths the host authenticates itself and that must not sit behind the
OIDC proxy, as (path, pathType) pairs.
*/}}
{{- define "marginalia.publicPaths" -}}
- path: /mcp
  type: PathPrefix
- path: /agent/
  type: PathPrefix
- path: /healthz
  type: Exact
- path: /.well-known/oauth-protected-resource
  type: PathPrefix
{{- end }}
