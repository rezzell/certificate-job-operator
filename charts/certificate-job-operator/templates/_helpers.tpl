{{- define "certificate-job-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "certificate-job-operator.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := include "certificate-job-operator.name" . -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "certificate-job-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "certificate-job-operator.labels" -}}
helm.sh/chart: {{ include "certificate-job-operator.chart" . }}
app.kubernetes.io/name: {{ include "certificate-job-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "certificate-job-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "certificate-job-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
control-plane: controller-manager
{{- end -}}

{{- define "certificate-job-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "certificate-job-operator.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "certificate-job-operator.webhookSecretName" -}}
{{- if .Values.webhook.secretName -}}
{{- .Values.webhook.secretName -}}
{{- else -}}
{{- printf "%s-webhook-server-cert" (include "certificate-job-operator.fullname" .) -}}
{{- end -}}
{{- end -}}
