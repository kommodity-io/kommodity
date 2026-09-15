# Example deployment of Kommodity on Azure using OIDC authentication

This Terraform configuration ties together two Kommodity modules to deploy the application on Azure with OIDC authentication, behind a custom domain protected by an Azure-managed certificate.

It first retrieves the current Azure AD client context and uses it to deploy the `kommodity_oidc_auth` module, which sets up an Azure AD application for OIDC.
Then it deploys the `kommodity_azure_deployment` module, which provisions the full Azure infrastructure for Kommodity (network, PostgreSQL, Container App), publishes the public DNS records for `app_url`, and issues a managed TLS certificate bound to the custom domain.

## Provider aliases

The DNS zone is often hosted in a different Azure subscription than the workload. The example declares two `azurerm` providers and passes the `azurerm.dns` alias to the deployment module:

```tf
providers = {
  azurerm     = azurerm     # workload subscription
  azurerm.dns = azurerm.dns # subscription hosting the public DNS zone
}
```

If the DNS zone lives in the **same subscription** as the workload, point both aliases at the default provider — no second `provider "azurerm"` block needed:

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

## Optional: stable outbound IP

By default the Container App egresses through Azure's shared platform IP pool. Set `nat_gateway.enabled = true` to attach a NAT gateway with one static public IP to the container subnet; add that IP to downstream allowlists (firewalls, DB, partner APIs). Read the pinned address from the `container_app_egress_ip` output.

## Optional: ingress IP restrictions

`ingress_ip_restrictions` whitelists inbound traffic to the Container App. Each entry becomes an Azure Container Apps [`ip_security_restriction`](https://learn.microsoft.com/azure/container-apps/ingress-overview#ip-restrictions) rule.

| Field         | Type   | Default | Description                                         |
| ------------- | ------ | ------- | --------------------------------------------------- |
| `cidr`        | string | —       | IP range in CIDR notation (e.g. `203.0.113.10/32`). |
| `action`      | string | `Allow` | `Allow` or `Deny`.                                  |
| `name`        | string | —       | Rule name.                                          |
| `description` | string | `""`    | Free-form description.                              |

Azure evaluates rules **top-down; first match wins**, so list order determines priority. Any traffic not matching an `Allow` rule is denied — this is how a whitelist is enforced. Omit the variable (or pass `[]`) to leave ingress open to all traffic, the default behavior.

```tf
ingress_ip_restrictions = [
  { cidr = "203.0.113.10/32", name = "office-nat", description = "Office public egress IP" },
  { cidr = "198.51.100.0/24", name = "vpn-range",  description = "VPN subnet" },
]
```
