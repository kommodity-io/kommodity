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
Resolve the failure domains for a pool (nodepool or controlplane) from the `zones` list.
Returns the zones as a JSON array string; decode with `fromJsonArray`.
Usage: {{ $zones := include "kommodity-cluster.poolZones" $np | fromJsonArray }}
*/}}
{{- define "kommodity-cluster.poolZones" -}}
{{- if hasKey . "zone" }}
{{- fail "singular 'zone' is deprecated; use plural 'zones' list instead" -}}
{{- end -}}
{{- $zones := list -}}
{{- range (.zones | default list) -}}
{{- $zones = append $zones . -}}
{{- end -}}
{{- $zones | uniq | toJson -}}
{{- end -}}

{{/*
Resolve the control-plane failure domains from controlplane.zones. These populate the
cluster's failureDomains, which the control plane uses to place its replicas. Optional: when
unset, no failureDomains are set and the provider uses its default zone (for Scaleway, the
zone from the credentials secret). Returns the zones as a JSON array string.
Usage: {{ $zones := include "kommodity-cluster.controlPlaneZones" . | fromJsonArray }}
*/}}
{{- define "kommodity-cluster.controlPlaneZones" -}}
{{- include "kommodity-cluster.poolZones" .Values.kommodity.controlplane -}}
{{- end -}}

{{/*
kommodity.kubevirt.nodeAffinity — render a `nodeAffinity` YAML block that restricts
VM scheduling to the given zones via the standard `topology.kubernetes.io/zone` label
on infra-cluster nodes. Returns empty when the zones list is empty.

KubeVirt provider context: CAPK ignores Machine.Spec.FailureDomain, so the chart's
per-zone MachineDeployment fan-out does not by itself pin VMs to zones. Injecting
this affinity on the KubevirtMachineTemplate's VM spec actually constrains where
virt-launcher pods land on the infra cluster.

Usage: {{ include "kommodity.kubevirt.nodeAffinity" (list "fr-par-1" "fr-par-2") }}
*/}}
{{- define "kommodity.kubevirt.nodeAffinity" -}}
{{- if gt (len .) 0 -}}
nodeAffinity:
  requiredDuringSchedulingIgnoredDuringExecution:
    nodeSelectorTerms:
      - matchExpressions:
          - key: topology.kubernetes.io/zone
            operator: In
            values:
              {{- range . }}
              - {{ . | quote }}
              {{- end }}
{{- end -}}
{{- end -}}

{{/*
Compute one zone's share when splitting a total count evenly across zones.
The remainder is front-loaded, so lower indices receive the extra units
(e.g. total 5 over 2 zones -> index 0 gets 3, index 1 gets 2).
Usage: {{ include "kommodity-cluster.zoneShare" (dict "total" 6 "count" 2 "index" 0) }}
*/}}
{{- define "kommodity-cluster.zoneShare" -}}
{{- $base := div .total .count -}}
{{- $extra := mod .total .count -}}
{{- if lt .index $extra -}}
{{- add $base 1 -}}
{{- else -}}
{{- $base -}}
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
{{- $patches := dict -}}
{{- if $hasGlobalPatches }}
	{{- $patches = mustMergeOverwrite $patches (deepCopy .allValues.kommodity.global.strategicPatches) -}}
{{- end }}
{{- if $hasPoolPatches }}
	{{- $patches = mustMergeOverwrite $patches (deepCopy .poolValues.strategicPatches) -}}
{{- end }}
{{- if gt (len $patches) 0 }}
	{{- $_ := set $data "strategicPatches" $patches -}}
{{- end }}
{{- $talosVersion := default .allValues.talos.version (dig "talos" "version" "" .poolValues) -}}
{{- $_ := set $data "talosVersion" $talosVersion -}}

