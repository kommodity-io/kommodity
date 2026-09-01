data "azuread_client_config" "current" {}

module "kommodity_oidc_auth" {
  source = "github.com/kommodity-io/kommodity//terraform/modules/kommodity_oidc_auth?ref=<tag>"

  owners = [data.azuread_client_config.current.object_id]
}

module "kommodity_azure_deployment" {
  source = "github.com/kommodity-io/kommodity//terraform/modules/kommodity_azure_deployment?ref=<tag>"

  providers = {
    azurerm     = azurerm
    azurerm.dns = azurerm.dns
  }

  resource_group = {
    name     = "my-kommodity"
    location = "North Europe"
  }

  app_url = "https://kommodity.dev.example.com"

  dns = {
    zone              = "example.com"
    az_resource_group = "infrastructure-dns"
  }

  oidc_configuration = {
    issuer_url  = "https://login.microsoftonline.com/<my-tenant-id>/v2.0"
    client_id   = module.kommodity_oidc_auth.application_client_id
    admin_group = "my-admin-group-ID"
  }

  # Restrict ingress to known networks. Unmatched traffic is denied.
  # Azure evaluates rules top-down; first match wins, so order matters.
  ingress_ip_restrictions = [
    {
      cidr        = "203.0.113.10/32"
      name        = "office-nat"
      description = "Office public egress IP"
    },
    {
      cidr        = "198.51.100.0/24"
      name        = "vpn-range"
      description = "VPN subnet"
    },
  ]
}
