# Kommodity Scaleway Deployment Terraform Module

This Terraform module provisions the infrastructure required to deploy the Kommodity service on Scaleway. It automates the creation and configuration of:

- A private VPC network
- A PostgreSQL database instance with private networking
- A container namespace and Kommodity container deployment
- All necessary networking, database, and container environment variables

## Usage

Refer to the module's `variables.tf` for all configurable options. See the example in your root manifest for how to call this module.

## Scaleway provider configuration

Use the [Scaleway CLI](https://www.scaleway.com/en/cli/) to authenticate to your Scaleway account.

Make sure you have generated [API keys](https://console.scaleway.com/iam/api-keys).

Once you are authenticated with the CLI, the following values will be populated in your `$HOME/.config/scw/config.yaml`:

- access_key
- secret_key
- region
- zone
- project_id
- organization_id

Terraform will use those values by default. They can be overidden in different ways. See the [documentation](https://registry.terraform.io/providers/scaleway/scaleway/latest/docs#authentication) for more information.
