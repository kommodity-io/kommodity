# Kommodity Azure deployment module

## Get started

To use this module, call it as followed in your `main.tf` :

```tf
module "kommodity_azure_deployment" {
  source = "github.com/kommodity-io/kommodity//terraform/modules/kommodity_azure_deployment?ref=<tag>"

  oidc_configuration = {
    issuer_url  = <issuer_url>
    client_id   = <client_id>
    admin_group = <admin_group>
  }
}
```

## Overview

This Terraform module provisions a complete Azure environment for the Kommodity service, including networking, database, logging, and containerized application deployment.

It creates a dedicated resource group with a virtual network and separate subnets for the database and container app. A private DNS zone enables internal name resolution between resources.

The module deploys a **PostgreSQL Flexible Server** in a private subnet with a randomly generated admin password and no public access by default. It also provisions a Log Analytics Workspace for monitoring and diagnostics.

An **Azure Container App** is created to host the Kommodity application. Kommodity is configured with environment variables for authentication, logging, and runtime settings.

## Requirements

| Name | Version |
|------|---------|
| <a name="requirement_azurerm"></a> [azurerm](#requirement\_azurerm) | ~>4.50.0 |
| <a name="requirement_random"></a> [random](#requirement\_random) | ~> 3.7 |

## Providers

| Name | Version |
|------|---------|
| <a name="provider_azurerm"></a> [azurerm](#provider\_azurerm) | 4.50.0 |
| <a name="provider_random"></a> [random](#provider\_random) | 3.7.2 |

## Modules

No modules.

## Resources

| Name | Type |
|------|------|
| [azurerm_container_app.kommodity-app](https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/resources/container_app) | resource |
| [azurerm_container_app_environment.kommodity-environment](https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/resources/container_app_environment) | resource |
| [azurerm_log_analytics_workspace.kommodity-log-analytics](https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/resources/log_analytics_workspace) | resource |
| [azurerm_management_lock.this](https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/resources/management_lock) | resource |
| [azurerm_postgresql_flexible_server.kommodity-db](https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/resources/postgresql_flexible_server) | resource |
| [azurerm_postgresql_flexible_server_database.this](https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/resources/postgresql_flexible_server_database) | resource |
| [azurerm_private_dns_zone.kommodity-dns](https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/resources/private_dns_zone) | resource |
| [azurerm_private_dns_zone_virtual_network_link.kommodity-dns-vnet-link](https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/resources/private_dns_zone_virtual_network_link) | resource |
| [azurerm_resource_group.kommodity-resource-group](https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/resources/resource_group) | resource |
| [azurerm_subnet.kommodity-container-sn](https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/resources/subnet) | resource |
| [azurerm_subnet.kommodity-db-sn](https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/resources/subnet) | resource |
| [azurerm_virtual_network.kommodity-vn](https://registry.terraform.io/providers/hashicorp/azurerm/latest/docs/resources/virtual_network) | resource |
| [random_password.database-password](https://registry.terraform.io/providers/hashicorp/random/latest/docs/resources/password) | resource |

## Inputs

| Name | Description | Type | Default | Required |
|------|-------------|------|---------|:--------:|
| <a name="input_database"></a> [database](#input\_database) | Database configuration | <pre>object({<br/>    storage_tier                  = optional(string, "P4")<br/>    sku_name                      = optional(string, "B_Standard_B1ms")<br/>    storage_mb                    = optional(number, 32768)<br/>    version                       = optional(string, "15")<br/>    public_network_access_enabled = optional(bool, false)<br/>    ha_enabled                    = optional(bool, false)<br/>    storage_georedundant_enabled  = optional(bool, false)<br/>    collation                     = optional(string, "en_US.utf8")<br/>    add_lock                      = optional(bool, false)<br/>  })</pre> | `{}` | no |
| <a name="input_database_password"></a> [database\_password](#input\_database\_password) | Database password configuration | <pre>object({<br/>    length  = number<br/>    special = bool<br/>  })</pre> | <pre>{<br/>  "length": 16,<br/>  "special": false<br/>}</pre> | no |
| <a name="input_kommodity_container"></a> [kommodity\_container](#input\_kommodity\_container) | Kommodity container configuration | <pre>object({<br/>    revision_mode                   = optional(string, "Single")<br/>    image_registry                  = optional(string, "ghcr.io/kommodity-io/kommodity/kommodity")<br/>    image_version                   = optional(string, "latest")<br/>    port                            = optional(number, 5000)<br/>    cpu                             = optional(number, 0.25)<br/>    memory                          = optional(string, "0.5Gi")<br/>    min_replicas                    = optional(number, 1)<br/>    max_replicas                    = optional(number, 1)<br/>    ssl_mode                        = optional(string, "require")<br/>    insecure_disable_authentication = optional(string, "false")<br/>    development_mode                = optional(string, "false")<br/>    kine_uri                        = optional(string, "unix:///tmp/kine.sock")<br/>    log_format                      = optional(string, "console")<br/>    log_level                       = optional(string, "info")<br/>    infrastructure_providers        = optional(string, "") # If env var is empty, Kommodity uses default providers<br/>    base_url                        = optional(string, "")<br/>  })</pre> | `{}` | no |
| <a name="input_log_analytics"></a> [log\_analytics](#input\_log\_analytics) | Log Analytics workspace configuration | <pre>object({<br/>    workspace_sku       = optional(string, "PerGB2018")<br/>    workspace_retention = optional(number, 30)<br/>  })</pre> | `{}` | no |
| <a name="input_oidc_configuration"></a> [oidc\_configuration](#input\_oidc\_configuration) | OIDC configuration | <pre>object({<br/>    issuer_url  = string<br/>    client_id   = string<br/>    admin_group = string<br/>  })</pre> | n/a | yes |
| <a name="input_resource_group"></a> [resource\_group](#input\_resource\_group) | Resource group configuration | <pre>object({<br/>    name     = string<br/>    location = string<br/>  })</pre> | <pre>{<br/>  "location": "North Europe",<br/>  "name": "kommodity"<br/>}</pre> | no |
| <a name="input_virtual_network"></a> [virtual\_network](#input\_virtual\_network) | Virtual network configuration | <pre>object({<br/>    address_space           = optional(string, "10.0.0.0/16")<br/>    database_subnet_prefix  = optional(string, "10.0.2.0/24")<br/>    container_subnet_prefix = optional(string, "10.0.0.0/23")<br/>  })</pre> | `{}` | no |

## Outputs

| Name | Description |
|------|-------------|
| <a name="output_kommodity_app_url"></a> [kommodity\_app\_url](#output\_kommodity\_app\_url) | The URL of the Kommodity Container App |
