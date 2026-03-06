# Current Azure client config (for Key Vault tenant_id)
data "azurerm_client_config" "current" {}

# Resource Group
resource "azurerm_resource_group" "kommodity-resource-group" {
  name     = var.resource_group.name
  location = var.resource_group.location
}

# Virtual Network
resource "azurerm_virtual_network" "kommodity-vn" {
  name                = "${var.resource_group.name}-vn"
  location            = azurerm_resource_group.kommodity-resource-group.location
  resource_group_name = azurerm_resource_group.kommodity-resource-group.name
  address_space       = ["${var.virtual_network.address_space}"]

  depends_on = [azurerm_resource_group.kommodity-resource-group]
}

# Application Gateway Subnet
resource "azurerm_subnet" "kommodity-appgw-sn" {
  name                 = "${var.resource_group.name}-appgw-sn"
  resource_group_name  = azurerm_resource_group.kommodity-resource-group.name
  virtual_network_name = azurerm_virtual_network.kommodity-vn.name
  address_prefixes     = ["${var.virtual_network.appgw_subnet_prefix}"]

  depends_on = [
    azurerm_resource_group.kommodity-resource-group,
    azurerm_virtual_network.kommodity-vn,
  ]
}

# ACI Subnet
resource "azurerm_subnet" "kommodity-aci-sn" {
  name                 = "${var.resource_group.name}-aci-sn"
  resource_group_name  = azurerm_resource_group.kommodity-resource-group.name
  virtual_network_name = azurerm_virtual_network.kommodity-vn.name
  address_prefixes     = ["${var.virtual_network.aci_subnet_prefix}"]

  delegation {
    name = "Microsoft.ContainerInstance.containerGroups"
    service_delegation {
      name = "Microsoft.ContainerInstance/containerGroups"
      actions = [
        "Microsoft.Network/virtualNetworks/subnets/join/action",
      ]
    }
  }

  depends_on = [
    azurerm_resource_group.kommodity-resource-group,
    azurerm_virtual_network.kommodity-vn,
  ]
}

# Database Subnet
resource "azurerm_subnet" "kommodity-db-sn" {
  name                 = "${var.resource_group.name}-db-sn"
  resource_group_name  = azurerm_resource_group.kommodity-resource-group.name
  virtual_network_name = azurerm_virtual_network.kommodity-vn.name
  address_prefixes     = ["${var.virtual_network.database_subnet_prefix}"]
  service_endpoints    = ["Microsoft.Storage"]
  delegation {
    name = "fs"
    service_delegation {
      name = "Microsoft.DBforPostgreSQL/flexibleServers"
      actions = [
        "Microsoft.Network/virtualNetworks/subnets/join/action",
      ]
    }
  }

  depends_on = [
    azurerm_resource_group.kommodity-resource-group,
    azurerm_virtual_network.kommodity-vn,
  ]
}

# Private DNS Zone
resource "azurerm_private_dns_zone" "kommodity-dns" {
  name                = "${var.resource_group.name}.postgres.database.azure.com"
  resource_group_name = azurerm_resource_group.kommodity-resource-group.name

  depends_on = [azurerm_resource_group.kommodity-resource-group]
}

# DNS-VNET Link
resource "azurerm_private_dns_zone_virtual_network_link" "kommodity-dns-vnet-link" {
  name                  = "${azurerm_virtual_network.kommodity-vn.name}-link"
  private_dns_zone_name = azurerm_private_dns_zone.kommodity-dns.name
  virtual_network_id    = azurerm_virtual_network.kommodity-vn.id
  resource_group_name   = azurerm_resource_group.kommodity-resource-group.name
  depends_on = [
    azurerm_resource_group.kommodity-resource-group,
    azurerm_virtual_network.kommodity-vn,
    azurerm_subnet.kommodity-db-sn,
    azurerm_private_dns_zone.kommodity-dns,
  ]
}

# Database Password
resource "random_password" "database-password" {
  length  = var.database_password.length
  special = var.database_password.special
}

# Management Lock
resource "azurerm_management_lock" "this" {
  count = var.database.add_lock ? 1 : 0

  name       = "${azurerm_postgresql_flexible_server.kommodity-db.name}-lock"
  scope      = azurerm_postgresql_flexible_server.kommodity-db.id
  lock_level = "CanNotDelete"
  notes      = "Protect accidental deletion of PostgreSQL database resources"
}

