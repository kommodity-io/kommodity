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
kommodity.kubevirt.affinity — render the inner of an `affinity` block (without the
wrapping `affinity:` key) for a KubevirtMachineTemplate VM spec. It combines:

  - nodeAffinity (required) pinning virt-launcher pods to the given zones via the
    standard `topology.kubernetes.io/zone` label on infra-cluster nodes. Omitted
    when the zones list is empty.
  - podAntiAffinity (required) spreading virt-launcher pods of the same pool across
    distinct infra-cluster hosts, using the `kubernetes.io/hostname` topology key and
    a shared `kommodity.io/nodepool` label that the caller must also set on the VMI
    template metadata. Hard required: if a pool has more replicas than available hosts,
    the excess pods stay Pending. Omitted when spreadAcrossHosts is false.

KubeVirt provider context: CAPK ignores Machine.Spec.FailureDomain, so the chart's
per-zone MachineDeployment fan-out does not by itself pin VMs to zones. Injecting
this affinity on the KubevirtMachineTemplate's VM spec actually constrains where
virt-launcher pods land on the infra cluster.

Usage: {{ include "kommodity.kubevirt.affinity" (dict "zones" (list "fr-par-1" "fr-par-2") "nodepool" "mycluster-default" "spreadAcrossHosts" true) }}
*/}}
{{- define "kommodity.kubevirt.affinity" -}}
{{- $zones := .zones | default list -}}
{{- $nodepool := .nodepool -}}
{{- $spread := .spreadAcrossHosts | default false -}}
{{- if $zones }}
nodeAffinity:
  requiredDuringSchedulingIgnoredDuringExecution:
    nodeSelectorTerms:
      - matchExpressions:
          - key: topology.kubernetes.io/zone
            operator: In
            values:
              {{- range $zones }}
              - {{ . | quote }}
              {{- end }}
{{- end }}
{{- if and $nodepool $spread }}
podAntiAffinity:
  requiredDuringSchedulingIgnoredDuringExecution:
    - labelSelector:
        matchLabels:
          kommodity.io/nodepool: {{ $nodepool | quote }}
      topologyKey: kubernetes.io/hostname
{{- end }}
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
Compute a short hash of an addon's initialExtraValues.
Returns empty string when initialExtraValues is not set, so the Job name
is unaffected for addons without extra values.
Usage: {{ include "kommodity.addon.valuesHash" .addon }}
*/}}
{{- define "kommodity.addon.valuesHash" -}}
{{- if .initialExtraValues }}
{{- toYaml .initialExtraValues | sha256sum | trunc 8 -}}
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
{{- with (dig "instanceVolumes" "" .poolValues) -}}
	{{- $_ := set $data "instanceVolumes" . -}}
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
{{- $disk := default (dict) (dig "os" "disk" (dict) .poolValues) -}}
{{- $_ := set $data "diskSize" (dig "size" "" $disk) -}}
{{- $_ := set $data "gpus" (dig "gpus" "" .poolValues) -}}
{{- if and (eq .allValues.kommodity.provider.name "Azure") (hasKey .poolValues "acceleratedNetworking") -}}
{{- $_ := set $data "acceleratedNetworking" .poolValues.acceleratedNetworking -}}
{{- end -}}
{{- with (dig "additionalVolumes" "" .poolValues) -}}
	{{- $_ := set $data "additionalVolumes" . -}}
{{- end -}}
{{- if eq .allValues.kommodity.provider.name "Kubevirt" -}}
{{- $effectiveEvictionStrategy := default "LiveMigrateIfPossible" (dig "evictionStrategy" "" (default dict .allValues.kommodity.provider.config)) -}}
{{- $_ := set $data "evictionStrategy" $effectiveEvictionStrategy -}}
{{- if (dig "spreadAcrossHosts" false .poolValues) -}}
{{- $_ := set $data "spreadAcrossHosts" true -}}
{{- end -}}
{{- end -}}
{{- /* Byot: the ByotMachineTemplate spec is a hostSelector derived from
     resources/os.disk plus the operator freeform hostSelector. Any of these
     changing must roll the template (CAPI infra templates are immutable),
     so hash the merged selector inputs. byot.io/available is constant and
     omitted from the hash. */ -}}
{{- if eq .allValues.kommodity.provider.name "Byot" -}}
{{- $disk := default (dict) (dig "os" "disk" (dict) .poolValues) -}}
{{- $_ := set $data "diskType" (dig "type" "" $disk) -}}
{{- $_ := set $data "diskSize" (dig "size" "" $disk) -}}
{{- $_ := set $data "byotHostSelector" (dig "hostSelector" "" .poolValues) -}}
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
name). When `resourceGroup` is omitted from values it defaults to the release
name (see cluster.yaml). This template catches the case where it is set
explicitly to a different value — a copied-but-unedited values file — and
rejects it at `helm install`/`template` time, before anything is provisioned.

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

