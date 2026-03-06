# Resource Group
variable "resource_group" {
  type = object({
    name     = string
    location = string
  })
  description = "Resource group configuration"
  default = {
    name     = "kommodity"
    location = "North Europe"
  }
}

# Networking
variable "virtual_network" {
  type = object({
    address_space          = optional(string, "10.0.0.0/16")
    appgw_subnet_prefix    = optional(string, "10.0.0.0/24")
    aci_subnet_prefix      = optional(string, "10.0.1.0/24")
    database_subnet_prefix = optional(string, "10.0.2.0/24")
  })
  description = "Virtual network configuration"
  default     = {}
}

variable "database_password" {
  type = object({
    length  = number
    special = bool
  })
  description = "Database password configuration"
  default = {
    length  = 16
    special = false
  }
}

# Database
variable "database" {
  type = object({
    storage_tier                  = optional(string, "P4")
    sku_name                      = optional(string, "B_Standard_B1ms")
    storage_mb                    = optional(number, 32768)
    version                       = optional(string, "15")
    public_network_access_enabled = optional(bool, false)
    ha_enabled                    = optional(bool, false)
    storage_georedundant_enabled  = optional(bool, false)
    collation                     = optional(string, "en_US.utf8")
    add_lock                      = optional(bool, false)
  })
  description = "Database configuration"
  default     = {}
}

# Log Analytics
variable "log_analytics" {
  type = object({
    workspace_sku       = optional(string, "PerGB2018")
    workspace_retention = optional(number, 30)
  })
  description = "Log Analytics workspace configuration"
  default     = {}
}

# Application Gateway
variable "application_gateway" {
  type = object({
    sku_name = optional(string, "Standard_v2")
    sku_tier = optional(string, "Standard_v2")
    capacity = optional(number, 1)
  })
  description = "Application Gateway configuration"
  default     = {}
}

# Key Vault
variable "key_vault" {
  type = object({
    name     = string
    sku_name = optional(string, "standard")
  })
  description = "Key Vault configuration for TLS certificate storage"
}

# Kommodity Container (ACI)
variable "kommodity_container" {
  type = object({
    image_registry                  = optional(string, "ghcr.io/kommodity-io/kommodity")
    image_version                   = optional(string, "latest")
    port                            = optional(number, 5000)
    cpu                             = optional(number, 1)
    memory                          = optional(number, 1.5)
    restart_policy                  = optional(string, "Always")
    ssl_mode                        = optional(string, "require")
    insecure_disable_authentication = optional(string, "false")
    development_mode                = optional(string, "false")
    kine_uri                        = optional(string, "unix:///tmp/kine.sock")
    log_format                      = optional(string, "console")
    log_level                       = optional(string, "info")
    infrastructure_providers        = optional(string, "")
    base_url                        = optional(string, "")
  })
  description = "Kommodity ACI container configuration"
  default     = {}
}

variable "oidc_configuration" {
  type = object({
    issuer_url     = string
    client_id      = string
    admin_group    = string
    username_claim = optional(string, "")
  })
  description = "OIDC configuration"
}
