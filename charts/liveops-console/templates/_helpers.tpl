{{- define "liveops-console.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "liveops-console.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "liveops-console.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "liveops-console.labels" -}}
app.kubernetes.io/name: {{ include "liveops-console.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}

{{- define "liveops-console.selectorLabels" -}}
app.kubernetes.io/name: {{ include "liveops-console.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "liveops-console.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}

{{- define "liveops-console.secretName" -}}
{{- if .Values.auth.existingSecret -}}
{{- .Values.auth.existingSecret -}}
{{- else -}}
{{- include "liveops-console.fullname" . -}}
{{- end -}}
{{- end -}}

{{- define "liveops-console.dsn" -}}
{{- if .Values.database.dsn -}}
{{- .Values.database.dsn -}}
{{- else -}}
{{- printf "postgres://liveops:liveops@%s-postgres:5432/liveops?sslmode=disable" .Release.Name -}}
{{- end -}}
{{- end -}}
