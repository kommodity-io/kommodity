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
Patches targeting the same path are merged together.
*/}}
{{- define "kommodity-cluster.combinedConfigPatches" -}}
{{- $patchesByPath := dict -}}
{{- /* Collect global config patches */ -}}
{{- range .globalConfigPatches -}}
{{- if hasKey $patchesByPath .path -}}
{{- $_ := set $patchesByPath .path (merge (get $patchesByPath .path) .value) -}}
{{- else -}}
{{- $_ := set $patchesByPath .path .value -}}
{{- end -}}
{{- end -}}
{{- /* Collect nodepool config patches */ -}}
{{- range .nodepoolConfigPatches -}}
{{- if hasKey $patchesByPath .path -}}
{{- $_ := set $patchesByPath .path (merge (get $patchesByPath .path) .value) -}}
{{- else -}}
{{- $_ := set $patchesByPath .path .value -}}
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
{{- $kubeletPath := "/machine/kubelet/extraArgs" -}}
{{- if hasKey $patchesByPath $kubeletPath -}}
{{- $_ := set (get $patchesByPath $kubeletPath) "register-with-taints" (join "," $taintStrings) -}}
{{- else -}}
{{- $_ := set $patchesByPath $kubeletPath (dict "register-with-taints" (join "," $taintStrings)) -}}
{{- end -}}
{{- /* Build nodeTaints */ -}}
{{- $nodeTaintsPath := "/machine/nodeTaints" -}}
{{- $nodeTaintsValue := dict -}}
{{- range $key, $value := $taints -}}
{{- $_ := set $nodeTaintsValue $key $value -}}
{{- end -}}
{{- if hasKey $patchesByPath $nodeTaintsPath -}}
{{- $_ := set $patchesByPath $nodeTaintsPath (merge (get $patchesByPath $nodeTaintsPath) $nodeTaintsValue) -}}
{{- else -}}
{{- $_ := set $patchesByPath $nodeTaintsPath $nodeTaintsValue -}}
{{- end -}}
{{- end -}}
{{- /* Add labels patch */ -}}
{{- $labels := .labels -}}
{{- if and $labels (gt (len $labels) 0) -}}
{{- $labelsPath := "/machine/nodeLabels" -}}
{{- if hasKey $patchesByPath $labelsPath -}}
{{- $_ := set $patchesByPath $labelsPath (merge (get $patchesByPath $labelsPath) $labels) -}}
{{- else -}}
{{- $_ := set $patchesByPath $labelsPath $labels -}}
{{- end -}}
{{- end -}}
{{- /* Add annotations patch */ -}}
{{- $annotations := .annotations -}}
{{- if and $annotations (gt (len $annotations) 0) -}}
{{- $annotationsPath := "/machine/nodeAnnotations" -}}
{{- if hasKey $patchesByPath $annotationsPath -}}
{{- $_ := set $patchesByPath $annotationsPath (merge (get $patchesByPath $annotationsPath) $annotations) -}}
{{- else -}}
{{- $_ := set $patchesByPath $annotationsPath $annotations -}}
{{- end -}}
{{- end -}}
{{- /* Output all combined patches */ -}}
{{- range $path, $value := $patchesByPath }}
- op: add
  path: {{ $path }}
  value:
    {{- range $k, $v := $value }}
    {{ $k }}: {{ $v | quote }}
    {{- end }}
{{- end -}}
{{- end -}}
