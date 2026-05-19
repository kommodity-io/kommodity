terraform {
  required_version = "~> 1.10.0"

  required_providers {
    azurerm = {
      source  = "hashicorp/azurerm"
      version = "~>4.69.0"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.7"
    }
  }
}

provider "azurerm" {
  subscription_id = "my-subscription-ID"
  features {}
}

# DNS zone usually lives in a separate "infrastructure" subscription.
provider "azurerm" {
  alias           = "dns"
  subscription_id = "my-dns-subscription-ID"
  features {}
}