{{- /* CCM enabled drives auto-injection of kubelet --cloud-provider=external
       (see mergedStrategicPatch); include it so toggling CCM rolls the nodes. */ -}}
{{- $_ := set $data "ccmEnabled" (dig "provider" "cloudControllerManager" "enabled" false .allValues.kommodity) -}}
{{- $_ := set $data "kmsEnabled" .allValues.kommodity.kms.enabled -}}
{{- if .allValues.kommodity.kms.enabled -}}
	{{- with .allValues.kommodity.kms.endpoint -}}
		{{- $_ := set $data "kmsEndpoint" . -}}
	{{- end -}}
{{- end -}}
{{- $_ := set $data "labels" (dig "labels" "" .poolValues) -}}
{{- $_ := set $data "annotations" (dig "annotations" "" .poolValues) -}}
{{- $_ := set $data "taints" (dig "taints" "" .poolValues) -}}
{{- with (dig "additionalVolumes" "" .poolValues) -}}
	{{- $_ := set $data "additionalVolumes" . -}}
{{- end -}}
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
{{- with (dig "resources" "" .poolValues) -}}
{{- $_ := set $data "resources" . -}}
{{- end -}}
{{- $_ := set $data "diskSize" (dig "os" "disk" "size" "" .poolValues) -}}
{{- $_ := set $data "gpus" (dig "gpus" "" .poolValues) -}}
{{- with (dig "additionalVolumes" "" .poolValues) -}}
	{{- $_ := set $data "additionalVolumes" . -}}
{{- end -}}
{{- $_ := set $data "publicNetworkEnabled" .allValues.kommodity.network.ipv4.public -}}
{{- $zones := include "kommodity-cluster.poolZones" .poolValues | fromJsonArray -}}
{{- if gt (len $zones) 0 -}}
{{- $_ := set $data "zones" $zones -}}
{{- end -}}
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
{{- $_ := mustMergeOverwrite $result (dict "machine" (dict "nodeLabels" (deepCopy .labels))) -}}
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
{{- /*
  Cloud Controller Manager → kubelet --cloud-provider=external.

  When a CCM is enabled the kubelet MUST run with --cloud-provider=external so
  the node registers with the node.cloudprovider.kubernetes.io/uninitialized
  taint and defers spec.providerID assignment to the CCM. Without it the node
  comes up with NO providerID; CAPI links Machine→Node by matching providerID,
  so Machine.status.nodeRef is never set, and CACPPT's control-plane scale-down
  refuses to remove any machine while one lacks a NodeRef — wedging every
  subsequent rollout. Auto-injected here (not left to global.strategicPatches)
  so it can never be omitted by mistake. An explicit user patch for
  cloud-provider still wins, as user patches are merged on top below.
*/ -}}
{{- if .ccmEnabled -}}
{{- $_ := mustMergeOverwrite $result (dict "machine" (dict "kubelet" (dict "extraArgs" (dict "cloud-provider" "external")))) -}}
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
{{- /* Merge global user patch (single MachineConfig dict) */ -}}
{{- if and .globalPatches (gt (len .globalPatches) 0) -}}
{{- $_ := mustMergeOverwrite $result (deepCopy .globalPatches) -}}
{{- end -}}
{{- /* Merge nodepool/controlplane user patch on top of global (wins on conflicts) */ -}}
{{- if and .nodepoolPatches (gt (len .nodepoolPatches) 0) -}}
{{- $_ := mustMergeOverwrite $result (deepCopy .nodepoolPatches) -}}
{{- end -}}
{{- /* Output as YAML block scalar if non-empty */ -}}
{{- if gt (len (keys $result)) 0 -}}
- |
{{ $result | toYaml | indent 2 }}
{{- end -}}
{{- end -}}

