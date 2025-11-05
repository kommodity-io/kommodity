# Example deployment of Kommodity on Azure using OIDC authentication

This Terraform configuration ties together two Kommodity modules to deploy the application on Azure with OIDC authentication.

It first retrieves the current Azure AD client context and uses it to deploy the kommodity_oidc_auth module, which sets up an Azure AD application for OIDC.
Then it deploys the kommodity_azure_deployment module, which provisions the full Azure infrastructure for Kommodity and configures it to use the previously created OIDC app for authentication.
