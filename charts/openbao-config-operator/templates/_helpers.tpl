{{- define "openbao-config-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "openbao-config-operator.fullname" -}}
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

{{- define "openbao-config-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "openbao-config-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "openbao-config-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "openbao-config-operator.labels" -}}
helm.sh/chart: {{ include "openbao-config-operator.chart" . }}
{{ include "openbao-config-operator.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "openbao-config-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "openbao-config-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- required "serviceAccount.name is required when serviceAccount.create is false" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "openbao-config-operator.image" -}}
{{- if .Values.image.digest }}
{{- printf "%s@%s" .Values.image.repository .Values.image.digest }}
{{- else }}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) }}
{{- end }}
{{- end }}

{{- define "openbao-config-operator.watchNamespaces" -}}
{{- join "," (uniq .Values.watchNamespaces) }}
{{- end }}

{{/*
User-facing rules: one rule per API group for the selected resources and a read-only rule for their status.
Arguments: dict "root" . "verbs" list "excludePlurals" list "namespacedOnly" bool.
*/}}
{{- define "openbao-config-operator.userRules" -}}
{{- $byGroup := dict }}
{{- range (include "openbao-config-operator.crdResources" .root | fromYamlArray) }}
{{- if and (not (has .plural $.excludePlurals)) (or (not $.namespacedOnly) (eq .scope "Namespaced")) (or (not .plugin) $.root.Values.pluginKinds.enabled) }}
{{- $_ := set $byGroup .group (append (get $byGroup .group | default list) .plural) }}
{{- end }}
{{- end }}
{{- range $group := keys $byGroup | sortAlpha }}
{{- $plurals := get $byGroup $group | sortAlpha }}
- apiGroups: [{{ $group | quote }}]
  resources:
    {{- toYaml $plurals | nindent 4 }}
  verbs:
    {{- toYaml $.verbs | nindent 4 }}
- apiGroups: [{{ $group | quote }}]
  resources:
    {{- range $plurals }}
    - {{ printf "%s/status" . }}
    {{- end }}
  verbs:
    - get
{{- end }}
{{- end }}