{{/*
kommodity-cluster.byotHostSelector — build the ByotMachineTemplate
spec.template.spec.hostSelector block for a byot pool (controlplane or nodepool).

byot claims ByotHosts from the registry by label selector. The chart derives
the selector from a SKU-style `resources` + `os.disk` block, mapping each field
to a curated byot.io/ label (exact-match against the controller-promoted
ByotHost labels), and force-injects `byot.io/available: "true"` (a derived index
the controller honors regardless). An optional operator `hostSelector` escape
hatch (full LabelSelector) is merged: operator freeform matchLabels win over
derived labels on key conflict, and matchExpressions pass through verbatim.
`byot.io/available` is always forced last and cannot be overridden.

All four hardware fields (resources.cpu, resources.memory, os.disk.type,
os.disk.size) map to curated byot.io/ labels. resources.cpu and resources.memory
are always required; os.disk.type and os.disk.size are optional (omit both
for a disk-agnostic selector that matches diskless hosts like Talos-in-Docker,
or set both to pin a disk type+class). When set, each field is validated against
its curated value set. Exact-match semantics: a value no host ever carries
matches nothing, so invalid values fail the template rather than silently
sticking Machines.

Input: dict "poolValues" (controlplane/nodepool values) "scope" (value path
for error messages, e.g. "kommodity.controlplane").
Returns the `hostSelector:` YAML block (empty matchLabels omitted), indented
by the caller.
*/}}
{{- define "kommodity-cluster.byotHostSelector" -}}
{{- $p := .poolValues -}}
{{- $scope := .scope -}}
{{- $labels := dict -}}
{{- /* Operator freeform escape hatch is the base; it wins over derived labels
     on key conflict, so each derived label is only set when the operator
     has not already provided it. */ -}}
{{- with (dig "hostSelector" "matchLabels" (dict) $p) -}}
{{- $labels = mustMergeOverwrite $labels (deepCopy .) -}}
{{- end -}}
{{- /* resources.cpu -> byot.io/cpu-cores (plain integer string). */ -}}
{{- $cpu := dig "resources" "cpu" "" $p -}}
{{- if not $cpu -}}
{{- fail (printf "%s.resources.cpu is required for Byot (plain integer string of physical cores, e.g. \"4\")" $scope) -}}
{{- end -}}
{{- if not (regexMatch "^[0-9]+$" $cpu) -}}
{{- fail (printf "%s.resources.cpu must be a plain integer string of physical cores (e.g. \"4\"), got %q; byot.io/cpu-cores is not bucketed and not a k8s quantity (\"4000m\" is rejected)" $scope $cpu) -}}
{{- end -}}
{{- if not (hasKey $labels "byot.io/cpu-cores") -}}{{- $_ := set $labels "byot.io/cpu-cores" $cpu -}}{{- end -}}
{{- /* resources.memory -> byot.io/memory-class (exact bucket). */ -}}
{{- $memory := dig "resources" "memory" "" $p -}}
{{- if not $memory -}}
{{- fail (printf "%s.resources.memory is required for Byot (one of: 4G 8G 16G 32G 64G 128G)" $scope) -}}
{{- end -}}
{{- $memoryBuckets := list "4G" "8G" "16G" "32G" "64G" "128G" -}}
{{- if not (has $memory $memoryBuckets) -}}
{{- fail (printf "%s.resources.memory must be one of 4G 8G 16G 32G 64G 128G (exact bucket matching the controller-promoted byot.io/memory-class label), got %q" $scope $memory) -}}
{{- end -}}
{{- if not (hasKey $labels "byot.io/memory-class") -}}{{- $_ := set $labels "byot.io/memory-class" $memory -}}{{- end -}}
{{- /* os.disk is OPTIONAL. Some hosts (e.g. Talos-in-Docker, diskless
     boot) promote no byot.io/disk-* labels, so requiring a disk selector
     would make them unclaimable. When either os.disk.type or os.disk.size is
     set, BOTH must be set and valid (a disk claim is type+class together);
     when neither is set, the selector omits byot.io/disk-* and matches any
     host regardless of disk. Operator freeform hostSelector labels can still
     pin a disk if needed. */ -}}
{{- $disk := default (dict) (dig "os" "disk" (dict) $p) -}}
{{- $diskType := dig "type" "" $disk -}}
{{- $diskSize := dig "size" "" $disk -}}
{{- $diskTypes := list "nvme" "ssd" "hdd" "sd" -}}
{{- $diskClasses := list "20G" "100G" "250G" "500G" "1T" -}}
{{- if or $diskType $diskSize -}}
{{- if not $diskType -}}
{{- fail (printf "%s.os.disk.type is required when os.disk.size is set (one of: nvme ssd hdd sd); omit both for a disk-agnostic selector" $scope) -}}
{{- end -}}
{{- if not (has $diskType $diskTypes) -}}
{{- fail (printf "%s.os.disk.type must be one of nvme ssd hdd sd (lowercase, matching the controller-promoted byot.io/disk-type label), got %q" $scope $diskType) -}}
{{- end -}}
{{- if not $diskSize -}}
{{- fail (printf "%s.os.disk.size is required when os.disk.type is set (one of: 20G 100G 250G 500G 1T); omit both for a disk-agnostic selector" $scope) -}}
{{- end -}}
{{- if not (has $diskSize $diskClasses) -}}
{{- fail (printf "%s.os.disk.size must be one of 20G 100G 250G 500G 1T (exact bucket matching the controller-promoted byot.io/disk-class label), got %q" $scope $diskSize) -}}
{{- end -}}
{{- if not (hasKey $labels "byot.io/disk-type") -}}{{- $_ := set $labels "byot.io/disk-type" $diskType -}}{{- end -}}
{{- if not (hasKey $labels "byot.io/disk-class") -}}{{- $_ := set $labels "byot.io/disk-class" $diskSize -}}{{- end -}}
{{- end -}}
{{- /* byot.io/available is force-injected last; it cannot be overridden. */ -}}
{{- $_ := set $labels "byot.io/available" "true" -}}
hostSelector:
  matchLabels:
{{- $labels | toYaml | nindent 4 }}
{{- with (dig "hostSelector" "matchExpressions" (list) .poolValues) }}
{{- if gt (len .) 0 }}
  matchExpressions:
{{- . | toYaml | nindent 4 }}
{{- end }}
{{- end }}
{{- end -}}
