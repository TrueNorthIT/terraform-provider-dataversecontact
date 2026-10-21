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

  # One-time invite links: whoever holds one can do one thing, once, with no
  # account. "create" logs a new case for the contact the invite was sent for
  # (the create_default above binds it); "update" changes only the fields the
  # invite names. Adding a note is invites on the child route (casenotes,
  # below); uploading a document is documents.upload.invites. Set where links
  # open with invite_url on dataversecontact_permissions_sync.
  invites = ["create", "update"]

  # The SharePoint documents in each case's folder, listed with
  # GET /me/case/{id}/documents and uploaded through
  # POST /me/case/{id}/documents/uploads. Both need `casedocuments`, granted in
  # dataversecontact_permissions_sync like a route: "me" lists, "create" uploads.
  #   default_permissions = { case = ["me", "write", "create"], casedocuments = ["me", "create"] }
  # Reading the case isn't enough on its own. Only documents an officer has
  # marked ShowOnPortal in the library are listed.
  documents {
    permission = "casedocuments"
    columns    = { ShowOnPortal = "boolean" } # SharePoint internal name = type
    filters    = ["ShowOnPortal eq true"]
    # folder   = "Public"                    # serve a sub-folder instead

    # Leave upload out to allow no uploads. Every uploaded file is marked
    # ShowOnPortal, so the citizen sees what they sent.
    upload {
      columns = { ShowOnPortal = "true" } # fixed: callers can't change it
      invites = true                      # a one-time invite link may upload one file
      # max_bytes  = 5242880              # default 10 MiB, never above the deployment's cap
      # extensions = ["pdf", "jpg", "png"] # default: common documents and images
    }
  }
}

# A child of case: the notes on it. invites = ["create"] lets an invite sent on
# a case add one note to that case ("please add the photos you mentioned").
# The invitee sets only the writable fields that aren't lookups (subject and
# notetext): the note always goes on the invite's case, through the field whose
# bind_field is parent_table's navigation_property.
resource "dataversecontact_table" "casenotes" {
  scope               = "default"
  route_name          = "casenotes"
  description         = "Notes on the citizen's cases"
  dataverse_table     = "annotations"
  primary_key         = "annotationid"
  required_permission = "casenotes"
  filters             = ["objecttypecode eq 'incident'"]

  default_select = ["annotationid", "subject", "notetext", "createdon"]
  lookup_fields  = ["subject"]

  invites = ["create"]

  parent_table {
    table               = "case"
    navigation_property = "objectid_incident"
  }

  # me: note -> its case -> the case's citizen.
  contact_join_step {
    table = "incidents"
    from  = "objectid_incident"
    key   = "incidentid"
  }
  contact_join_step {
    table = "contacts"
    from  = "customerid_contact"
    key   = "contactid"
  }

  fields = {
    annotationid = { type = "string", description = "Note id.", read_only = true }
    subject      = { type = "string", description = "Note title." }
    notetext     = { type = "string", description = "Note text." }
    objectid     = { type = "lookup", description = "The case.", lookup_table = "case", bind_field = "objectid_incident" }
    createdon    = { type = "datetime", description = "Added on.", read_only = true }
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

# Emails the signed-in contact was on, with their attachments as files.
#
# The contact is reached through the email's activity parties (a reverse
# step), and `match` counts a party only as From, To, CC or BCC. Without it,
# the Regarding party Dataverse adds for the record an email is filed against
# would make an internal email "one the contact was on".
#
# Attachments are rows of activitymimeattachments. Callers list them with
# GET /me/emails/{id}/files and download one through a short-lived link from
# POST /me/emails/{id}/files/{fileId}/link; the content is never inline.
resource "dataversecontact_table" "emails" {
  scope                  = "default"
  route_name             = "emails"
  dataverse_table        = "emails"
  dataverse_logical_name = "email"
  primary_key            = "activityid"
  required_permission    = "emails"
  default_select         = ["activityid", "subject", "sender", "torecipients", "senton", "attachmentcount"]
  lookup_fields          = ["subject"]
  filters                = ["statecode eq 1"] # sent or received, never drafts

  contact_join_step {
    table   = "activityparties"
    from    = "email_activity_parties"
    key     = "activitypartyid"
    reverse = true
    match   = { participationtypemask = [1, 2, 3, 4] } # sender, to, cc, bcc
  }
  contact_join_step {
    table = "contacts"
    from  = "partyid_contact"
    key   = "contactid"
  }

  files = {
    table         = "activitymimeattachments"
    primary_key   = "activitymimeattachmentid"
    parent_column = "objectid"
    content       = "body"
    file_name     = "filename"
    mime_type     = "mimetype"
    size          = "filesize"
  }

  fields = {
    activityid      = { type = "string", description = "Email id", read_only = true }
    subject         = { type = "string", description = "Subject", read_only = true }
    description     = { type = "string", description = "Body (HTML; render it sanitised)", read_only = true }
    sender          = { type = "string", description = "From address", read_only = true }
    torecipients    = { type = "string", description = "To addresses", read_only = true }
    senton          = { type = "datetime", description = "Sent on", read_only = true }
    attachmentcount = { type = "number", description = "Number of attachments", read_only = true }
  }
}

# A note's own attachment: the row is the file, so only the columns are named.
# Its content column (documentbody) is removed from the table's fields.
#   files = { content = "documentbody", file_name = "filename", mime_type = "mimetype", size = "filesize" }