{{/*
kommodity.azure.validateNaming — fail fast on the copy-paste footgun.

A common mistake is copying a values file for one cluster to another and
forgetting to update the cluster-scoped Azure identifiers. The Helm release name
changes (so the AzureCluster/Cluster names differ), but the resource group is
carried over verbatim. The duplicate then silently shares the original's resource
group (and, in BYO-VNet mode, its VNet — overlapping subnet CIDRs), corrupting
both rather than failing fast.

Convention: the Azure resource group is named after the cluster (== release
name). This template enforces that convention so a copied-but-unedited values
file is rejected at `helm install`/`template` time — before anything is
provisioned, when it is trivially removable.

(The CCM Secret collision is guarded independently on the management plane: the
credential materializer refuses to take over a Secret owned by another cluster —
see ErrSecretOwnedByAnotherCluster — so this template intentionally does not
constrain provider.secret.name, which has a legitimate custom-override use case.)

Set kommodity.provider.config.allowSharedResourceGroup: true to intentionally
place multiple clusters in one resource group (you are then responsible for
non-colliding resource names and CIDRs).

Usage: {{ include "kommodity.azure.validateNaming" . }}
*/}}
{{- define "kommodity.azure.validateNaming" -}}
{{- if eq .Values.kommodity.provider.name "Azure" -}}
{{- if not (dig "config" "allowSharedResourceGroup" false .Values.kommodity.provider) -}}
{{- $rg := dig "config" "resourceGroup" "" .Values.kommodity.provider -}}
{{- if and $rg (ne $rg .Release.Name) -}}
{{- fail (printf "Azure resourceGroup %q does not match the Helm release name %q. This usually means a values file was copied from another cluster without updating kommodity.provider.config.resourceGroup, which would make this release share the other cluster's resource group and corrupt both. Rename the resource group to %q, or set kommodity.provider.config.allowSharedResourceGroup=true to intentionally share one." $rg .Release.Name .Release.Name) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
kommodity.azure.image — render the AzureMachineTemplate spec.template.spec.image
block. Mirrors the Scaleway model: provide just talos.imageName and the full
managed-image ARM ID is assembled from the subscription + image resource group, so
you only supply the last segment of the resource ID rather than the whole thing.

Precedence (first match wins):
  1. talos.marketplace      — Azure Marketplace image
  2. talos.computeGallery   — Shared Image Gallery
  3. talos.id               — explicit full ARM resource ID (escape hatch)
  4. talos.imageName        — managed image; ARM ID built from
                              kommodity.provider.config.subscriptionID +
                              kommodity.provider.config.talosImageResourceGroup

Usage (after an `image:` key): {{- include "kommodity.azure.image" . | nindent 8 }}
*/}}
{{- define "kommodity.azure.image" -}}
{{- $talos := .Values.talos -}}
{{- if dig "marketplace" "" $talos -}}
marketplace:
  publisher: {{ $talos.marketplace.publisher }}
  offer: {{ $talos.marketplace.offer }}
  sku: {{ $talos.marketplace.sku }}
  version: {{ $talos.marketplace.version }}
{{- else if dig "computeGallery" "" $talos -}}
computeGallery:
  gallery: {{ $talos.computeGallery.gallery }}
  name: {{ $talos.computeGallery.name }}
  version: {{ $talos.computeGallery.version }}
  {{- if dig "computeGallery" "subscriptionID" "" $talos }}
  subscriptionID: {{ $talos.computeGallery.subscriptionID }}
  {{- end }}
  {{- if dig "computeGallery" "resourceGroup" "" $talos }}
  resourceGroup: {{ $talos.computeGallery.resourceGroup }}
  {{- end }}
{{- else if dig "id" "" $talos -}}
id: {{ $talos.id }}
{{- else if dig "imageName" "" $talos -}}
{{- $subID := required "talos.imageName requires kommodity.provider.config.subscriptionID to build the Talos image resource ID" (dig "config" "subscriptionID" "" .Values.kommodity.provider) -}}
{{- $imageRG := required "talos.imageName requires kommodity.provider.config.talosImageResourceGroup (the resource group holding the Talos managed image) to build the Talos image resource ID" (dig "config" "talosImageResourceGroup" "" .Values.kommodity.provider) -}}
id: {{ printf "/subscriptions/%s/resourceGroups/%s/providers/Microsoft.Compute/images/%s" $subID $imageRG $talos.imageName }}
{{- else -}}
{{- fail "no Talos image configured for Azure: set talos.imageName (recommended) together with kommodity.provider.config.talosImageResourceGroup, or use talos.id / talos.computeGallery / talos.marketplace" -}}
{{- end -}}
{{- end -}}
