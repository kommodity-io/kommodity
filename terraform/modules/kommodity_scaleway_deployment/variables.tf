# Scaleway variables
variable "scaleway" {
  type        = object({
    project_id = string # Scaleway project ID
    region     = string # Scaleway region
  })
  description = "The Scaleway project and region configuration"
}

# DB variables
variable "database" {
  type        = object({
    user                = string # username for the database user
    password            = string # password for the database user
    private_network_ip  = string # private network IP for the database
    name                = string # name of the database
    port                = number # port for the database
    node_type           = string # node type for the database
  })
  description = "The username for the database user"
  default = {
    user                = "kommodity"
    password            = "Kommod1ty!"
    private_network_ip  = "172.16.20.4/22"
    name                = "kommodity-pg-database"
    port                = 5432
    node_type           = "db-dev-s"
  }
}

# Network
variable "private_network_subnet" {
  type        = string
  description = "The Subnet for the private network"
  default     = "172.16.20.0/22"
}

# Kommodity
variable "kommodity" {
  type        = object({
    image_registry = string # registry for the kommodity container image
    image_version  = string # version for the kommodity container image
    container_port = number # port for the kommodity container
  })
  description = "The configuration for the kommodity container"
  default = {
    image_registry = "ghcr.io/kommodity-io/kommodity/kommodity"
    image_version  = "latest"
    container_port = 8000
  }
}
