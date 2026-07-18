{{- define "crane-cost-collector.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "crane-cost-collector.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name (include "crane-cost-collector.name" .) | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "crane-cost-collector.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "crane-cost-collector.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: crane
{{- end }}

{{- define "crane-cost-collector.selectorLabels" -}}
app.kubernetes.io/name: {{ include "crane-cost-collector.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "crane-cost-collector.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "crane-cost-collector.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- required "serviceAccount.name is required when serviceAccount.create=false" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "crane-cost-collector.claimName" -}}
{{- default (include "crane-cost-collector.fullname" .) .Values.persistence.existingClaim }}
{{- end }}
