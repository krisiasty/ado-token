{{- define "ado-token.name" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "ado-token.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
app.kubernetes.io/name: {{ include "ado-token.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "ado-token.credentialsNamespace" -}}
{{- .Values.credentialsSecret.namespace | default .Release.Namespace }}
{{- end }}

{{- define "ado-token.outputNamespace" -}}
{{- .Values.outputSecret.namespace | default .Release.Namespace }}
{{- end }}

{{- define "ado-token.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) }}
{{- end }}
