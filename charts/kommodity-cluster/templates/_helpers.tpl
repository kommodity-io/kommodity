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
{{- $hasPoolPatches := and .poolValues.strategicPatches (gt (len .poolValues.strategicPatches) 0) }}
{{- $hasGlobalPatches := and .allValues.kommodity.global.strategicPatches (gt (len .allValues.kommodity.global.strategicPatches) 0) }}
{{- $data := dict -}}
{{- $patches := list -}}
{{- if $hasGlobalPatches }}
	{{- $patches = concat $patches .allValues.kommodity.global.strategicPatches -}}
{{- end }}
{{- if $hasPoolPatches }}
	{{- $patches = concat $patches .poolValues.strategicPatches -}}
{{- end }}
{{- if gt (len $patches) 0 }}
	{{- $_ := set $data "strategicPatches" $patches -}}
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
{{- $_ := set $data "gpus" (dig "gpus" "" .poolValues) -}}
{{- $_ := set $data "publicNetworkEnabled" .allValues.kommodity.network.ipv4.public -}}
{{- toJson $data | sha256sum | trunc 6 -}}
{{- end -}}

{{/*
Build a merged strategic patch from all configuration sources.
Returns a YAML block scalar list item (- |\n  <yaml>) representing a single MachineConfig strategic patch.
Returns empty string if there are no patches to apply.
User patches from nodepools override global patches for same keys via deep merge.
Note: /cluster/inlineManifests are handled separately in controlplane.yaml
to preserve YAML block scalar formatting for multi-line contents.
*/}}
{{- define "kommodity-cluster.mergedStrategicPatch" -}}
{{- $result := dict -}}
{{- /* Add labels */ -}}
{{- if and .labels (gt (len .labels) 0) -}}
{{- $_ :=   $result (dict "machine" (dict "nodeLabels" (deepCopy .labels))) -}}
{{- end -}}
{{- /* Add annotations */ -}}
{{- if and .annotations (gt (len .annotations) 0) -}}
{{- $_ := mustMergeOverwrite $result (dict "machine" (dict "nodeAnnotations" (deepCopy .annotations))) -}}
{{- end -}}
{{- /* Add taints */ -}}
{{- if and .taints (gt (len .taints) 0) -}}
{{- $taintStrings := list -}}
{{- range $key, $value := .taints -}}
{{- $taintStrings = append $taintStrings (printf "%s=%s" $key $value) -}}
{{- end -}}
{{- $_ := mustMergeOverwrite $result (dict "machine" (dict "kubelet" (dict "extraArgs" (dict "register-with-taints" (join "," $taintStrings))))) -}}
{{- $_ := mustMergeOverwrite $result (dict "machine" (dict "nodeTaints" (deepCopy .taints))) -}}
{{- end -}}
{{- /* Add OIDC apiServer extraArgs */ -}}
{{- if and .oidc .oidc.enabled -}}
{{- $oidcExtraArgs := include "kommodity.talos.oidc.extraArgs" (dict "oidc" .oidc) | fromJson -}}
{{- $_ := mustMergeOverwrite $result (dict "cluster" (dict "apiServer" (dict "extraArgs" $oidcExtraArgs))) -}}
{{- end -}}
{{- /* Add installer image */ -}}
{{- if .installer -}}
{{- $installerImage := include "kommodity.talos.installer.image" (dict "installer" .installer) -}}
{{- $_ := mustMergeOverwrite $result (dict "machine" (dict "install" (dict "image" $installerImage))) -}}
{{- end -}}
{{- /* Add global Kommodity environment variables */ -}}
{{- if .logLevel -}}
{{- $globalEnv := include "kommodity.talos.globalEnv" (dict "logLevel" .logLevel) | fromJson -}}
{{- $_ := mustMergeOverwrite $result (dict "machine" (dict "env" $globalEnv)) -}}
{{- end -}}
{{- /* Disable CNI (controlplane only) */ -}}
{{- if .disableCNI -}}
{{- $_ := mustMergeOverwrite $result (include "kommodity.talos.cni.disable" . | fromJson) -}}
{{- end -}}
{{- /* Disable proxy (controlplane only) */ -}}
{{- if .disableProxy -}}
{{- $_ := mustMergeOverwrite $result (include "kommodity.talos.proxy.disable" . | fromJson) -}}
{{- end -}}
{{- /* Cloud Controller Manager (controlplane only) */ -}}
{{- if .ccmEnabled -}}
{{- $_ := mustMergeOverwrite $result (include "kommodity.talos.ccm" (dict "manifest" .ccmManifest) | fromJson) -}}
{{- end -}}
{{- /* Merge global user patches */ -}}
{{- range .globalPatches -}}
{{- $_ := mustMergeOverwrite $result (deepCopy .) -}}
{{- end -}}
{{- /* Merge nodepool user patches (override global for same keys) */ -}}
{{- range .nodepoolPatches -}}
{{- $_ := mustMergeOverwrite $result (deepCopy .) -}}
{{- end -}}
{{- /* Output as YAML block scalar if non-empty */ -}}
{{- if gt (len (keys $result)) 0 -}}
- |
{{ $result | toYaml | indent 2 }}
{{- end -}}
{{- end -}}
