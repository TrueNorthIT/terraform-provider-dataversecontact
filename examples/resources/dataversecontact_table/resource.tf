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

# A polymorphic family: publish every target of a lookup as a route, without
# declaring one dataversecontact_table per target.
#
# Service Builder emits one Dataverse table per service and points
# incident.sb_service_recordid at it, so the set grows whenever a service
# ships. Declaring the RULE means a new service is readable the moment its
# table exists — there is no generated table list to regenerate, and nothing
# to remember after a publish.
resource "dataversecontact_table" "case_with_service_answers" {
  scope                  = "fcc"
  route_name             = "case"
  dataverse_table        = "incidents"
  dataverse_logical_name = "incident"
  primary_key            = "incidentid"
  required_permission    = "case"

  default_select = ["incidentid", "title", "sb_service_recordid"]
  lookup_fields  = ["title"]

  fields = {
    title = { type = "string", description = "Case title." }
    # The rule's field must also be declared as a lookup here, or
    # ?expand=sb_service_recordid cannot be rewritten onto the concrete
    # targets — it fails by returning nothing, not by erroring.
    #
    # It deliberately carries NO lookup_table, and must not be given one: a
    # polymorphic lookup points at many tables, so naming one would be a claim
    # the data contradicts. Dataverse says which target a given row used, and
    # the API surfaces that as sb_service_recordid_logicalname. The provider
    # exempts a field named by polymorphic_lookup from the lookup_table rule
    # for exactly this reason.
    sb_service_recordid = {
      type        = "lookup"
      description = "Your submitted answers."
      read_only   = true
    }
  }

  # Derived routes inherit this, with the reverse hop into `incidents`
  # prefixed — so they need no join of their own.
  contact_join_step {
    table = "contacts"
    from  = "customerid_contact"
    key   = "contactid"
  }

  polymorphic_lookup {
    field               = "sb_service_recordid"
    required_permission = "servicerecord"
    route_prefix_strip  = "sb_" # sb_missed_bin → GET /me/missed_bin/{id}
    exclude_targets     = ["sb_service_request"]

    # Each service table has its own business process flow. With this, every
    # case row carries `progress` — the stage its service record is in — or
    # null. One extra Dataverse read per page, never one per row. For a
    # process on the table ITSELF, set `business_process` on the resource.
    business_process = { expose_as = "progress" }
  }
}

# One entry covers the whole family. The API fans `servicerecord` out onto
# every derived route, because default_permissions is keyed by ROUTE name and
# a key naming only the permission would match no table and grant nothing.
resource "dataversecontact_permissions_sync" "fcc" {
  scope = "fcc"

  default_permissions = {
    case          = ["me", "write", "create"]
    servicerecord = ["me"]
  }

  depends_on = [dataversecontact_table.case_with_service_answers]
}
