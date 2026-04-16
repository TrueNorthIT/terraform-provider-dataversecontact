terraform {
  required_providers {
    dataversecontact = {
      source = "tnapps/dataversecontact"
    }
  }
}

provider "dataversecontact" {
  api_url = "https://acme-api.vercel.app"
  api_key = var.admin_api_key
}

variable "admin_api_key" {
  type      = string
  sensitive = true
}
