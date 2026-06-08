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
    address_space           = optional(string, "10.0.0.0/16")
    database_subnet_prefix  = optional(string, "10.0.2.0/24")
    container_subnet_prefix = optional(string, "10.0.0.0/23")
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

# Kommodity Container
variable "kommodity_container" {
  type = object({
    revision_mode                   = optional(string, "Single")
    image_registry                  = optional(string, "ghcr.io/kommodity-io/kommodity")
    image_version                   = optional(string, "latest")
    port                            = optional(number, 5000)
    cpu                             = optional(number, 0.25)
    memory                          = optional(string, "0.5Gi")
    min_replicas                    = optional(number, 1)
    max_replicas                    = optional(number, 1)
    ssl_mode                        = optional(string, "require")
    insecure_disable_authentication = optional(string, "false")
    development_mode                = optional(string, "false")
    kine_uri                        = optional(string, "unix:///tmp/kine.sock")
    log_format                      = optional(string, "console")
    log_level                       = optional(string, "info")
    infrastructure_providers        = optional(string, "") # If env var is empty, Kommodity uses default providers
    garbage_collector_enabled       = optional(string, "true")
    azure_embedded_arm_reconciler   = optional(string, "true")
    azure_default_credential_secret = optional(string, "")
  })
  description = "Kommodity container configuration"
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

variable "app_url" {
  type        = string
  description = "Custom domain URL for the Kommodity Container App (e.g. https://kommodity.dev.example.com). Must be a subdomain of var.dns.zone."

  validation {
    condition     = startswith(var.app_url, "https://") || startswith(var.app_url, "http://")
    error_message = "app_url must start with 'http://' or 'https://'."
  }
}

variable "dns" {
  type = object({
    zone              = string
    ttl               = optional(number, 300)
    az_resource_group = optional(string, "infrastructure-dns")
  })
  description = "DNS configuration for the custom domain. zone = parent DNS zone name; az_resource_group = resource group hosting the zone."
  validation {
    condition = can(regex(
      "^https?://([A-Za-z0-9-]+\\.)+${replace(var.dns.zone, ".", "\\.")}$",
      var.app_url
    ))
    error_message = "app_url must be an http(s) URL whose host is a non-apex subdomain of var.dns.zone, with no port, path, query, fragment, or trailing slash."
  }
}
