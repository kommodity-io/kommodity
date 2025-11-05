output "kommodity_app_url" {
  value       = azurerm_container_app.kommodity-app.latest_revision_fqdn
  description = "The URL of the Kommodity Container App"
}
