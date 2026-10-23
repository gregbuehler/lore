{{- define "lore.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- define "lore.fullname" -}}
{{- if .Values.fullnameOverride -}}{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}{{- else if contains (include "lore.name" .) .Release.Name -}}{{- .Release.Name | trunc 63 | trimSuffix "-" -}}{{- else -}}{{- printf "%s-%s" .Release.Name (include "lore.name" .) | trunc 63 | trimSuffix "-" -}}{{- end -}}
{{- end -}}
{{- define "lore.labels" -}}
app.kubernetes.io/name: {{ include "lore.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
{{- end -}}
{{- define "lore.selectorLabels" -}}
app.kubernetes.io/name: {{ include "lore.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
