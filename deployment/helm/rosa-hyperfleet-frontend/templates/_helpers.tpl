{{/*
Expand the name of the chart.
*/}}
{{- define "rosa-hyperfleet.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "rosa-hyperfleet.fullname" -}}
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
Create chart name and version as used by the chart label.
*/}}
{{- define "rosa-hyperfleet.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "rosa-hyperfleet.labels" -}}
helm.sh/chart: {{ include "rosa-hyperfleet.chart" . }}
{{ include "rosa-hyperfleet.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "rosa-hyperfleet.selectorLabels" -}}
app.kubernetes.io/name: {{ include "rosa-hyperfleet.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
lookupConfigMapValue retrieves a key from a ConfigMap. Returns "" when run offline (e.g. helm template) or when ConfigMap is missing.
Usage: include "lookupConfigMapValue" (dict "Namespace" "kube-system" "Name" "bootstrap-output" "Key" "api_target_group_arn" "Scope" .)
*/}}
{{- define "lookupConfigMapValue" -}}
{{- $key := .Key -}}
{{- $cm := lookup "v1" "ConfigMap" .Namespace .Name -}}
{{- if and $cm $cm.data -}}
{{- index $cm.data $key | default "" -}}
{{- else -}}
{{- "" -}}
{{- end -}}
{{- end -}}
