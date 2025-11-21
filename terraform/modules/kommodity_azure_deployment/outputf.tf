output "kommodity_app_url" {
  value       = "https://${azurerm_container_app.kommodity-app.ingress[0].fqdn}"
  description = "The URL of the Kommodity Container App"
}
