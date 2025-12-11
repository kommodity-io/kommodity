output "kommodity_app_url" {
  value       = "https://${azurerm_container_app.kommodity-app.ingress[0].fqdn}"
  description = "The URL of the Kommodity Container App"
}

output "kommodity_container_app_id" {
  value       = azurerm_container_app.kommodity-app.id
  description = "The ID of the Kommodity Container App"
}
