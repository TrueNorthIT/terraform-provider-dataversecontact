terraform {
  required_providers {
    dataversecontact = {
      source  = "TrueNorthIT/dataversecontact"
      version = "~> 1.8"
    }
  }
}

# api_url can come from DATAVERSE_CONTACT_API_URL instead.
#
# Signing in: with DATAVERSE_CONTACT_CONNECTION_KEY set (the API's
# ADMIN_CONNECTION_KEY), the provider uses the key. Without it, the provider
# uses your az login, or in a pipeline the ARM_USE_OIDC / ARM_CLIENT_ID /
# ARM_ADO_PIPELINE_SERVICE_CONNECTION_ID variables azurerm uses.
provider "dataversecontact" {
  api_url = "https://api.dataverse-contact.tnapps.co.uk"
}
