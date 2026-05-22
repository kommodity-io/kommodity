terraform {
  required_providers {
    azurerm = {
      source                = "hashicorp/azurerm"
      version               = "~>4.69.0"
      configuration_aliases = [azurerm, azurerm.dns]
    }

    azapi = {
      source  = "azure/azapi"
      version = "~> 2.9"
    }

    random = {
      source  = "hashicorp/random"
      version = "~> 3.9"
    }

    time = {
      source  = "hashicorp/time"
      version = "~> 0.14"
    }
  }
}
