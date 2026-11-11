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
Workers never receive control-plane-only documents (KubeAPIServerConfig,
KubeAuthenticationConfig) — they are dropped from the hash so changing only
these does not roll every worker's bootstrap configRef.
*/}}
{{- define "kommodity-cluster.talosConfigHash" -}}
{{- $data := dict -}}
{{- /* Global and pool patches are hashed separately: merging them into one
     dict would replace list-kind entries (KernelModuleConfig,
     CRICustomizationConfig) wholesale, silently dropping global entries the
     renderer retains — a change to those would then not roll the workers. */ -}}
{{- /* Workers never receive control-plane-only documents (KubeAPIServerConfig,
     KubeAuthenticationConfig) — dropped so changing only these does not roll
     every worker's bootstrap configRef. */ -}}
{{- if and .allValues.kommodity.global.machineConfig (gt (len .allValues.kommodity.global.machineConfig) 0) -}}
{{- $_ := set $data "globalMachineConfig" (omit (deepCopy .allValues.kommodity.global.machineConfig) "KubeAPIServerConfig" "KubeAuthenticationConfig") -}}
{{- end -}}
{{- if and .poolValues.machineConfig (gt (len .poolValues.machineConfig) 0) -}}
{{- $_ := set $data "poolMachineConfig" (omit (deepCopy .poolValues.machineConfig) "KubeAPIServerConfig" "KubeAuthenticationConfig") -}}
{{- end -}}
{{- /* logLevel feeds LOG_LEVEL into every worker EnvironmentConfig */ -}}
{{- $_ := set $data "logLevel" (default "" (index (index .allValues "kommodity") "logLevel")) -}}
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
{{- with (dig "raidArrays" "" .poolValues) -}}
	{{- $_ := set $data "raidArrays" . -}}
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
{{- $os := default (dict) (dig "os" (dict) .poolValues) -}}
{{- $disk := default (dict) (dig "disk" (dict) $os) -}}
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
{{- $os := default (dict) (dig "os" (dict) .poolValues) -}}
{{- $disk := default (dict) (dig "disk" (dict) $os) -}}
{{- $_ := set $data "diskType" (dig "type" "" $disk) -}}
{{- $_ := set $data "diskSize" (dig "size" "" $disk) -}}
{{- $_ := set $data "byotHostSelector" (dig "hostSelector" "" .poolValues) -}}
{{- $_ := set $data "desiredTalosVersion" (include "kommodity-cluster.byotDesiredTalosVersion" (dict "poolValues" .poolValues "allValues" .allValues)) -}}
{{- end -}}
{{- /* Azure: the effective image identity must mint a new template name (infrastructure templates are immutable per CAPZ). Hash the identity sources, mirroring kommodity.azure.image's precedence (marketplace > computeGallery > id > imageName; imageName is hashed as talosImageName above, and the derived resource-ID reshaping via provider.config.talosImageResourceGroup is included so the rendered id rolls). Hashing the identity instead of the rendered block avoids duplicating the required-error paths in the hash evaluation. */ -}}
{{- if eq .allValues.kommodity.provider.name "Azure" -}}
{{- $talos := .allValues.talos -}}
{{- $_ := set $data "azureImageIdentity" (list (dig "marketplace" "version" "" $talos) (dig "computeGallery" "version" "" $talos) (dig "id" "" $talos) (dig "config" "talosImageResourceGroup" "" .allValues.kommodity.provider)) -}}
{{- end -}}
{{- if and (hasKey . "isControlPlane") .isControlPlane -}}
{{- $inlineManifests := include "kommodity.inlineManifests" (dict "addons" .root.Values.kommodity.addons "extraSecrets" .root.Values.kommodity.extraSecrets "root" .root) | trim -}}
{{- if not (empty $inlineManifests) -}}
{{- $_ := set $data "inlineManifests" $inlineManifests -}}
{{- end -}}
{{- end -}}
{{- $_ := set $data "publicNetworkEnabled" .allValues.kommodity.network.ipv4.public -}}
{{- $zones := include "kommodity-cluster.poolZones" .poolValues | fromJsonArray -}}
{{- if gt (len $zones) 0 -}}
{{- $_ := set $data "zones" $zones -}}
{{- end -}}
{{- toJson $data | sha256sum | trunc 6 -}}
{{- end -}}

