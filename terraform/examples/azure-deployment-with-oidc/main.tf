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
}