# PostgreSQL Flexible Server
resource "azurerm_postgresql_flexible_server" "kommodity-db" {
  name                          = "${var.resource_group.name}-db"
  resource_group_name           = azurerm_resource_group.kommodity-resource-group.name
  location                      = azurerm_resource_group.kommodity-resource-group.location
  version                       = var.database.version
  delegated_subnet_id           = azurerm_subnet.kommodity-db-sn.id
  private_dns_zone_id           = azurerm_private_dns_zone.kommodity-dns.id
  public_network_access_enabled = var.database.public_network_access_enabled
  administrator_login           = "kommodity"
  administrator_password        = random_password.database-password.result

  storage_mb   = var.database.storage_mb
  storage_tier = var.database.storage_tier

  sku_name = var.database.sku_name
  depends_on = [
    azurerm_resource_group.kommodity-resource-group,
    azurerm_private_dns_zone_virtual_network_link.kommodity-dns-vnet-link,
  ]

  geo_redundant_backup_enabled = var.database.storage_georedundant_enabled

  dynamic "high_availability" {
    for_each = var.database.ha_enabled ? [""] : []

    content {
      mode = "ZoneRedundant"
    }
  }

  lifecycle {
    ignore_changes = [
      zone,
      high_availability.0.standby_availability_zone
    ]
  }
}

# PostgreSQL Database
resource "azurerm_postgresql_flexible_server_database" "this" {
  name      = "kommodity"
  server_id = azurerm_postgresql_flexible_server.kommodity-db.id
  charset   = "UTF8"
  collation = var.database.collation

  depends_on = [azurerm_postgresql_flexible_server.kommodity-db]

  lifecycle {
    prevent_destroy = true
  }
}

# Log Analytics Workspace
resource "azurerm_log_analytics_workspace" "kommodity-log-analytics" {
  name                = "${var.resource_group.name}-log-analytics"
  location            = azurerm_resource_group.kommodity-resource-group.location
  resource_group_name = azurerm_resource_group.kommodity-resource-group.name
  sku                 = var.log_analytics.workspace_sku
  retention_in_days   = var.log_analytics.workspace_retention

  depends_on = [azurerm_resource_group.kommodity-resource-group]
}

# User-Assigned Managed Identity (for App Gateway to access Key Vault)
resource "azurerm_user_assigned_identity" "kommodity-appgw-identity" {
  name                = "${var.resource_group.name}-appgw-identity"
  location            = azurerm_resource_group.kommodity-resource-group.location
  resource_group_name = azurerm_resource_group.kommodity-resource-group.name

  depends_on = [azurerm_resource_group.kommodity-resource-group]
}

# Key Vault
resource "azurerm_key_vault" "kommodity-kv" {
  name                       = var.key_vault.name
  location                   = azurerm_resource_group.kommodity-resource-group.location
  resource_group_name        = azurerm_resource_group.kommodity-resource-group.name
  tenant_id                  = data.azurerm_client_config.current.tenant_id
  sku_name                   = var.key_vault.sku_name
  soft_delete_retention_days = 7
  purge_protection_enabled   = false

  depends_on = [azurerm_resource_group.kommodity-resource-group]
}

# Key Vault Access Policy — current client (to manage certificates)
resource "azurerm_key_vault_access_policy" "kommodity-kv-current" {
  key_vault_id = azurerm_key_vault.kommodity-kv.id
  tenant_id    = data.azurerm_client_config.current.tenant_id
  object_id    = data.azurerm_client_config.current.object_id

  certificate_permissions = [
    "Create",
    "Delete",
    "Get",
    "Import",
    "List",
    "Purge",
    "Update",
  ]

  secret_permissions = [
    "Get",
    "List",
  ]

  depends_on = [azurerm_key_vault.kommodity-kv]
}

# Key Vault Access Policy — App Gateway managed identity
resource "azurerm_key_vault_access_policy" "kommodity-kv-appgw" {
  key_vault_id = azurerm_key_vault.kommodity-kv.id
  tenant_id    = data.azurerm_client_config.current.tenant_id
  object_id    = azurerm_user_assigned_identity.kommodity-appgw-identity.principal_id

  secret_permissions = [
    "Get",
  ]

  depends_on = [
    azurerm_key_vault.kommodity-kv,
    azurerm_user_assigned_identity.kommodity-appgw-identity,
  ]
}

