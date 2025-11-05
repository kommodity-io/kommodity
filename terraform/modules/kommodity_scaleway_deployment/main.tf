# Private network
resource "scaleway_vpc_private_network" "pn" {
  ipv4_subnet {
    subnet = var.private_network_subnet
  }
}

# DB
resource "scaleway_rdb_instance" "kommodity_db" {
  name           = var.database.name
  node_type      = var.database.node_type
  engine         = "PostgreSQL-15"
  is_ha_cluster  = true
  disable_backup = true
  user_name      = var.database.user
  password       = var.database.password
  private_network {
    pn_id  = scaleway_vpc_private_network.pn.id
    ip_net = var.database.private_network_ip
    # enable_ipam = false
  }
  # lifecycle {
  #   prevent_destroy = true
  # }
}

# Containers for kommodity and kine services
resource "scaleway_container_namespace" "main" {
  project_id  = var.scaleway.project_id
  region      = var.scaleway.region
  name        = "kommodity"
}

resource "scaleway_container" "kommodity_container" {
  depends_on = [
    scaleway_rdb_instance.kommodity_db,
  ]
  name           = "kommodity-container"
  description    = "Container for the kommodity service"
  namespace_id   = scaleway_container_namespace.main.id
  registry_image = "${var.kommodity.image_registry}:${var.kommodity.image_version}"
  port           = var.kommodity.container_port
  min_scale      = 1
  max_scale      = 1
  privacy        = "public"
  deploy         = true
  private_network_id = scaleway_vpc_private_network.pn.id
  health_check {
    http {
      path = "/healthz"
    }
    failure_threshold = 5
    interval          = "5s"
  }

  environment_variables = {
    "KOMMODITY_DB_URI" = "postgres://${var.database.user}:${var.database.password}@${scaleway_rdb_instance.kommodity_db.private_network[0].ip}:${var.database.port}/kommodity?sslmode=require"
    "KOMMODITY_PORT" = "${var.kommodity.container_port}"
    "KOMMODITY_INSECURE_DISABLE_AUTHENTICATION" = "true"
    "KOMMODITY_DEVELOPMENT_MODE" = "false"
    "KOMMODITY_KINE_URI" = "unix:///tmp/kine.sock"
    "LOG_FORMAT" = "console"
    "LOG_LEVEL" = "info"
  }

  timeouts {
    create = "15m"
    update = "15m"
  }
}
