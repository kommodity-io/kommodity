output "app_url" {
  value       = "https://${azurerm_public_ip.kommodity-appgw-pip.ip_address}"
  description = "The public HTTPS endpoint for Kommodity"
}

output "appgw_public_ip" {
  value       = azurerm_public_ip.kommodity-appgw-pip.ip_address
  description = "The public IP address of the Application Gateway (for DNS configuration)"
}

output "aci_private_ip" {
  value       = azurerm_container_group.kommodity-aci.ip_address
  description = "The private IP address of the ACI container group (for direct VNET access)"
}

output "application_gateway_id" {
  value       = azurerm_application_gateway.kommodity-appgw.id
  description = "The ID of the Application Gateway"
}
