output "app_url" {
  value       = "https://${azurerm_container_app.kommodity-app.ingress[0].fqdn}"
  description = "The URL of the Kommodity Container App"
}

output "container_app_id" {
  value       = azurerm_container_app.kommodity-app.id
  description = "The ID of the Kommodity Container App"
}

output "custom_domain_verification_id" {
  value       = azurerm_container_app.kommodity-app.custom_domain_verification_id
  description = "The Custom Domain Verification ID of the Kommodity Container App"
}
