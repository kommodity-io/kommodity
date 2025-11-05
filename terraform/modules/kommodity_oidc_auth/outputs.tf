# Outputs
output "application_client_id" {
  value       = azuread_application.kommodity_oidc_app.client_id
  description = "Client ID of the OIDC Application"
}
