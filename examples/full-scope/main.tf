# A complete scope, end to end: a citizen-owned parent table, an ownerless
# child scoped through it (see the "Parent-scoped children" guide), a custom
# API, and the mandatory permissions publish. Copy this to start a new scope.

terraform {
  required_providers {
    dataversecontact = {
      source  = "TrueNorthIT/dataversecontact"
      version = "~> 0.1"
    }
  }
}

provider "dataversecontact" {
  api_url        = var.api_url
  connection_key = var.connection_key
}

variable "api_url" {
  type = string
}

# Must match ADMIN_CONNECTION_KEY on the API deployment. Prefer setting it
# via the DATAVERSE_CONTACT_CONNECTION_KEY environment variable.
variable "connection_key" {
  type      = string
  sensitive = true
}

variable "scope" {
  type    = string
  default = "booking"
}

# ── The citizen ─────────────────────────────────────────────────────────────

resource "dataversecontact_table" "citizen" {
  scope           = var.scope
  route_name      = "citizen"
  description     = "Citizen contact records"
  dataverse_table = "contacts"
  primary_key     = "contactid"

  default_select = ["contactid", "fullname", "emailaddress1"]
  lookup_fields  = ["fullname"]

  contact_join_step {
    table = "contacts"
    from  = "contactid"
    key   = "contactid"
  }

  fields = {
    contactid = {
      type        = "string"
      description = "Unique citizen identifier."
      read_only   = true
    }
    fullname = {
      type        = "string"
      description = "Full name."
      read_only   = true
    }
    firstname = {
      type        = "string"
      description = "First name."
    }
    lastname = {
      type        = "string"
      description = "Last name."
    }
    emailaddress1 = {
      type        = "string"
      description = "Primary email address."
    }
  }
}

# ── Owned parent: the citizen's service booking ─────────────────────────────
# create_default binds tn_Citizen to the caller, so a citizen can only book
# for themselves. The underlying venue booking (below) is created by nesting
# it inside this parent's create — authorised by the parent's permission.

resource "dataversecontact_table" "servicebooking" {
  scope           = var.scope
  route_name      = "servicebooking"
  description     = "Citizen service booking"
  dataverse_table = "tn_citizenservicebookings"
  primary_key     = "tn_citizenservicebookingid"

  default_select = ["tn_citizenservicebookingid", "tn_name", "tn_requestedstart", "tn_requestedend"]
  lookup_fields  = ["tn_name"]

  contact_join_step {
    table = "contacts"
    from  = "tn_Citizen"
    key   = "contactid"
  }

  create_default {
    field      = "tn_Citizen"
    bind_to    = "contact"
    entity_set = "contacts"
  }

  expand {
    lookup_field  = "tn_Booking"
    related_table = "bookableresourcebooking"

    field {
      name        = "starttime"
      type        = "datetime"
      description = "Start time."
    }
    field {
      name        = "endtime"
      type        = "datetime"
      description = "End time."
    }
  }

  fields = {
    tn_citizenservicebookingid = {
      type        = "string"
      description = "Unique service booking identifier."
      read_only   = true
    }
    tn_name = {
      type        = "string"
      description = "Booking name."
    }
    tn_requestedstart = {
      type        = "datetime"
      description = "Requested start time."
    }
    tn_requestedend = {
      type        = "datetime"
      description = "Requested end time."
    }
    tn_citizen = {
      type         = "lookup"
      description  = "Citizen who made the booking."
      lookup_table = "citizen"
      bind_field   = "tn_Citizen"
    }
    tn_booking = {
      type         = "lookup"
      description  = "Underlying venue booking."
      lookup_table = "booking"
      bind_field   = "tn_Booking"
    }
  }
}

# ── Ownerless child: the venue slot ─────────────────────────────────────────
# No contact column of its own, so /me scoping walks the parent's lookup
# BACKWARDS (reverse = true, `from` = the relationship's collection nav),
# then forwards to the contact.

resource "dataversecontact_table" "booking" {
  scope           = var.scope
  route_name      = "booking"
  description     = "Venue time-slot bookings"
  dataverse_table = "bookableresourcebookings"
  primary_key     = "bookableresourcebookingid"

  default_select = ["bookableresourcebookingid", "name", "starttime", "endtime"]
  lookup_fields  = ["name"]

  contact_join_step {
    table   = "tn_citizenservicebookings"
    from    = "tn_booking_csb" # relationship's collection-valued navigation property
    key     = ""
    reverse = true
  }
  contact_join_step {
    table = "contacts"
    from  = "tn_Citizen"
    key   = "contactid"
  }

  fields = {
    bookableresourcebookingid = {
      type        = "string"
      description = "Unique booking identifier."
      read_only   = true
    }
    name = {
      type        = "string"
      description = "Booking name."
    }
    starttime = {
      type        = "datetime"
      description = "Start time."
    }
    endtime = {
      type        = "datetime"
      description = "End time."
    }
  }
}

# ── Custom API ──────────────────────────────────────────────────────────────

resource "dataversecontact_custom_api" "expand_calendar" {
  scope      = var.scope
  route_name = "expand-calendar"

  schema_json = jsonencode({
    routeName              = "expand-calendar"
    dataverseUniqueName    = "ExpandCalendar"
    requiredPermission     = "expand-calendar:invoke"
    isFunction             = true
    publicInvoke           = true
    bindingType            = "entity"
    boundEntityLogicalName = "calendar"
    boundEntitySetName     = "calendars"
    requestParameters = [
      { uniqueName = "Start", type = "datetime" },
      { uniqueName = "End", type = "datetime" },
    ]
  })
}

# ── Baseline permissions — mandatory ────────────────────────────────────────
# A scope with no published defaults grants nothing: every route answers
# "403 Missing required permission", including reads.

resource "dataversecontact_permissions_sync" "this" {
  scope = var.scope

  allow_self_register = true

  default_permissions = {
    servicebooking = ["me", "write", "create"]
    citizen        = ["me"]
    booking        = ["me", "write"]
  }

  triggers = {
    tables_hash = sha256(join(",", [
      dataversecontact_table.citizen.id,
      dataversecontact_table.servicebooking.id,
      dataversecontact_table.booking.id,
    ]))
    apis_hash = sha256(dataversecontact_custom_api.expand_calendar.id)
  }

  depends_on = [
    dataversecontact_table.citizen,
    dataversecontact_table.servicebooking,
    dataversecontact_table.booking,
    dataversecontact_custom_api.expand_calendar,
  ]
}

# ── Read back what's published ──────────────────────────────────────────────

data "dataversecontact_scopes" "all" {}

output "scopes" {
  value = data.dataversecontact_scopes.all.scopes
}

output "published_tables" {
  value = [
    dataversecontact_table.citizen.id,
    dataversecontact_table.servicebooking.id,
    dataversecontact_table.booking.id,
  ]
}