# Self-signed TLS Certificate
resource "azurerm_key_vault_certificate" "kommodity-cert" {
  name         = "${var.resource_group.name}-cert"
  key_vault_id = azurerm_key_vault.kommodity-kv.id

  certificate_policy {
    issuer_parameters {
      name = "Self"
    }

    key_properties {
      exportable = true
      key_size   = 2048
      key_type   = "RSA"
      reuse_key  = true
    }

    lifetime_action {
      action {
        action_type = "AutoRenew"
      }

      trigger {
        lifetime_percentage = 80
      }
    }

    secret_properties {
      content_type = "application/x-pkcs12"
    }

    x509_certificate_properties {
      key_usage = [
        "cRLSign",
        "dataEncipherment",
        "digitalSignature",
        "keyAgreement",
        "keyCertSign",
        "keyEncipherment",
      ]

      subject            = "CN=${var.resource_group.name}"
      validity_in_months = 12
    }
  }

  depends_on = [azurerm_key_vault_access_policy.kommodity-kv-current]
}

# Public IP for Application Gateway
resource "azurerm_public_ip" "kommodity-appgw-pip" {
  name                = "${var.resource_group.name}-appgw-pip"
  location            = azurerm_resource_group.kommodity-resource-group.location
  resource_group_name = azurerm_resource_group.kommodity-resource-group.name
  allocation_method   = "Static"
  sku                 = "Standard"

  depends_on = [azurerm_resource_group.kommodity-resource-group]
}

# Application Gateway (TCP/TLS Proxy for HTTP/2 passthrough)
locals {
  appgw_frontend_ip_name      = "${var.resource_group.name}-appgw-feip"
  appgw_frontend_port_name    = "${var.resource_group.name}-appgw-feport"
  appgw_backend_pool_name     = "${var.resource_group.name}-appgw-bepool"
  appgw_backend_settings_name = "${var.resource_group.name}-appgw-besettings"
  appgw_listener_name         = "${var.resource_group.name}-appgw-listener"
  appgw_routing_rule_name     = "${var.resource_group.name}-appgw-rule"
  appgw_probe_name            = "${var.resource_group.name}-appgw-probe"
  appgw_ssl_cert_name         = "${var.resource_group.name}-appgw-sslcert"
  appgw_gw_ip_config_name     = "${var.resource_group.name}-appgw-gwip"
}

resource "azurerm_application_gateway" "kommodity-appgw" {
  name                = "${var.resource_group.name}-appgw"
  location            = azurerm_resource_group.kommodity-resource-group.location
  resource_group_name = azurerm_resource_group.kommodity-resource-group.name
  sku {
    name     = var.application_gateway.sku_name
    tier     = var.application_gateway.sku_tier
    capacity = var.application_gateway.capacity
  }

  identity {
    type         = "UserAssigned"
    identity_ids = [azurerm_user_assigned_identity.kommodity-appgw-identity.id]
  }

  gateway_ip_configuration {
    name      = local.appgw_gw_ip_config_name
    subnet_id = azurerm_subnet.kommodity-appgw-sn.id
  }

  frontend_ip_configuration {
    name                 = local.appgw_frontend_ip_name
    public_ip_address_id = azurerm_public_ip.kommodity-appgw-pip.id
  }

  frontend_port {
    name = local.appgw_frontend_port_name
    port = 443
  }

  ssl_certificate {
    name                = local.appgw_ssl_cert_name
    key_vault_secret_id = azurerm_key_vault_certificate.kommodity-cert.versionless_secret_id
  }

  backend_address_pool {
    name         = local.appgw_backend_pool_name
    ip_addresses = [azurerm_container_group.kommodity-aci.ip_address]
  }

  backend_http_settings {
    name                  = local.appgw_backend_settings_name
    cookie_based_affinity = "Disabled"
    port                  = var.kommodity_container.port
    protocol              = "Http"
    request_timeout       = 86400
    probe_name            = local.appgw_probe_name
  }

  http_listener {
    name                           = local.appgw_listener_name
    frontend_ip_configuration_name = local.appgw_frontend_ip_name
    frontend_port_name             = local.appgw_frontend_port_name
    protocol                       = "Https"
    ssl_certificate_name           = local.appgw_ssl_cert_name
  }

  request_routing_rule {
    name                       = local.appgw_routing_rule_name
    priority                   = 1
    rule_type                  = "Basic"
    http_listener_name         = local.appgw_listener_name
    backend_address_pool_name  = local.appgw_backend_pool_name
    backend_http_settings_name = local.appgw_backend_settings_name
  }

  probe {
    name                = local.appgw_probe_name
    protocol            = "Http"
    host                = azurerm_container_group.kommodity-aci.ip_address
    path                = "/readyz"
    interval            = 30
    timeout             = 30
    unhealthy_threshold = 3
  }

  depends_on = [
    azurerm_resource_group.kommodity-resource-group,
    azurerm_subnet.kommodity-appgw-sn,
    azurerm_public_ip.kommodity-appgw-pip,
    azurerm_key_vault_certificate.kommodity-cert,
    azurerm_key_vault_access_policy.kommodity-kv-appgw,
    azurerm_container_group.kommodity-aci,
  ]
}

