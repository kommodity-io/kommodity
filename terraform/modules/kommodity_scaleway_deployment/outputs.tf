output "kommodity_domainname" {
    description = "The domain name of the Kommodity deployment"
    value       = scaleway_container.kommodity_container.domain_name
}
