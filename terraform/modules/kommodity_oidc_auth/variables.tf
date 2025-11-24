variable "azure_ad_application" {
  type = object({
    name                    = optional(string, "kommodity-oidc-app")
    sign_in_audience        = optional(string, "AzureADMyOrg")
    group_membership_claims = optional(list(string), ["SecurityGroup"])
  })
  description = "Azure AD application configuration"
  default     = {}
}

variable "resource_access_list" {
  description = "List of resource access permissions for the Azure AD application"
  type = list(object({
    id   = string
    type = string
  }))
  default = [
    {
      id   = "64a6cdd6-aab1-4aaf-94b8-3cc8405e90d0" # email
      type = "Scope"
    },
    {
      id   = "7427e0e9-2fba-42fe-b0c0-848c9e6a8182" # offline_access
      type = "Scope"
    },
    {
      id   = "37f7f235-527c-4136-accd-4a02d197296e" # openid
      type = "Scope"
    }
  ]
}

variable "owners" {
  description = "List of owners (object IDs) for the Azure AD application and service principal"
  type        = list(string)
  default     = []
}

