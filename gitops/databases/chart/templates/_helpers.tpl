{{- define "database-demo.metadata" -}}
name: db-{{ .root.Values.provider }}-{{ .root.Values.tenant }}-{{ .suffix }}
namespace: {{ .root.Values.configNamespace }}
labels:
  lab.obco.dev/profile: database
{{- end -}}
{{- define "database-demo.common" -}}
connectionRef:
  name: {{ .Values.connectionRef }}
deletionPolicy: Orphan
{{- if ne .Values.tenant "root" }}
namespace: lab-{{ .Values.tenant }}
{{- end }}
{{- end -}}
