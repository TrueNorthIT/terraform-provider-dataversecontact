terraform {
  required_providers {
    dataversecontact = {
      source = "tnapps/dataversecontact"
    }
  }
}

provider "dataversecontact" {
  api_url        = "https://api.dataverse-contact.tnapps.co.uk"
  connection_key = var.connection_key
}

# The admin connection key. This must match the ADMIN_CONNECTION_KEY
# configured on the API deployment.
variable "connection_key" {
  type      = string
  sensitive = true
}
