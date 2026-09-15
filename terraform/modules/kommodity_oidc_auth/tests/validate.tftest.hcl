mock_provider "azuread" {}

variables {
  owners = ["00000000-0000-0000-0000-000000000000"]
}

run "validate" {
  command = plan
}