# Container Group (ACI)
resource "azurerm_container_group" "kommodity-aci" {
  name                = "${var.resource_group.name}-aci"
  location            = azurerm_resource_group.kommodity-resource-group.location
  resource_group_name = azurerm_resource_group.kommodity-resource-group.name
  os_type             = "Linux"
  ip_address_type     = "Private"
  subnet_ids          = [azurerm_subnet.kommodity-aci-sn.id]
  restart_policy      = var.kommodity_container.restart_policy

  container {
    name   = "${var.resource_group.name}-container"
    image  = "${var.kommodity_container.image_registry}:${var.kommodity_container.image_version}"
    cpu    = var.kommodity_container.cpu
    memory = var.kommodity_container.memory

    ports {
      port     = var.kommodity_container.port
      protocol = "TCP"
    }

    environment_variables = {
      KOMMODITY_DB_URI                          = "postgres://${azurerm_postgresql_flexible_server.kommodity-db.administrator_login}:${random_password.database-password.result}@${azurerm_postgresql_flexible_server.kommodity-db.fqdn}:5432/kommodity?sslmode=${var.kommodity_container.ssl_mode}"
      KOMMODITY_PORT                            = var.kommodity_container.port
      KOMMODITY_INSECURE_DISABLE_AUTHENTICATION = var.kommodity_container.insecure_disable_authentication
      KOMMODITY_DEVELOPMENT_MODE                = var.kommodity_container.development_mode
      KOMMODITY_KINE_URI                        = var.kommodity_container.kine_uri
      LOG_FORMAT                                = var.kommodity_container.log_format
      LOG_LEVEL                                 = var.kommodity_container.log_level
      KOMMODITY_OIDC_ISSUER_URL                 = var.oidc_configuration.issuer_url
      KOMMODITY_OIDC_USERNAME_CLAIM             = var.oidc_configuration.username_claim
      KOMMODITY_OIDC_CLIENT_ID                  = var.oidc_configuration.client_id
      KOMMODITY_BASE_URL                        = var.kommodity_container.base_url
      KOMMODITY_ADMIN_GROUP                     = var.oidc_configuration.admin_group
      KOMMODITY_INFRASTRUCTURE_PROVIDERS        = var.kommodity_container.infrastructure_providers
    }

    liveness_probe {
      http_get {
        path = "/livez"
        port = var.kommodity_container.port
      }
      initial_delay_seconds = 10
      period_seconds        = 10
    }

    readiness_probe {
      http_get {
        path = "/readyz"
        port = var.kommodity_container.port
      }
      initial_delay_seconds = 5
      period_seconds        = 10
    }
  }

  diagnostics {
    log_analytics {
      workspace_id  = azurerm_log_analytics_workspace.kommodity-log-analytics.workspace_id
      workspace_key = azurerm_log_analytics_workspace.kommodity-log-analytics.primary_shared_key
    }
  }

  depends_on = [
    azurerm_resource_group.kommodity-resource-group,
    azurerm_subnet.kommodity-aci-sn,
    azurerm_postgresql_flexible_server_database.this,
    azurerm_log_analytics_workspace.kommodity-log-analytics,
  ]
}
