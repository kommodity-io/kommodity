{{/*
Return the Talos version, defaulting to .Chart.AppVersion if .Values.talos.version is not set or empty.
Usage: {{ include "kommodity.talosVersion" . }}
*/}}
{{- define "kommodity.talosVersion" -}}
{{- if and .Values.talos (hasKey .Values.talos "version") (not (empty .Values.talos.version)) -}}
	{{- .Values.talos.version -}}
{{- else -}}
	{{- .Chart.AppVersion -}}
{{- end -}}
{{- end -}}

{{/*
Expand the name of the chart.
*/}}
{{- define "kommodity-cluster.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "kommodity-cluster.fullname" -}}
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
{{- define "kommodity-cluster.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "kommodity-cluster.labels" -}}
helm.sh/chart: {{ include "kommodity-cluster.chart" . }}
{{ include "kommodity-cluster.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "kommodity-cluster.selectorLabels" -}}
app.kubernetes.io/name: {{ include "kommodity-cluster.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "kommodity-cluster.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "kommodity-cluster.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Compute sha256sum of parameters givens to the TalosConfigTemplate.
Any values that should trigger a new Talos config template when changed should be added to the hash computation.
*/}}
{{- define "kommodity-cluster.talosConfigHash" -}}
{{- $hasPoolConfigPatches := and .poolValues.configPatches (gt (len .poolValues.configPatches) 0) }}
{{- $hasGlobalConfigPatches := and .allValues.kommodity.global.configPatches (gt (len .allValues.kommodity.global.configPatches) 0) }}
{{- $data := dict -}}
{{- $configPatches := list -}}
{{- if $hasGlobalConfigPatches }}
	{{- $configPatches = concat $configPatches .allValues.kommodity.global.configPatches -}}
{{- end }}
{{- if $hasPoolConfigPatches }}
	{{- $configPatches = concat $configPatches .poolValues.configPatches -}}
{{- end }}
{{- if gt (len $configPatches) 0 }}
	{{- $_ := set $data "configPatches" $configPatches -}}
{{- end }}
{{- $talosVersion := default .allValues.talos.version (dig "talos" "version" "" .poolValues) -}}
{{- $_ := set $data "talosVersion" $talosVersion -}}

{{- $_ := set $data "kmsEnabled" .allValues.kommodity.kms.enabled -}}
{{- if .allValues.kommodity.kms.enabled -}}
	{{- with .allValues.kommodity.kms.endpoint -}}
		{{- $_ := set $data "kmsEndpoint" . -}}  
	{{- end -}}
{{- end -}}
{{- $_ := set $data "labels" (dig "labels" "" .poolValues) -}}
{{- $_ := set $data "annotations" (dig "annotations" "" .poolValues) -}}
{{- $_ := set $data "taints" (dig "taints" "" .poolValues) -}}
{{- toJson $data | sha256sum | trunc 6 -}}
{{- end -}}

{{/*
Compute sha256sum of parameters givens to the MachineTemplates.
Any values that should trigger a new Machine template when changed should be added to the hash computation.
*/}}
{{- define "kommodity-cluster.machineSpecsHash" -}}
{{- $data := dict -}}
{{- $talosImageName := default .allValues.talos.imageName (dig "talos" "imageName" "" .poolValues) -}}
{{- $_ := set $data "talosImageName" $talosImageName -}}
{{- $_ := set $data "sku" .poolValues.sku -}}
{{- $_ := set $data "diskSize" (dig "os" "disk" "size" "" .poolValues) -}}
{{- $_ := set $data "publicNetworkEnabled" .allValues.kommodity.network.ipv4.public -}}
{{- toJson $data | sha256sum | trunc 6 -}}
{{- end -}}

{{/*
Build combined config patches from global config patches, nodepool config patches, taints, labels, and annotations.
Patches with the same op and path are merged together.
*/}}
{{- define "kommodity-cluster.combinedConfigPatches" -}}
{{- $patches := dict -}}
{{- /* Collect global config patches */ -}}
{{- range .globalConfigPatches -}}
{{- $key := printf "%s:%s" .op .path -}}
{{- if hasKey $patches $key -}}
{{- $existing := get $patches $key -}}
{{- $_ := set $existing "value" (merge (get $existing "value") .value) -}}
{{- else -}}
{{- $_ := set $patches $key (dict "op" .op "path" .path "value" .value) -}}
{{- end -}}
{{- end -}}
{{- /* Collect nodepool config patches */ -}}
{{- range .nodepoolConfigPatches -}}
{{- $key := printf "%s:%s" .op .path -}}
{{- if hasKey $patches $key -}}
{{- $existing := get $patches $key -}}
{{- $_ := set $existing "value" (merge (get $existing "value") .value) -}}
{{- else -}}
{{- $_ := set $patches $key (dict "op" .op "path" .path "value" .value) -}}
{{- end -}}
{{- end -}}
{{- /* Add taints patches */ -}}
{{- $taints := .taints -}}
{{- if and $taints (gt (len $taints) 0) -}}
{{- /* Build register-with-taints for kubelet extraArgs */ -}}
{{- $taintStrings := list -}}
{{- range $key, $value := $taints -}}
{{- $taintStrings = append $taintStrings (printf "%s=%s" $key $value) -}}
{{- end -}}
{{- $kubeletKey := "add:/machine/kubelet/extraArgs" -}}
{{- if hasKey $patches $kubeletKey -}}
{{- $existing := get $patches $kubeletKey -}}
{{- $_ := set (get $existing "value") "register-with-taints" (join "," $taintStrings) -}}
{{- else -}}
{{- $_ := set $patches $kubeletKey (dict "op" "add" "path" "/machine/kubelet/extraArgs" "value" (dict "register-with-taints" (join "," $taintStrings))) -}}
{{- end -}}
{{- /* Build nodeTaints */ -}}
{{- $nodeTaintsKey := "add:/machine/nodeTaints" -}}
{{- $nodeTaintsValue := dict -}}
{{- range $key, $value := $taints -}}
{{- $_ := set $nodeTaintsValue $key $value -}}
{{- end -}}
{{- if hasKey $patches $nodeTaintsKey -}}
{{- $existing := get $patches $nodeTaintsKey -}}
{{- $_ := set $existing "value" (merge (get $existing "value") $nodeTaintsValue) -}}
{{- else -}}
{{- $_ := set $patches $nodeTaintsKey (dict "op" "add" "path" "/machine/nodeTaints" "value" $nodeTaintsValue) -}}
{{- end -}}
{{- end -}}
{{- /* Add labels patch */ -}}
{{- $labels := .labels -}}
{{- if and $labels (gt (len $labels) 0) -}}
{{- $labelsKey := "add:/machine/nodeLabels" -}}
{{- if hasKey $patches $labelsKey -}}
{{- $existing := get $patches $labelsKey -}}
{{- $_ := set $existing "value" (merge (get $existing "value") $labels) -}}
{{- else -}}
{{- $_ := set $patches $labelsKey (dict "op" "add" "path" "/machine/nodeLabels" "value" $labels) -}}
{{- end -}}
{{- end -}}
{{- /* Add annotations patch */ -}}
{{- $annotations := .annotations -}}
{{- if and $annotations (gt (len $annotations) 0) -}}
{{- $annotationsKey := "add:/machine/nodeAnnotations" -}}
{{- if hasKey $patches $annotationsKey -}}
{{- $existing := get $patches $annotationsKey -}}
{{- $_ := set $existing "value" (merge (get $existing "value") $annotations) -}}
{{- else -}}
{{- $_ := set $patches $annotationsKey (dict "op" "add" "path" "/machine/nodeAnnotations" "value" $annotations) -}}
{{- end -}}
{{- end -}}
{{- /* Output all combined patches */ -}}
{{- range $key, $patch := $patches }}
- op: {{ $patch.op }}
  path: {{ $patch.path }}
  value:
{{ $patch.value | toYaml | indent 4 }}
{{- end -}}
{{- end -}}
