data "azuread_client_config" "current" {}

module "kommodity_oidc_auth" {
  source = "github.com/kommodity-io/kommodity//terraform/modules/kommodity_oidc_auth?ref=<tag>"

  owners = [data.azuread_client_config.current.object_id]
}

module "kommodity_azure_deployment" {
  source = "github.com/kommodity-io/kommodity//terraform/modules/kommodity_azure_deployment?ref=<tag>"

  resource_group = "my-resource-group"
  oidc_configuration = {
    issuer_url  = "https://login.microsoftonline.com/<my-tenant-id>/v2.0"
    client_id   = module.kommodity_oidc_auth.application_client_id
    admin_group = "my-admin-group-ID"
  }
}
