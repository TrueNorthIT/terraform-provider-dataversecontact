# A citizen-owned table: cases raised by the signed-in citizen.
resource "dataversecontact_table" "case" {
  scope       = "default"
  route_name  = "case"
  description = "Citizen service cases"

  dataverse_table = "incidents" # OData entity set name
  # dataverse_logical_name omitted -> derived by singularizing: "incident"
  # required_permission omitted    -> defaults to route_name: "case"
  # filters omitted                -> defaults to ["statecode eq 0"]
  primary_key = "incidentid"

  default_select = ["title", "ticketnumber", "createdon", "statuscode"]
  lookup_fields  = ["title", "ticketnumber"]

  fields = {
    title = {
      type        = "string"
      description = "Case title."
    }
    ticketnumber = {
      type        = "string"
      description = "Case reference shown to the citizen."
      read_only   = true
    }
    createdon = {
      type        = "datetime"
      description = "When the case was raised."
      read_only   = true
    }
    new_iscomplex = {
      type        = "boolean"
      description = "Complex-case flag."
    }
    new_estimatedcost = {
      type        = "number"
      description = "Estimated cost."
    }
    statuscode = {
      type        = "choice"
      description = "Case status."
    }
    customerid = {
      type         = "lookup"
      description  = "The citizen this case belongs to."
      lookup_table = "contact"            # route name of the target table
      value_field  = "_customerid_value"  # polymorphic lookup's raw value column
      bind_field   = "customerid_contact" # navigation property for @odata.bind
    }
  }

  # How /me row-scoping walks from this table back to the caller's contact.
  contact_join_step {
    table = "contacts"
    from  = "customerid_contact"
    key   = "contactid"
  }

  # Return a couple of contact fields inline when the client expands the lookup.
  expand {
    lookup_field  = "customerid_contact"
    related_table = "contacts"

    field {
      name        = "fullname"
      type        = "string"
      description = "Citizen name."
    }
    field {
      name        = "emailaddress1"
      type        = "string"
      description = "Citizen email."
    }
  }

  # Bind the citizen automatically on create, so a caller can only ever
  # create cases owned by themselves.
  create_default {
    field      = "customerid_contact"
    bind_to    = "contact"
    entity_set = "contacts"
  }
}

# A public catalog table: readable without authentication, no citizen owner.
resource "dataversecontact_table" "service" {
  scope           = "default"
  route_name      = "service"
  description     = "Service categories citizens browse"
  dataverse_table = "bookableresourcecategories"
  primary_key     = "bookableresourcecategoryid"
  public_read     = true

  aliases = ["services", "category", "categories"]

  default_select = ["bookableresourcecategoryid", "name", "description"]
  lookup_fields  = ["name"]

  fields = {
    bookableresourcecategoryid = {
      type        = "string"
      description = "Unique service identifier."
      read_only   = true
    }
    name = {
      type        = "string"
      description = "Service name."
    }
    description = {
      type        = "string"
      description = "Service description."
    }
  }
}
