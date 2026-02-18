# Resource Group
resource "azurerm_resource_group" "kommodity-resource-group" {
  name     = var.resource_group.name
  location = var.resource_group.location
}

# Networking resources for DB
resource "azurerm_virtual_network" "kommodity-vn" {
  name                = "${var.resource_group.name}-vn"
  location            = azurerm_resource_group.kommodity-resource-group.location
  resource_group_name = azurerm_resource_group.kommodity-resource-group.name
  address_space       = ["${var.virtual_network.address_space}"]

  depends_on = [azurerm_resource_group.kommodity-resource-group]
}

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

resource "azurerm_private_dns_zone" "kommodity-dns" {
  name                = "${var.resource_group.name}.postgres.database.azure.com"
  resource_group_name = azurerm_resource_group.kommodity-resource-group.name

  depends_on = [azurerm_resource_group.kommodity-resource-group]
}

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

# Database
resource "random_password" "database-password" {
  length  = var.database_password.length
  special = var.database_password.special
}

resource "azurerm_management_lock" "this" {
  count = var.database.add_lock ? 1 : 0

  name       = "${azurerm_postgresql_flexible_server.kommodity-db.name}-lock"
  scope      = azurerm_postgresql_flexible_server.kommodity-db.id
  lock_level = "CanNotDelete"
  notes      = "Protect accidental deletion of PostgreSQL database resources"
}

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

# Log Analytics Workspace for Container Apps
resource "azurerm_log_analytics_workspace" "kommodity-log-analytics" {
  name                = "${var.resource_group.name}-log-analytics"
  location            = azurerm_resource_group.kommodity-resource-group.location
  resource_group_name = azurerm_resource_group.kommodity-resource-group.name
  sku                 = var.log_analytics.workspace_sku
  retention_in_days   = var.log_analytics.workspace_retention

  depends_on = [azurerm_resource_group.kommodity-resource-group]
}

# Networking resources for Container App
resource "azurerm_subnet" "kommodity-container-sn" {
  name                 = "${var.resource_group.name}-container-sn"
  resource_group_name  = azurerm_resource_group.kommodity-resource-group.name
  virtual_network_name = azurerm_virtual_network.kommodity-vn.name
  address_prefixes     = ["${var.virtual_network.container_subnet_prefix}"]

  delegation {
    name = "Microsoft.App.environments"
    service_delegation {
      name    = "Microsoft.App/environments"
      actions = ["Microsoft.Network/virtualNetworks/subnets/join/action"]
    }
  }

  depends_on = [
    azurerm_resource_group.kommodity-resource-group,
    azurerm_virtual_network.kommodity-vn,
  ]
}

resource "azurerm_container_app_environment" "kommodity-environment" {
  name                       = "${var.resource_group.name}-environment"
  location                   = azurerm_resource_group.kommodity-resource-group.location
  resource_group_name        = azurerm_resource_group.kommodity-resource-group.name
  log_analytics_workspace_id = azurerm_log_analytics_workspace.kommodity-log-analytics.id
  infrastructure_subnet_id   = azurerm_subnet.kommodity-container-sn.id

  depends_on = [
    azurerm_resource_group.kommodity-resource-group,
    azurerm_log_analytics_workspace.kommodity-log-analytics,
    azurerm_subnet.kommodity-container-sn,
  ]

  lifecycle {
    ignore_changes = [infrastructure_resource_group_name, workload_profile]
  }
}

# Container App for kommodity service
resource "azurerm_container_app" "kommodity-app" {
  depends_on = [
    azurerm_resource_group.kommodity-resource-group,
    azurerm_container_app_environment.kommodity-environment,
    azurerm_postgresql_flexible_server_database.this,
  ]
  name                         = "${var.resource_group.name}-app"
  container_app_environment_id = azurerm_container_app_environment.kommodity-environment.id
  resource_group_name          = azurerm_resource_group.kommodity-resource-group.name
  revision_mode                = var.kommodity_container.revision_mode

  ingress {
    external_enabled = true
    target_port      = var.kommodity_container.port
    transport        = "http2"
    # traffic_weight block only applies when revision_mode is set to Multiple
    traffic_weight {
      percentage      = 100
      latest_revision = true
    }
  }

  template {
    min_replicas = var.kommodity_container.min_replicas
    max_replicas = var.kommodity_container.max_replicas
    container {
      name   = "${var.resource_group.name}-container"
      image  = "${var.kommodity_container.image_registry}:${var.kommodity_container.image_version}"
      cpu    = var.kommodity_container.cpu
      memory = var.kommodity_container.memory

      env {
        name  = "KOMMODITY_DB_URI"
        value = "postgres://${azurerm_postgresql_flexible_server.kommodity-db.administrator_login}:${random_password.database-password.result}@${azurerm_postgresql_flexible_server.kommodity-db.fqdn}:5432/kommodity?sslmode=${var.kommodity_container.ssl_mode}"
      }
      env {
        name  = "KOMMODITY_PORT"
        value = var.kommodity_container.port
      }
      env {
        name  = "KOMMODITY_INSECURE_DISABLE_AUTHENTICATION"
        value = var.kommodity_container.insecure_disable_authentication
      }
      env {
        name  = "KOMMODITY_DEVELOPMENT_MODE"
        value = var.kommodity_container.development_mode
      }
      env {
        name  = "KOMMODITY_KINE_URI"
        value = var.kommodity_container.kine_uri
      }
      env {
        name  = "LOG_FORMAT"
        value = var.kommodity_container.log_format
      }
      env {
        name  = "LOG_LEVEL"
        value = var.kommodity_container.log_level
      }
      env {
        name  = "KOMMODITY_OIDC_ISSUER_URL"
        value = var.oidc_configuration.issuer_url
      }
      env {
        name  = "KOMMODITY_OIDC_USERNAME_CLAIM"
        value = var.oidc_configuration.username_claim
      }
      env {
        name  = "KOMMODITY_OIDC_CLIENT_ID"
        value = var.oidc_configuration.client_id
      }
      env {
        name  = "KOMMODITY_BASE_URL"
        value = var.kommodity_container.base_url
      }
      env {
        name  = "KOMMODITY_ADMIN_GROUP"
        value = var.oidc_configuration.admin_group
      }
      env {
        name  = "KOMMODITY_INFRASTRUCTURE_PROVIDERS"
        value = var.kommodity_container.infrastructure_providers
      }

      liveness_probe {
        transport = "HTTP"
        port      = var.kommodity_container.port
        path      = "/livez"
      }

      readiness_probe {
        transport = "HTTP"
        port      = var.kommodity_container.port
        path      = "/readyz"
      }
    }
  }

  lifecycle {
    ignore_changes = [workload_profile_name]
  }
}