{{/*
Build merged machine config for strategicPatches (Talos v1.14 multi-doc kinds).
Each returned block is one YAML document as a "- |" list item:
  KubeletConfig, KubeAPIServerConfig (control planes only),
  KubeAuthenticationConfig (control planes only), KubeNodeConfig,
  EnvironmentConfig, KubeProxyConfig (disableProxy), KubeFlannelCNIConfig
  with $patch: delete (disableCNI), one KernelModuleConfig per entry, one
  RegistryMirrorConfig per host, one RegistryAuthConfig per host,
  one RegistryTLSConfig per host, ResolverConfig, one
  CRICustomizationConfig per entry, then a v1alpha1 fragment, then
  extraDocuments appended verbatim. Returns empty string if nothing to emit.

machineConfig keys are Talos document names — KubeletConfig, KubeAPIServerConfig,
KubeAuthenticationConfig, KubeNodeConfig, EnvironmentConfig, KernelModuleConfig,
CRICustomizationConfig, RegistryMirrorConfig, RegistryAuthConfig,
RegistryTLSConfig, ResolverConfig — whose fields map 1:1 to the document schema (see
https://docs.siderolabs.com/talos/v1.14/getting-started/what's-new-in-talos#kubernetes-multi-document-configuration).
Registry-kind keys (RegistryMirrorConfig, RegistryAuthConfig,
RegistryTLSConfig) emit one document per registry host, keyed by the entry
"name" (duplicate names: nodepool replaces global), sorted by name for
deterministic output. List-kind keys (KernelModuleConfig, CRICustomizationConfig)
emit one document per entry. Any other top-level key (machine, cluster, ...)
is a v1alpha1 strategic patch fragment, emitted verbatim as escape hatch for
fields with no doc kind yet. extraDocuments is a raw multi-doc YAML string
split on --- and appended verbatim.

Precedence: built-in settings (LOG_LEVEL from kommodity.logLevel, CNI/proxy
disable documents, OIDC authentication config, pool-level labels/annotations/taints) are seeded first,
then user patches overlay them (nodepool wins over global).
*/}}

{{- define "kommodity-cluster.mergedMachineConfig" -}}
{{- /* EnvironmentConfig: LOG_LEVEL from kommodity.logLevel seeded first,
     user EnvironmentConfig.variables override it */ -}}
{{- $envConfig := dict -}}
{{- if .logLevel -}}{{- $_ := set $envConfig "variables" (dict "LOG_LEVEL" .logLevel) -}}{{- end -}}
{{- /* v1alpha1 fragment: built-in fields first, user patches override them */ -}}
{{- $merged := dict -}}
{{- /* KubeFlannelCNIConfig/KubeProxyConfig: cluster.network.cni and cluster.proxy
     are deprecated v1alpha1 paths in Talos v1.14; disabling is document-level:
     delete the auto-generated flannel doc, set enabled: false on kube-proxy.
     Control-plane only: worker configs are generated separately and never
     received these settings before either. */ -}}
{{- $flannelConfig := dict -}}
{{- if .disableCNI -}}
{{- $_ := set $flannelConfig "$patch" "delete" -}}
{{- end -}}
{{- $proxyConfig := dict -}}
{{- if .disableProxy -}}
{{- $_ := set $proxyConfig "enabled" false -}}
{{- end -}}

{{- /* KubeNodeConfig: pool-level labels/annotations/taints first, user patches override */ -}}
{{- $nodeConfig := dict -}}
{{- if and .labels (gt (len .labels) 0) -}}
{{- $_ := mustMergeOverwrite $nodeConfig (dict "labels" (deepCopy .labels)) -}}
{{- end -}}
{{- if and .annotations (gt (len .annotations) 0) -}}
{{- $_ := mustMergeOverwrite $nodeConfig (dict "annotations" (deepCopy .annotations)) -}}
{{- end -}}
{{- if and .taints (gt (len .taints) 0) -}}
{{- $_ := mustMergeOverwrite $nodeConfig (dict "taints" (deepCopy .taints)) -}}
{{- end -}}

