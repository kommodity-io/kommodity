# Upgrading to kommodity-cluster 0.39.0

Version 0.39.0 restructures the provider-specific values under
`kommodity.provider`. This is a **breaking change to the values interface**:
the rendered Kubernetes resources are byte-for-byte identical, but your values
files must be updated or the chart fails at render time with a
`missing required ...` error (or, worse, silently drops provider settings it
can no longer find).

## What changed and why

Previously all providers shared the generic `kommodity.provider.secret` and
`kommodity.provider.config` blocks. Helm deep-merges values files, so settings
from one provider's values file leaked into clusters of another provider —
e.g. Scaleway's `scaleway-secret` ending up on an Azure cluster, with the
wrong credentials delivered to its CCM.

In 0.39.0 every provider reads its settings from its own key,
`kommodity.provider.<lowercase-provider-name>` (matching `provider.name`):
`azure`, `byot`, `hetzner`, `kubevirt`, `scaleway`. There is no nested
`config:` level — provider settings sit directly under the provider key.

## Values migration

### Generic rule

| Before (≤ 0.38)                     | After (0.39)                                        |
| ------------------------------------ | --------------------------------------------------- |
| `kommodity.provider.secret.name`     | `kommodity.provider.<name>.secret.name`             |
| `kommodity.provider.config.<key>`    | `kommodity.provider.<name>.<key>`                   |
| `kommodity.provider.identity.*`      | `kommodity.provider.<name>.identity.*` (Azure only) |
| `kommodity.region`                   | `kommodity.provider.scaleway.region` (Scaleway only) |

### Azure clusters

```yaml
# Before
kommodity:
  provider:
    name: Azure
    identity:
      name: azure-cluster-identity
    config:
      subscriptionID: <your-subscription-id>
      location: westeurope
      resourceGroup: my-cluster-rg
      talosImageResourceGroup: shared-infra
      # allowSharedResourceGroup: true

# After
kommodity:
  provider:
    name: Azure
    azure:
      identity:
        name: azure-cluster-identity
      subscriptionID: <your-subscription-id>
      location: westeurope
      resourceGroup: my-cluster-rg
      talosImageResourceGroup: shared-infra
      # allowSharedResourceGroup: true
```

The optional CCM secret override moves from `kommodity.provider.secret.name`
to `kommodity.provider.azure.secret.name`; when unset it still defaults to
`<release>-cloud-provider`.

### Hetzner clusters

```yaml
# Before
kommodity:
  provider:
    name: Hetzner
    secret:
      name: hetzner
    config:
      region: fsn1
      # sshKeyName: my-ssh-key
      # loadBalancer:
      #   enabled: true
      #   type: lb11
      # controlPlaneEndpoint:
      #   host: cp.example.com
      #   port: 6443
      # networkZone: eu-central

# After
kommodity:
  provider:
    name: Hetzner
    hetzner:
      secret:
        name: hetzner
      region: fsn1
      # sshKeyName: my-ssh-key
      # loadBalancer:
      #   enabled: true
      #   type: lb11
      # controlPlaneEndpoint:
      #   host: cp.example.com
      #   port: 6443
      # networkZone: eu-central
```

### Kubevirt clusters

```yaml
# Before
kommodity:
  provider:
    name: Kubevirt
    secret:
      name: kubevirt-credentials
    config:
      infraClusterNamespace: kubevirt-cluster-namespace
      # storageClassName: my-storage-class
      # controlPlaneServiceTemplate: ...
      # controlPlaneEndpoint: ...
      # evictionStrategy: LiveMigrateIfPossible

# After
kommodity:
  provider:
    name: Kubevirt
    kubevirt:
      secret:
        name: kubevirt-credentials
      infraClusterNamespace: kubevirt-cluster-namespace
      # storageClassName: my-storage-class
      # controlPlaneServiceTemplate: ...
      # controlPlaneEndpoint: ...
      # evictionStrategy: LiveMigrateIfPossible
```

### Scaleway clusters

```yaml
# Before
kommodity:
  region: fr-par
  provider:
    name: Scaleway
    secret:
      name: scaleway-secret
    config:
      projectID: <your-project-id>

# After
kommodity:
  provider:
    name: Scaleway
    scaleway:
      region: fr-par
      projectID: <your-project-id>
      # secret.name still defaults to "scaleway-secret" when omitted;
      # set it only if you use a custom secret name.
      # secret:
      #   name: scaleway-secret
```

`kommodity.region` was only ever consumed by the Scaleway templates and is
gone; Scaleway now reads `region` from its own block, in the same style as
Hetzner's `provider.hetzner.region`.

### Byot clusters

```yaml
# Before
kommodity:
  provider:
    name: Byot
    config:
      controlPlaneEndpoint:
        host: <your-cp-endpoint>
        port: 6443

# After
kommodity:
  provider:
    name: Byot
    byot:
      controlPlaneEndpoint:
        host: <your-cp-endpoint>
        port: 6443
```

## What stays the same

- `kommodity.provider.name` — unchanged, still selects the provider.
- `kommodity.provider.cloudControllerManager.*` — unchanged; CCM configuration
  is shared across providers and stays at the generic level.
- All non-provider values (`kommodity.network`, `kommodity.controlplane`,
  `kommodity.nodepools`, `kommodity.addons`, `talos`, ...) — unchanged.
- Rendered resource names and specs — unchanged. Because the rendered output
  is identical, upgrading does not roll or recreate any workload; only the
  values interface moved. This also means a
  `helm template ... -f values.yaml` diff against the previous chart version
  should come back empty once your values are migrated.

## How to upgrade

1. Move your provider settings under `kommodity.provider.<name>` as shown
   above (remove the `config:` nesting; do not keep a stale
   `kommodity.provider.config`/`kommodity.provider.secret` block — unknown
   keys are silently ignored and would mask a missed field).
2. For Scaleway, also move `kommodity.region` to
   `kommodity.provider.scaleway.region`.
3. Render with the 0.39.0 chart and diff against the output of your current
   version to confirm the rendered manifests are unchanged:

   ```bash
   helm template my-cluster charts/kommodity-cluster -f my-cluster.yaml > new.yaml
   diff old.yaml new.yaml
   ```

4. Apply as usual. The chart intentionally fails fast at render time on
   missing required fields (e.g. Scaleway `projectID`/`region`, Azure
   `subscriptionID`/`location`, Byot `controlPlaneEndpoint.host`) instead of
   rendering half-configured clusters.
