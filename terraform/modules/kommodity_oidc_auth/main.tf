# Azure AD Application (App Registration)

data "azuread_client_config" "current" {}

resource "azuread_application" "kommodity_oidc_app" {
  display_name                   = var.azure_ad_application.name
  sign_in_audience               = var.azure_ad_application.sign_in_audience
  group_membership_claims        = var.group_membership_claims
  fallback_public_client_enabled = true
  owners                         = var.owners

  public_client {
    redirect_uris = [
      "http://localhost:8000", # redirect to kubectl oidc-login
    ]
  }

  required_resource_access {
    resource_app_id = "00000003-0000-0000-c000-000000000000" # Microsoft Graph

    dynamic "resource_access" {
      for_each = var.resource_access_list
      content {
        id   = resource_access.value.id
        type = resource_access.value.type
      }
    }
  }
}

# Service principal (enterprise app)
resource "azuread_service_principal" "kommodity_oidc_sp" {
  client_id = azuread_application.kommodity_oidc_app.client_id
  owners    = var.owners
}
