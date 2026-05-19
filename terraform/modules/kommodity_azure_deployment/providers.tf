terraform {
  required_providers {
    azurerm = {
      source                = "hashicorp/azurerm"
      version               = "~>4.69.0"
      configuration_aliases = [azurerm, azurerm.dns]
    }

    random = {
      source  = "hashicorp/random"
      version = "~> 3.7"
    }

    time = {
      source  = "hashicorp/time"
      version = "~> 0.12"
    }
  }
}
