# Migrating to kommodity-cluster 0.39.0 (Talos v1.14 multi-doc configs)

Chart 0.39.0 upgrades Talos to v1.14.5 and replaces the deprecated v1alpha1
`strategicPatches` fragment with `machineConfig`: kind-keyed
[Talos v1.14 multi-document configuration](https://docs.siderolabs.com/talos/v1.14/reference/configuration/document-map).

**Read this before upgrading:**

1. **`strategicPatches` is removed.** The chart no longer renders it. Values must move
   to `machineConfig` at the same three scopes:
   `kommodity.global.machineConfig`, `kommodity.controlplane.machineConfig`,
   `kommodity.nodepools.<name>.machineConfig`.
2. **Upgrading rolls every machine, even with identical configuration.** The config
   hash inputs changed with the rename, so TalosConfigTemplate/MachineTemplate names
   change on first deploy of 0.39.0 and CAPI replaces all machines. Plan an upgrade
   window.
3. **The CAPI CRD field is still `strategicPatches`.** Only the Helm values key is
   renamed. Each rendered item is now a full multi-doc kind (or a v1alpha1 fragment
   for fields without a multi-doc replacement), not a single merged fragment.

## The new `machineConfig` shape

Keys are dispatched three ways, based on the kind classification generated from
Talos's published schema (`files/talos-doc-kinds.yaml`):

| Key | Shape | Semantics |
|---|---|---|
| **Singleton kind** (`KubeletConfig`, `EnvironmentConfig`, `SysctlConfig`, `KubeAPIServerConfig`, ...) | plain map of the document's fields | `apiVersion`/`kind` envelope injected; global and nodepool deep-merged (nodepool wins on conflicts); one document per kind |
| **Named kind** (`KernelModuleConfig`, `RegistryMirrorConfig`, `EtcFileConfig`, ...) | map keyed by document `name` | one document per entry; the key becomes `name`; a null value emits a bare document; **a nodepool entry with the same name replaces the global entry wholesale** (Talos rejects duplicate names within a kind, so field-level merging across scopes is impossible); a body `name` that does not match the map key fails rendering |
| **`machine:` / `cluster:` / unknown kinds** | v1alpha1 fragment | deep-merged together and emitted verbatim as a single strategic patch — the escape hatch for fields with no multi-doc replacement |

### Singleton kind example

Plain map of the document's fields. Global and nodepool deep-merge — the nodepool
wins per field, everything else from global is kept:

```yaml
kommodity:
  global:
    machineConfig:
      KubeletConfig:
        extraArgs:
          max-pods: "200"
  nodepools:
    gpu:
      machineConfig:
        KubeletConfig:
          extraArgs:
            max-pods: "512"   # nodepool wins; global KubeletConfig fields kept
```

### Named kind example

Map keyed by document `name` — the key becomes the `name` field; do not set `name`
in the body. A nodepool entry with the same name replaces the global entry
wholesale; different names coexist:

```yaml
kommodity:
  global:
    machineConfig:
      KernelModuleConfig:
        br_netfilter: {}        # null body: bare document, just load the module
        nvidia:
          parameters:
            - NVreg_EnableS0ixStreamingBER=0
  nodepools:
    gpu:
      machineConfig:
        KernelModuleConfig:
          nvidia:               # replaces the global `nvidia` entry wholesale
            parameters:
              - NVreg_DeviceOverrideGPUA75.4DBF.LT=1
```

There is also a raw escape hatch:

```yaml
machineConfig:
  extraDocuments: |
    ---
    apiVersion: v1alpha1
    kind: SecurityProfileConfig
    workloadIsolation: true
```

`extraDocuments` is a raw multi-doc YAML string, split on `---` and appended
verbatim, one patch item per document. A nodepool `extraDocuments` replaces the
global one wholesale.

Full list of document kinds and which v1alpha1 field each replaces:
[Talos v1.14 document map](https://docs.siderolabs.com/talos/v1.14/reference/configuration/document-map).

## Field mapping

| Old (`strategicPatches`) | New (`machineConfig`) | Notes |
|---|---|---|
| `machine.kubelet.extraArgs` | `KubeletConfig.extraArgs` | singleton kind |
| `machine.kubelet.extraConfig` | `KubeletConfig.config` | field renamed by Talos v1.14 |
| `machine.env` | `EnvironmentConfig.variables` | singleton kind; `kommodity.logLevel` is auto-emitted as `LOG_LEVEL` here |
| `machine.nodeLabels` | `KubeNodeConfig.labels` | pool-level `labels`/`annotations`/`taints` values (outside `machineConfig`) also emit `KubeNodeConfig` — no need to duplicate |
| `machine.nodeAnnotations` | `KubeNodeConfig.annotations` | |
| `machine.nodeTaints` | `KubeNodeConfig.taints` | the chart no longer emits `register-with-taints` as a kubelet extraArg — remove any manual override of it |
| `machine.kernel.modules` | `KernelModuleConfig` (name-keyed) | one document per module: `KernelModuleConfig: {nvidia: null, br_netfilter: {parameters: [...]}}` |
| `machine.sysctls` | `SysctlConfig.sysctls` | singleton kind |
| `machine.sysfs` | `SysfsConfig.sysfs` | singleton kind |
| `machine.network.nameservers` | `ResolverConfig` | singleton kind; on Hetzner, a user-supplied `ResolverConfig` suppresses the chart's built-in one |
| `machine.registries.mirrors.<host>` | `RegistryMirrorConfig` (name-keyed by host) | one document per host |
| `machine.registries.config.<host>.auth` | `RegistryAuthConfig` (name-keyed by host) | one document per host |
| `machine.registries.config.<host>.tls` | `RegistryTLSConfig` (name-keyed by host) | one document per host |
| `cluster.apiServer.extraArgs` | `KubeAPIServerConfig.extraArgs` | except OIDC/anonymous flags, see below |
| `cluster.proxy` | `KubeProxyConfig` | |
| `cluster.scheduler` | `KubeSchedulerConfig` | |
| `cluster.controllerManager` | `KubeControllerManagerConfig` | |
| `cluster.coreDNS` | `KubeCoreDNSConfig` | |
| `cluster.allowSchedulingOnControlPlanes` | `KubeNodeConfig` with `taints."node-role.kubernetes.io/control-plane": {$patch: delete}` | |
| `cluster.inlineManifests` | `KubeInlineManifestConfig` (name-keyed) | field `contents` renamed to `manifest`; one document per manifest |
| `cluster.extraManifests` | `KubeExternalManifestConfig` | |
| `machine.udev` | `UdevRulesConfig` | |
| `machine.time` | `TimeSyncConfig` | |
| `machine.install` | `UnattendedInstallConfig` | |

### Stay as v1alpha1 fragments (no multi-doc replacement in v1.14)

Keep these under the `machine:` / `cluster:` fragment keys in `machineConfig` — the
shape is unchanged, just moved one level down:

`machine.kubelet.extraMounts`, `machine.network.interfaces`, `machine.disks`,
`machine.certSANs`, `machine.features.*`, `machine.logging`,
`machine.seccompProfiles`, `machine.type`, `machine.ca`, `cluster.etcd`,
`cluster.token`, `cluster.externalCloudProvider`, `cluster.adminKubeconfig`

Anything else with no automatic mapping goes in `extraDocuments`.

## Worked example

Before (0.38.x):

```yaml
kommodity:
  global:
    strategicPatches:
      machine:
        kubelet:
          extraArgs:
            cloud-provider: external
            max-pods: "200"
        env:
          HTTP_PROXY: http://proxy.example.com:8080
        nodeLabels:
          pool: general
        kernel:
          modules:
            - name: br_netfilter
        registries:
          mirrors:
            registry.internal:
              endpoints:
                - https://registry.internal/v2
      cluster:
        apiServer:
          extraArgs:
            anonymous-auth: "true"
        allowSchedulingOnControlPlanes: true
```

After (0.39.0):

```yaml
kommodity:
  global:
    machineConfig:
      KubeletConfig:                 # singleton kind: plain fields
        extraArgs:
          cloud-provider: external
          max-pods: "200"
      EnvironmentConfig:             # singleton kind
        variables:
          HTTP_PROXY: http://proxy.example.com:8080
      KubeNodeConfig:                # singleton kind
        labels:
          pool: general
        taints:
          node-role.kubernetes.io/control-plane:
            $patch: delete           # was cluster.allowSchedulingOnControlPlanes
      KernelModuleConfig:            # named kind: keyed by module name
        br_netfilter: {}
      RegistryMirrorConfig:          # named kind: keyed by registry host
        registry.internal:
          endpoints:
            - https://registry.internal/v2
      KubeAuthenticationConfig:      # singleton kind
        configuration:
          anonymous:
            enabled: true
            conditions:
              - path: /livez
              - path: /readyz
              - path: /healthz
```

## OIDC and authentication changes

`kommodity.oidc.*` values are unchanged; the chart now emits a
`KubeAuthenticationConfig` document instead of `oidc-*` apiserver extraArgs
(Talos v1.14 rejects those flags). Two behavior changes to know:

- **`--anonymous-auth` is rejected** in `KubeAPIServerConfig.extraArgs`. Use
  `KubeAuthenticationConfig.configuration.anonymous` (see example above; the
  Azure values file ships exactly this).
- **OIDC usernames are now stable.** Legacy flags never prefixed the `email`
  username claim; the chart preserves that (empty prefix for `email`, issuer
  prefix for any other claim). Existing RoleBindings keep matching — do not
  copy the old `<issuer>#` prefix expectations into new configs.

## No values changes needed

These keep their values keys; only the emitted documents changed under the hood:

- `talos.disableCNI` — now emits `KubeFlannelCNIConfig` with `$patch: delete`
- `talos.disableKubeProxy` — now emits `KubeProxyConfig` with `enabled: false`
- `kommodity.logLevel` — now emits `EnvironmentConfig` with `LOG_LEVEL`
- pool-level `labels` / `annotations` / `taints` — now emit `KubeNodeConfig`
- `instanceVolumes`, `raidArrays`, `additionalVolumes`, `kms`, `security`,
  `autoBootstrap` — already multi-doc, unchanged

## Deployments repo: secrets.yaml helm_path references

Any `helm_path` pointing into the old key must be renamed, e.g.:

```
kommodity.global.strategicPatches.machine.registries.config.corti.azurecr.io.auth.password
→ kommodity.global.machineConfig.RegistryAuthConfig.corti.azurecr.io.auth.password
```

(named-kind maps are keyed by document name, so the registry host moved one level
down into the kind's name map).

## Render-time validation

The chart now fails rendering (instead of emitting a broken config) when:

- a named-kind entry's body `name` does not match its map key — drop the `name`
  field, the key is the name
- a kind value is not a map
