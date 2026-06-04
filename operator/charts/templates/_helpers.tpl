{{/*
Expand the name of the chart.
*/}}
{{- define "gpu-sharing-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name.
*/}}
{{- define "gpu-sharing-operator.fullname" -}}
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

{{/*
Common labels.
*/}}
{{- define "gpu-sharing-operator.labels" -}}
helm.sh/chart: {{ include "gpu-sharing-operator.chart" . }}
{{ include "gpu-sharing-operator.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "gpu-sharing-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gpu-sharing-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Chart name and version.
*/}}
{{- define "gpu-sharing-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Webhook service name.
*/}}
{{- define "gpu-sharing-operator.webhookServiceName" -}}
{{- printf "%s-webhook" (include "gpu-sharing-operator.fullname" .) }}
{{- end }}

{{/*
Webhook cert secret name.
*/}}
{{- define "gpu-sharing-operator.webhookCertSecretName" -}}
{{- printf "%s-webhook-cert" (include "gpu-sharing-operator.fullname" .) }}
{{- end }}

{{/*
Service account name.
*/}}
{{- define "gpu-sharing-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "gpu-sharing-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}