{{- /* KubeAuthenticationConfig: Talos v1.14 rejects oidc and anonymous-auth
     args in KubeAPIServerConfig.extraArgs; auth is structured configuration */ -}}
{{- $authConfig := dict -}}
{{- if and .oidc .oidc.enabled -}}
{{- $_ := mustMergeOverwrite $authConfig (include "kommodity-cluster.oidcAuthenticationConfig" (dict "oidc" .oidc) | fromYaml) -}}
{{- end -}}
{{- $apiServerConfig := dict -}}

{{- /* Overlay user patches: global first, nodepool last (wins on conflicts) */ -}}
{{- $kubeletConfig := dict -}}
{{- $kernelModules := list -}}
{{- $criCustomizations := list -}}
{{- /* Registry docs are keyed by name: one document per registry host.
     Talos rejects duplicate names within a kind, so a name-keyed dict makes
     nodepool entries replace global ones on conflict. */ -}}
{{- $registryMirrors := dict -}}
{{- $registryAuthConfigs := dict -}}
{{- $registryTLSConfigs := dict -}}
{{- $resolverConfig := dict -}}
{{- $extraDocs := "" -}}
{{- range $patches := list .globalPatches .nodepoolPatches -}}
{{- if and $patches (gt (len $patches) 0) -}}
{{- range $key := keys $patches -}}
{{- $value := deepCopy (index $patches $key) -}}
{{- if eq $key "KubeletConfig" -}}
{{- $_ := mustMergeOverwrite $kubeletConfig $value -}}
{{- else if eq $key "KubeAPIServerConfig" -}}
{{- $_ := mustMergeOverwrite $apiServerConfig $value -}}
{{- else if eq $key "KubeAuthenticationConfig" -}}
{{- $_ := mustMergeOverwrite $authConfig $value -}}
{{- else if eq $key "KubeFlannelCNIConfig" -}}
{{- $_ := mustMergeOverwrite $flannelConfig $value -}}
{{- else if eq $key "KubeProxyConfig" -}}
{{- $_ := mustMergeOverwrite $proxyConfig $value -}}
{{- else if eq $key "KubeNodeConfig" -}}
{{- $_ := mustMergeOverwrite $nodeConfig $value -}}
{{- else if eq $key "EnvironmentConfig" -}}
{{- $_ := mustMergeOverwrite $envConfig $value -}}
{{- else if eq $key "KernelModuleConfig" -}}
{{- range $module := $value -}}{{- $kernelModules = mustAppend $kernelModules $module -}}{{- end -}}
{{- else if eq $key "CRICustomizationConfig" -}}
{{- range $entry := $value -}}{{- $criCustomizations = mustAppend $criCustomizations $entry -}}{{- end -}}
{{- else if eq $key "RegistryMirrorConfig" -}}
{{- range $entry := $value -}}{{- $_ := set $registryMirrors (dig "name" "" $entry) $entry -}}{{- end -}}
{{- else if eq $key "RegistryAuthConfig" -}}
{{- range $entry := $value -}}{{- $_ := set $registryAuthConfigs (dig "name" "" $entry) $entry -}}{{- end -}}
{{- else if eq $key "RegistryTLSConfig" -}}
{{- range $entry := $value -}}{{- $_ := set $registryTLSConfigs (dig "name" "" $entry) $entry -}}{{- end -}}
{{- else if eq $key "ResolverConfig" -}}
{{- $_ := mustMergeOverwrite $resolverConfig $value -}}
{{- else if eq $key "extraDocuments" -}}
{{- $extraDocs = $value -}}
{{- else -}}
{{- $_ := mustMergeOverwrite $merged (dict $key $value) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- if gt (len (keys $kubeletConfig)) 0 -}}
{{- $doc := dict "apiVersion" "v1alpha1" "kind" "KubeletConfig" -}}
{{- $_ := mustMergeOverwrite $doc $kubeletConfig -}}
- |
{{ printf "%s\n" ($doc | toYaml | indent 2) }}
{{- end -}}
{{- /* Control-plane-only document: Talos rejects it on workers */ -}}
{{- if and .isControlPlane (gt (len (keys $apiServerConfig)) 0) -}}
{{- $doc := dict "apiVersion" "v1alpha1" "kind" "KubeAPIServerConfig" -}}
{{- $_ := mustMergeOverwrite $doc $apiServerConfig -}}
- |
{{ printf "%s\n" ($doc | toYaml | indent 2) }}
{{- end -}}
{{- /* Control-plane-only document: Talos rejects it on workers */ -}}
{{- if and .isControlPlane (gt (len (keys $authConfig)) 0) -}}
{{- $doc := dict "apiVersion" "v1alpha1" "kind" "KubeAuthenticationConfig" -}}
{{- $_ := mustMergeOverwrite $doc $authConfig -}}
- |
{{ printf "%s\n" ($doc | toYaml | indent 2) }}
{{- end -}}
{{- /* Control-plane-only document: Talos rejects it on workers */ -}}
{{- if and .isControlPlane (gt (len (keys $flannelConfig)) 0) -}}
{{- $doc := dict "apiVersion" "v1alpha1" "kind" "KubeFlannelCNIConfig" -}}
{{- $_ := mustMergeOverwrite $doc $flannelConfig -}}
- |
{{ printf "%s\n" ($doc | toYaml | indent 2) }}
{{- end -}}
{{- /* Control-plane-only document: Talos rejects it on workers */ -}}
{{- if and .isControlPlane (gt (len (keys $proxyConfig)) 0) -}}
{{- $doc := dict "apiVersion" "v1alpha1" "kind" "KubeProxyConfig" -}}
{{- $_ := mustMergeOverwrite $doc $proxyConfig -}}
- |
{{ printf "%s\n" ($doc | toYaml | indent 2) }}
{{- end -}}
{{- if gt (len (keys $nodeConfig)) 0 -}}
{{- $doc := dict "apiVersion" "v1alpha1" "kind" "KubeNodeConfig" -}}
{{- $_ := mustMergeOverwrite $doc $nodeConfig -}}
- |
{{ printf "%s\n" ($doc | toYaml | indent 2) }}
{{- end -}}
{{- /* EnvironmentConfig: variables dict, LOG_LEVEL seeded from kommodity.logLevel */ -}}
{{- $envVars := deepCopy (default (dict) (dig "variables" (dict) $envConfig)) -}}
{{- if gt (len $envVars) 0 -}}
{{- $doc := dict "apiVersion" "v1alpha1" "kind" "EnvironmentConfig" "variables" $envVars -}}
- |
{{ printf "%s\n" ($doc | toYaml | indent 2) }}
{{- end -}}
{{- /* KernelModuleConfig: one document per entry */ -}}
{{- range $module := $kernelModules -}}
{{- $doc := dict "apiVersion" "v1alpha1" "kind" "KernelModuleConfig" -}}
{{- $_ := mustMergeOverwrite $doc (deepCopy $module) -}}
- |
{{ printf "%s\n" ($doc | toYaml | indent 2) }}
{{- end -}}
{{- /* RegistryMirrorConfig: one document per host, sorted by name */ -}}
{{- range $name := keys $registryMirrors | sortAlpha -}}
{{- $doc := dict "apiVersion" "v1alpha1" "kind" "RegistryMirrorConfig" -}}
{{- $_ := mustMergeOverwrite $doc (deepCopy (index $registryMirrors $name)) -}}
- |
{{ printf "%s\n" ($doc | toYaml | indent 2) }}
{{- end -}}
{{- /* RegistryAuthConfig: one document per host, sorted by name */ -}}
{{- range $name := keys $registryAuthConfigs | sortAlpha -}}
{{- $doc := dict "apiVersion" "v1alpha1" "kind" "RegistryAuthConfig" -}}
{{- $_ := mustMergeOverwrite $doc (deepCopy (index $registryAuthConfigs $name)) -}}
- |
{{ printf "%s\n" ($doc | toYaml | indent 2) }}
{{- end -}}
{{- /* RegistryTLSConfig: one document per host, sorted by name */ -}}
{{- range $name := keys $registryTLSConfigs | sortAlpha -}}
{{- $doc := dict "apiVersion" "v1alpha1" "kind" "RegistryTLSConfig" -}}
{{- $_ := mustMergeOverwrite $doc (deepCopy (index $registryTLSConfigs $name)) -}}
- |
{{ printf "%s\n" ($doc | toYaml | indent 2) }}
{{- end -}}
{{- /* ResolverConfig: single document, no name */ -}}
{{- if gt (len (keys $resolverConfig)) 0 -}}
{{- $doc := dict "apiVersion" "v1alpha1" "kind" "ResolverConfig" -}}
{{- $_ := mustMergeOverwrite $doc (deepCopy $resolverConfig) -}}
- |
{{ printf "%s\n" ($doc | toYaml | indent 2) }}
{{- end -}}
{{- /* CRICustomizationConfig: one document per entry */ -}}
{{- range $entry := $criCustomizations -}}
{{- $doc := dict "apiVersion" "v1alpha1" "kind" "CRICustomizationConfig" -}}
{{- $_ := mustMergeOverwrite $doc (deepCopy $entry) -}}
- |
{{ printf "%s\n" ($doc | toYaml | indent 2) }}
{{- end -}}
{{- if gt (len (keys $merged)) 0 -}}
- |
{{ printf "%s\n" ($merged | toYaml | indent 2) }}
{{- end -}}
{{- /* extraDocuments: raw multi-doc YAML appended verbatim. Split on
     document separators (--- plus optional comment, trailing whitespace,
     CRLF) so each document lands in its own patch item; content between
     separators is preserved byte-for-byte, only its trailing newlines are
     dropped (block scalar chomping adds them back). */ -}}
{{- with $extraDocs -}}
{{- range $doc := (regexSplit `(?:\r?\n|^)---(?:[ \t]+[^\r\n]*)?\r?\n` . -1) -}}
{{- if (trim $doc) -}}
- |+
{{ printf "%s\n" (trimSuffix "\n" $doc | indent 2) }}
{{- end -}}
{{- end -}}
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
{{- /* resources.cpu -> byot.io/cpu-cores (plain integer string), OPTIONAL.
     Not validated; passed through verbatim when set. */ -}}
{{- $cpu := dig "resources" "cpu" "" $p -}}
{{- if and $cpu (not (hasKey $labels "byot.io/cpu-cores")) -}}{{- $_ := set $labels "byot.io/cpu-cores" $cpu -}}{{- end -}}
{{- /* resources.memory -> byot.io/memory, OPTIONAL. Not validated;
     passed through verbatim when set. */ -}}
{{- $memory := dig "resources" "memory" "" $p -}}
{{- if and $memory (not (hasKey $labels "byot.io/memory")) -}}{{- $_ := set $labels "byot.io/memory" $memory -}}{{- end -}}
{{- $gpu := default (dict) (dig "resources" "gpu" (dict) $p) -}}
{{- $gpuCount := dig "count" "" $gpu | toString -}}
{{- $gpuModel := dig "model" "" $gpu -}}
{{- if or $gpuCount $gpuModel -}}
{{- if not $gpuModel -}}
{{- fail (printf "%s.resources.gpu.model is required when resources.gpu is set (model string matching the controller-promoted byot.io/gpu-model label, e.g. \"h100-pcie\")" $scope) -}}
{{- end -}}
{{- /* count defaults to 1 when only model is provided. */ -}}
{{- if not $gpuCount -}}{{- $gpuCount = "1" -}}{{- end -}}
{{- if not (regexMatch "^[0-9]+$" $gpuCount) -}}
{{- fail (printf "%s.resources.gpu.count must be a plain integer string of GPUs (e.g. \"1\"), got %q; byot.io/gpu-count is not a k8s quantity" $scope $gpuCount) -}}
{{- end -}}
{{- if not (hasKey $labels "byot.io/gpu-count") -}}{{- $_ := set $labels "byot.io/gpu-count" $gpuCount -}}{{- end -}}
{{- if not (hasKey $labels "byot.io/gpu-model") -}}{{- $_ := set $labels "byot.io/gpu-model" $gpuModel -}}{{- end -}}
{{- end -}}
{{- /* os.disk is OPTIONAL. Some hosts (e.g. Talos-in-Docker, diskless
     boot) promote no byot.io/disk-* labels, so requiring a disk selector
     would make them unclaimable. When either os.disk.type or os.disk.size is
     set, BOTH must be set and valid (a disk claim is type+class together);
     when neither is set, the selector omits byot.io/disk-* and matches any
     host regardless of disk. Operator freeform hostSelector labels can still
     pin a disk if needed. */ -}}
{{- $os := default (dict) (dig "os" (dict) $p) -}}
{{- $disk := default (dict) (dig "disk" (dict) $os) -}}
{{- $diskType := dig "type" "" $disk -}}
{{- $diskSize := dig "size" "" $disk -}}
{{- $diskTypes := list "nvme" "ssd" "hdd" "sd" -}}
{{- if or $diskType $diskSize -}}
{{- if not $diskType -}}
{{- fail (printf "%s.os.disk.type is required when os.disk.size is set (one of: nvme ssd hdd sd); omit both for a disk-agnostic selector" $scope) -}}
{{- end -}}
{{- if not (has $diskType $diskTypes) -}}
{{- fail (printf "%s.os.disk.type must be one of nvme ssd hdd sd (lowercase, matching the controller-promoted byot.io/disk-type label), got %q" $scope $diskType) -}}
{{- end -}}
{{- if not $diskSize -}}
{{- fail (printf "%s.os.disk.size is required when os.disk.type is set (a rounded value like 250G or 1T, matching the controller-promoted byot.io/disk-size label); omit both for a disk-agnostic selector" $scope) -}}
{{- end -}}
{{- if not (regexMatch "^[0-9]+[GT]$" $diskSize) -}}
{{- fail (printf "%s.os.disk.size must be a rounded value like 250G or 1T (integer + G/T suffix, matching the controller-promoted byot.io/disk-size label), got %q" $scope $diskSize) -}}
{{- end -}}
{{- if not (hasKey $labels "byot.io/disk-type") -}}{{- $_ := set $labels "byot.io/disk-type" $diskType -}}{{- end -}}
{{- if not (hasKey $labels "byot.io/disk-size") -}}{{- $_ := set $labels "byot.io/disk-size" $diskSize -}}{{- end -}}
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

