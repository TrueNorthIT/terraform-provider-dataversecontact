# Registers a Dataverse Custom API as an invokable route. The schema is the
# CustomApiHint JSON — inline via jsonencode, or file("....customapi.json").
resource "dataversecontact_custom_api" "expand_calendar" {
  scope      = "default"
  route_name = "expand-calendar"

  schema_json = jsonencode({
    routeName              = "expand-calendar"
    dataverseUniqueName    = "ExpandCalendar"
    requiredPermission     = "expand-calendar:invoke"
    isFunction             = true # GET (function) rather than POST (action)
    publicInvoke           = true # callable without authentication
    bindingType            = "entity"
    boundEntityLogicalName = "calendar"
    boundEntitySetName     = "calendars"
    requestParameters = [
      { uniqueName = "Start", type = "datetime" },
      { uniqueName = "End", type = "datetime" },
    ]
  })
}

# A custom API acting on the caller's own case: answers on
# /me/actions/close-incident/{caseId} and /team/actions/close-incident/{caseId}.
# The case id in the URL is checked against the "case" route's scoping and
# written into IncidentResolution.incidentid@odata.bind. Needs the API release
# with tiered custom APIs.
resource "dataversecontact_custom_api" "close_incident" {
  scope      = "default"
  route_name = "close-incident"

  schema_json = jsonencode({
    routeName           = "close-incident"
    dataverseUniqueName = "CloseIncident"
    isFunction          = false
    bindingType         = "global"
    tiers               = ["me", "team"]
    target = {
      table        = "case"
      recordIdPath = "IncidentResolution.incidentid@odata.bind"
      filters      = ["statecode eq 0"]
    }
    requestParameters = [
      { uniqueName = "IncidentResolution", type = "entity" },
      { uniqueName = "Status", type = "integer" },
    ]
  })
}
