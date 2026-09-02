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
