{{- define "order-management.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "order-management.fullname" -}}
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

{{- define "order-management.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "order-management.labels" -}}
helm.sh/chart: {{ include "order-management.chart" . }}
{{ include "order-management.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "order-management.selectorLabels" -}}
app.kubernetes.io/name: {{ include "order-management.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "order-management.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "order-management.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "order-management.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "order-management.fullname" . }}-database
{{- end }}
{{- end }}

{{/*
Fully qualified name of the analytics projector deployment (ADR-0006).
*/}}
{{- define "order-management.projectorFullname" -}}
{{- include "order-management.fullname" . }}-projector
{{- end }}

{{/*
Fully qualified name of the analytics reports deployment/service (ADR-0006).
*/}}
{{- define "order-management.reportsFullname" -}}
{{- include "order-management.fullname" . }}-reports
{{- end }}

{{/*
Name of the Secret holding the analytics DSNs, when the chart creates its own.
*/}}
{{- define "order-management.analyticsSecretName" -}}
{{- if .Values.analytics.database.existingSecret }}
{{- .Values.analytics.database.existingSecret }}
{{- else }}
{{- include "order-management.fullname" . }}-analytics
{{- end }}
{{- end }}

{{/*
Name of the Secret holding the REST identity keys (ADR-0011): API_READ_KEY,
API_READWRITE_KEY and the outbound INVENTORY_STORAGE_API_KEY.
*/}}
{{- define "order-management.authSecretName" -}}
{{- if .Values.auth.existingSecret }}
{{- .Values.auth.existingSecret }}
{{- else }}
{{- include "order-management.fullname" . }}-auth
{{- end }}
{{- end }}

{{/*
Non-empty when at least one REST identity key value is set, i.e. when the
chart should render its own auth Secret and the deployments should reference
it. Empty string otherwise (falsy in an `if`).
*/}}
{{- define "order-management.authSecretHasKeys" -}}
{{- if or .Values.auth.readKey .Values.auth.readWriteKey .Values.inventoryStorage.apiKey -}}
true
{{- end -}}
{{- end }}

{{/*
Non-empty when the deployments should mount the auth Secret: either an
existing Secret is named, or the chart renders one because a key is set.
*/}}
{{- define "order-management.authSecretEnabled" -}}
{{- if or .Values.auth.existingSecret (include "order-management.authSecretHasKeys" .) -}}
true
{{- end -}}
{{- end }}
