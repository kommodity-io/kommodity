# Kommodity OIDC authentication module

## Get started

To use this module, call it as followed in your `main.tf` :

```tf
module "kommodity_oidc_auth" {
  source = "github.com/kommodity-io/kommodity//terraform/modules/kommodity_oidc_auth?ref=<tag>"
}
```

## Overview

This Terraform module creates an Azure AD application and service principal to support OIDC authentication for the Kommodity service.

It registers an Azure AD Application (App Registration) with configurable sign-in audiences, group membership claims, redirect URIs (e.g., for kubectl oidc-login), and permissions to Microsoft Graph based on the provided access list.

It also creates a corresponding Service Principal (Enterprise Application) for the app, enabling it to authenticate and interact with Azure AD resources securely.

## Requirements

| Name | Version |
|------|---------|
| <a name="requirement_azuread"></a> [azuread](#requirement\_azuread) | ~> 2.50 |

## Providers

| Name | Version |
|------|---------|
| <a name="provider_azuread"></a> [azuread](#provider\_azuread) | ~> 2.50 |

## Modules

No modules.

## Resources

| Name | Type |
|------|------|
| [azuread_application.kommodity_oidc_app](https://registry.terraform.io/providers/hashicorp/azuread/latest/docs/resources/application) | resource |
| [azuread_service_principal.kommodity_oidc_sp](https://registry.terraform.io/providers/hashicorp/azuread/latest/docs/resources/service_principal) | resource |

## Inputs

| Name | Description | Type | Default | Required |
|------|-------------|------|---------|:--------:|
| <a name="input_azure_ad_application"></a> [azure\_ad\_application](#input\_azure\_ad\_application) | Azure AD application configuration | <pre>object({<br/>    name             = string<br/>    sign_in_audience = string<br/>  })</pre> | <pre>{<br/>  "name": "kommodity-oidc-app",<br/>  "sign_in_audience": "AzureADMyOrg"<br/>}</pre> | no |
| <a name="input_group_membership_claims"></a> [group\_membership\_claims](#input\_group\_membership\_claims) | Group membership claims configuration for the Azure AD application | `list(string)` | <pre>[<br/>  "SecurityGroup"<br/>]</pre> | no |
| <a name="input_owners"></a> [owners](#input\_owners) | List of owners (object IDs) for the Azure AD application and service principal | `list(string)` | `[]` | no |
| <a name="input_resource_access_list"></a> [resource\_access\_list](#input\_resource\_access\_list) | List of resource access permissions for the Azure AD application | <pre>list(object({<br/>    id   = string<br/>    type = string<br/>  }))</pre> | <pre>[<br/>  {<br/>    "id": "64a6cdd6-aab1-4aaf-94b8-3cc8405e90d0",<br/>    "type": "Scope"<br/>  },<br/>  {<br/>    "id": "7427e0e9-2fba-42fe-b0c0-848c9e6a8182",<br/>    "type": "Scope"<br/>  },<br/>  {<br/>    "id": "37f7f235-527c-4136-accd-4a02d197296e",<br/>    "type": "Scope"<br/>  }<br/>]</pre> | no |

## Outputs

| Name | Description |
|------|-------------|
| <a name="output_application_client_id"></a> [application\_client\_id](#output\_application\_client\_id) | Client ID of the OIDC Application |
