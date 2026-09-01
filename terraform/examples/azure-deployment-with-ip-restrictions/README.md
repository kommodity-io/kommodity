# Example deployment of Kommodity on Azure with ingress IP restrictions

This Terraform configuration deploys Kommodity on Azure with OIDC authentication and restricts inbound access to the Container App to a whitelist of IP/CIDR ranges.

It reuses the same two-module setup as [`azure-deployment-with-oidc`](../azure-deployment-with-oidc/) and adds an `ingress_ip_restrictions` block. The `kommodity_azure_deployment` module renders each entry as an Azure Container Apps [`ip_security_restriction`](https://learn.microsoft.com/azure/container-apps/ingress-overview#ip-restrictions) rule on the public ingress.

## Provider aliases

The DNS zone is often hosted in a different Azure subscription than the workload. The example declares two `azurerm` providers and passes the `azurerm.dns` alias to the deployment module:

```tf
providers = {
  azurerm     = azurerm     # workload subscription
  azurerm.dns = azurerm.dns # subscription hosting the public DNS zone
}
```

If the DNS zone lives in the **same subscription** as the workload, point both aliases at the default provider:

```tf
providers = {
  azurerm     = azurerm
  azurerm.dns = azurerm
}
```

## Required inputs

- `app_url` — full HTTPS URL of the custom domain (e.g. `https://kommodity.dev.example.com`); must be a subdomain of `dns.zone`.
- `dns.zone` — parent DNS zone name (e.g. `example.com`).
- `dns.az_resource_group` — resource group hosting the DNS zone (defaults to `infrastructure-dns`).

## IP restrictions

`ingress_ip_restrictions` is a list of objects:

| Field        | Type   | Default | Description |
| ------------ | ------- | ------- | ----------- |
| `cidr`       | string  | —       | IP range in CIDR notation (e.g. `203.0.113.10/32`). |
| `action`     | string  | `Allow` | `Allow` or `Deny`. |
| `name`       | string  | —       | Rule name. |
| `description`| string  | `""`    | Free-form description. |

Azure evaluates rules **top-down; first match wins**, so list order determines priority. Any traffic not matching an `Allow` rule is denied — this is how a whitelist is enforced.

```tf
ingress_ip_restrictions = [
  { cidr = "203.0.113.10/32", name = "office-nat", description = "Office public egress IP" },
  { cidr = "198.51.100.0/24", name = "vpn-range", description = "VPN subnet" },
]
```

Omit the variable (or pass `[]`) to leave ingress open to all traffic — the default behavior.

## Optional: stable outbound IP

By default the Container App egresses through Azure's shared platform IP pool. Set `nat_gateway.enabled = true` to attach a NAT gateway with one static public IP to the container subnet; add that IP to downstream allowlists (firewalls, DB, partner APIs). Read the pinned address from the `container_app_egress_ip` output.
