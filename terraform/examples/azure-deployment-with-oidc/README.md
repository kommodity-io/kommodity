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
- `environment` — `production` enables `CanNotDelete` locks on the CNAME and TXT verification records.