{{/*
kommodity-cluster.byotDesiredTalosVersion — build the ByotMachineTemplate
spec.template.spec.desiredTalosVersion (an installer image ref) for a byot pool.

For BYOT the image is always an installer: the host is adopted in maintenance
mode and upgraded via LifecycleClient.Upgrade, which runs /bin/installer from
the ref. talos.imageName carries the full installer ref (e.g.
ghcr.io/siderolabs/installer:v1.14.2, a factory.talos.dev schematic installer,
or a custom-built installer with extensions). The tag is opaque to Talos, so
any tag works (extensions ship through the installer image, not the machine
config). Resolved with nodepool precedence — poolValues.talos.imageName
overrides the global talos.imageName. Required: when imageName is unset the
helper fails (no silent Sidero default); the default lives in values.byot.yaml
so operators opt in explicitly and custom-registry installs never silently
fall back to the public Sidero image.

Input: dict "poolValues" (controlplane/nodepool values) "allValues" (.Values).
Returns the installer image ref string.
*/ -}}
{{- define "kommodity-cluster.byotDesiredTalosVersion" -}}
{{- $imageName := default .allValues.talos.imageName (dig "talos" "imageName" "" .poolValues) -}}
{{- if not $imageName -}}
{{- fail "talos.imageName is required for BYOT (set it in values.byot.yaml to the installer image ref, e.g. ghcr.io/siderolabs/installer:v1.14.2)" -}}
{{- end -}}
{{- $imageName -}}
{{- end -}}
