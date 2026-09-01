mock_provider "azurerm" {}

mock_provider "azurerm" {
  alias = "dns"
}

mock_provider "random" {}

mock_provider "time" {}

variables {
  resource_group = {
    name     = "test"
    location = "North Europe"
  }
  app_url = "https://kommodity.dev.example.com"
  dns = {
    zone              = "example.com"
    az_resource_group = "infra-dns"
  }
  oidc_configuration = {
    issuer_url  = "https://login.microsoftonline.com/tid/v2.0"
    client_id   = "cid"
    admin_group = "gid"
  }
}

run "validate" {
  command = plan
}

run "ingress_ip_restrictions_empty_default_allows_all" {
  command = plan

  assert {
    condition     = length(azurerm_container_app.kommodity-app.ingress[0].ip_security_restriction) == 0
    error_message = "Default (empty list) must produce no IP security restrictions."
  }
}
