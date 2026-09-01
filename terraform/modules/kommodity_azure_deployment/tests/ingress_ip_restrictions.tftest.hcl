mock_provider "azurerm" {}

mock_provider "azurerm" {
  alias = "dns"
}

mock_provider "azapi" {}

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
  ingress_ip_restrictions = [
    { cidr = "203.0.113.0/24", action = "Allow", name = "office", description = "Office NAT" },
    { cidr = "198.51.100.5/32", name = "vpn" },
  ]
}

run "ingress_ip_restrictions_rendered" {
  command = plan

  assert {
    condition     = length(azurerm_container_app.kommodity-app.ingress[0].ip_security_restriction) == 2
    error_message = "Two IP security restrictions should be rendered."
  }
  assert {
    condition     = azurerm_container_app.kommodity-app.ingress[0].ip_security_restriction[0].ip_address_range == "203.0.113.0/24"
    error_message = "First restriction CIDR mismatch."
  }
  assert {
    condition     = azurerm_container_app.kommodity-app.ingress[0].ip_security_restriction[0].action == "Allow"
    error_message = "First restriction action mismatch."
  }
  assert {
    condition     = azurerm_container_app.kommodity-app.ingress[0].ip_security_restriction[1].action == "Allow"
    error_message = "Default action should be Allow when omitted."
  }
}
