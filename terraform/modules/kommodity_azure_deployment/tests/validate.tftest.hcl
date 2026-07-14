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

run "env_vars" {
  command = plan

  variables {
    kommodity_container = {
      env_vars = {
        KINE_DATASTORE_MAX_OPEN_CONNECTIONS    = "50"
        KINE_DATASTORE_CONNECTION_MAX_LIFETIME = "30m"
      }
    }
  }
}
