terraform {
  required_providers {
    dataversecontact = {
      source  = "TrueNorthIT/dataversecontact"
      version = "~> 1.0"
    }
  }
}

# Both settings can come from the environment instead:
#   DATAVERSE_CONTACT_API_URL
#   DATAVERSE_CONTACT_CONNECTION_KEY  (the admin key — keep it out of config)
# The connection key must match ADMIN_CONNECTION_KEY on the API deployment.
provider "dataversecontact" {
  api_url = "https://api.dataverse-contact.tnapps.co.uk"
}
