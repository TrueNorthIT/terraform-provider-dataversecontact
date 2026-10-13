---
page_title: "Custom APIs on the me, team, all and public tiers"
subcategory: ""
description: |-
  How a dataversecontact_custom_api definition decides who may call a Dataverse function or action, and on which record.
---

# Custom APIs on the me, team, all and public tiers

`dataversecontact_custom_api` publishes a Dataverse custom API (a function or
an action, bound or unbound) as a route of the Contact API. The definition is
the `schema_json` you pass; the provider sends it as is, so everything below is
a property of that JSON.

-> **Requires the API release with tiered custom APIs.** A definition that uses
`tiers`, `target` or `callerParameters` must only be applied once every API
deployment reading the scope runs that release. An older deployment reads such
a definition as a legacy API with **no ownership check**.

## Routes

Callers reach a custom API on the tier the URL names. `GET` calls a function
(inputs in the query string), `POST` calls an action (inputs in a JSON body).

| Tier | URL | Permission | The record must |
|---|---|---|---|
| `public` | `/api/v2/{scope}/public/actions/{name}[/{id}]` | none, no sign-in | — |
| `me` | `/api/v2/{scope}/me/actions/{name}[/{id}]` | `{name}:invoke` | belong to the caller's contact |
| `team` | `/api/v2/{scope}/team/actions/{name}[/{id}]` | `{name}:invoke:team` | belong to the caller's company account |
| `all` | `/api/v2/{scope}/all/actions/{name}[/{id}]` | `{name}:invoke:all` | exist and pass `target.filters` |

`tiers` lists which of these URLs exist; any other tier answers 404. Grant the
permissions in `dataversecontact_permissions_sync.default_permissions`, keyed by
the route name — for example `close-incident = ["invoke", "invoke:team"]`.

The caller always names the record in the URL. A bound API runs on it; an
unbound one has its id written into the parameter `target.recordIdPath` names.
Either way the record Dataverse acts on is the record the API checked.

## The binding comes from Dataverse

Whether the API is a function or an action, whether it's bound, and to which
table, are read from Dataverse's `customapis` table when the definition is
saved. Leave `isFunction`, `bindingType`, `boundEntityLogicalName` and
`boundEntitySetName` out; a definition that states one of them differently from
Dataverse fails the apply with a 400 naming the field. A bound API is called on
its `target` table's entity set, which must be the table Dataverse binds it to;
with no target the set is read from the table's metadata.

Built-in messages (`CloseIncident`, `ExpandCalendar`) and classic process
actions aren't in `customapis`, so their definitions still declare `isFunction`
and `bindingType` — plus `boundEntitySetName`, or a `target`, when bound. Even
then a wrong entity set can't run the API on another table: Dataverse answers a
bound call on the wrong set with a 404.

The provider doesn't refresh `schema_json` from the server, so the fields the
API fills in cause no drift. The computed `is_function` and `binding_type`
attributes show what Dataverse says.

## Shapes

### Bound to a record

`target.table` names a published table route in the same scope, and its
contact and account scoping is reused. It must be a route on the table
Dataverse binds the API to — `tn_EscalateCase` below is bound to `incident`, and
the `case` route is on `incidents`.

```terraform
resource "dataversecontact_custom_api" "escalate_case" {
  scope      = var.scope
  route_name = "escalate-case"

  schema_json = jsonencode({
    routeName           = "escalate-case"
    dataverseUniqueName = "tn_EscalateCase"
    tiers               = ["me", "team", "all"]
    target              = { table = "case" }
  })
}
```

Called as `POST /api/v2/{scope}/me/actions/escalate-case/{caseId}`.

### Unbound, acting on a record

`target.recordIdPath` is the parameter the record id is written into. A dotted
path reaches inside an `entity` parameter; a final `x@odata.bind` segment is
written as `/{entityset}({id})`.

```terraform
resource "dataversecontact_custom_api" "close_incident" {
  scope      = var.scope
  route_name = "close-incident"

  schema_json = jsonencode({
    routeName           = "close-incident"
    dataverseUniqueName = "CloseIncident"
    # A built-in message, not in customapis — so its binding is declared.
    isFunction  = false
    bindingType = "global"
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
```

Called as `POST /api/v2/{scope}/me/actions/close-incident/{caseId}` with
`{ "IncidentResolution": { "subject": "Done" }, "Status": 5 }`.

### Naming the record by its reference number

When the plugin finds a case by its reference number rather than its GUID, set
`target.recordKey` to that column (`ticketnumber`, or a custom column such as
`lcc_caseref`). The URL can then carry the reference number or the GUID. The
API finds the case in the same query that checks it belongs to the caller,
and writes the reference number, **read from that case**, into a `string`
parameter. A `guid` / `entityReference` parameter, or a bound API, gets the
case's GUID.

```terraform
resource "dataversecontact_custom_api" "add_case_note" {
  scope      = var.scope
  route_name = "add-case-note"

  schema_json = jsonencode({
    routeName           = "add-case-note"
    dataverseUniqueName = "lcc_AddCaseNote"
    tiers               = ["me", "team"]
    target = {
      table        = "case"
      recordIdPath = "CaseRef"
      recordKey    = "ticketnumber"
    }
    requestParameters = [
      { uniqueName = "CaseRef", type = "string" },
      { uniqueName = "Note", type = "string" },
    ]
  })

  depends_on = [dataversecontact_table.case]
}
```

Called as `POST /api/v2/{scope}/me/actions/add-case-note/CAS-01234-X1Y2` with
`{ "Note": "Called back" }`.

The column needs a **single-column alternate key** in Dataverse whose index is
`Active`: the reference number only names one case while Dataverse keeps it
unique. Add the key in the solution that owns the table (maker portal: *Tables
→ Case → Keys*) before applying; the apply fails with a 400 that says so
until it's there.

### Caller-scoped (no record)

`callerParameters` fills parameters from the signed-in caller — `contactId` or
`accountId` — and ignores whatever the client sent. The plugin behind the API
is trusted to filter by them.

```terraform
resource "dataversecontact_custom_api" "my_upcoming_bookings" {
  scope      = var.scope
  route_name = "my-upcoming-bookings"

  schema_json = jsonencode({
    routeName           = "my-upcoming-bookings"
    dataverseUniqueName = "tn_MyUpcomingBookings"
    tiers               = ["me"]
    callerParameters    = { CitizenId = "contactId" }
    requestParameters = [
      { uniqueName = "CitizenId", type = "guid" },
    ]
  })
}
```

### Values the definition supplies

Two ways to give a request parameter a value from the definition, deliberately
different (API 1.30.0+):

| | Client leaves it out | Client sends a value | Use for |
|---|---|---|---|
| `default` on a request parameter | the API sends the default | the client's value is used | an input the custom API declares required but that usually takes one value |
| `fixedParameters` on the definition | the API sends the fixed value | **overwritten**, like `callerParameters` | a flag callers must never choose (`IsPrivate`, a source or channel code) |

A default leaves the caller in control. For anything a caller mustn't decide,
use `fixedParameters`.

```terraform
resource "dataversecontact_custom_api" "add_case_note" {
  scope      = var.scope
  route_name = "add-case-note"

  schema_json = jsonencode({
    routeName           = "add-case-note"
    dataverseUniqueName = "lcc_AddCaseNote"
    tiers               = ["me", "team"]
    target = {
      table        = "case"
      recordIdPath = "CaseRef"
      recordKey    = "ticketnumber"
    }
    # Always sent as false, whatever the client posts: a citizen can't file an
    # internal-only note. Keep IsPrivate declared below; Dataverse requires it.
    fixedParameters = { IsPrivate = false }
    requestParameters = [
      { uniqueName = "CaseRef", type = "string" },
      { uniqueName = "Note", type = "string" },
      { uniqueName = "IsPrivate", type = "boolean" },
      { uniqueName = "Source", type = "string", default = "portal" },
    ]
  })

  depends_on = [dataversecontact_table.case]
}
```

Both are applied on every tier, `public` included, and a function receives
them in its URL. A required parameter that is fixed or has a default never has
to be sent by the client. The API's OpenAPI document leaves fixed parameters
out of the request and says "Always sent as: `IsPrivate` = `false`"; a
defaulted one stays in with an OpenAPI `default`. Apply these only once every
API deployment reading the scope runs 1.30.0 or later: an older one ignores
both, so the client's value would reach Dataverse.

### Public

```terraform
resource "dataversecontact_custom_api" "expand_calendar" {
  scope      = var.scope
  route_name = "expand-calendar"

  schema_json = jsonencode({
    routeName           = "expand-calendar"
    dataverseUniqueName = "ExpandCalendar"
    # A built-in message, not in customapis — so its binding is declared.
    isFunction         = true
    bindingType        = "entity"
    boundEntitySetName = "calendars"
    tiers              = ["public"]
    requestParameters = [
      { uniqueName = "Start", type = "datetime" },
      { uniqueName = "End", type = "datetime" },
    ]
  })
}
```

### `target` without a table route

When the record's table isn't published as a route, declare it inline. The
owner columns are navigation properties, one hop from the record; several are
OR'd.

```terraform
target = {
  dataverseTable = "incidents"
  primaryKey     = "incidentid"
  contactColumn  = ["customerid_contact", "primarycontactid"]
  accountColumn  = "customerid_account"
  recordIdPath   = "CaseId" # unbound APIs only
}
```

## Rules the API enforces on apply

A definition that breaks one of these fails `terraform apply` with a 400 that
names the problem.

- A definition that states `isFunction`, `bindingType` or the bound table
  differently from Dataverse is refused. One that isn't in `customapis` (a
  built-in message or classic action) must state `isFunction` and
  `bindingType`, and a bound one `boundEntitySetName` or a `target`.
- A bound API's `target` must be the table Dataverse binds it to, and it takes
  no `recordIdPath`.
- An unbound API with a `target` needs `recordIdPath`, rooted at a declared
  request parameter. On a function it must be a top-level `guid`,
  `entityReference` or `string` parameter.
- `me` needs a `target` (with `contactColumn`, or a table route with a contact
  path) or a `contactId` caller parameter; `team` the same with `accountColumn`
  / `accountId`.
- `public` can't carry a `target` or `callerParameters`.
- Every other `guid`, `entityReference` or `entity` request parameter is
  refused unless marked `unchecked = true` — a record id the API doesn't check
  has to be a decision.
- A `target.table` route must be on the bound table, have the paths its tiers
  check through, and not be a lake table. If the route isn't published yet the
  definition is accepted, and calls fail closed until it is — add a
  `depends_on` on the `dataversecontact_table`.
- `target.recordKey` must be a text column of the `target.table` route's table
  with an active single-column alternate key, and not its primary key. An
  inline target can't take one.
- A `fixedParameters` value or a parameter's `default` must suit the
  parameter's type: `true`/`false` for `boolean`, a whole number for `integer`
  and `picklist`, a number for `decimal`, `float` and `money`, a string for
  `string`, an ISO 8601 date or date-time string for `datetime`, a GUID string
  for `guid`, and a list of strings for `stringArray` (actions only). `entity`,
  `entityReference` and `entityCollection` parameters can't take either: they
  name records, which come from the URL or the caller.
- `fixedParameters` must name declared request parameters, and can't be the
  `recordIdPath` parameter or a `callerParameters` entry, nor have a `default`.
  A `default` can't be set on the `recordIdPath` parameter or a caller
  parameter either. A fixed `guid` counts as checked for the `unchecked` rule
  above; a default `guid` doesn't, because the client can still change it.
- `ownershipCheck` can't be combined with `tiers`; `target`,
  `callerParameters` and `fixedParameters` need `tiers`.

`requiredPermission` is optional; it defaults to `{routeName}:invoke`.
`tiers = null` is the same as leaving `tiers` out.

## Definitions without `tiers`

Existing definitions keep working unchanged. `publicInvoke = true` means
`tiers = ["public"]`. A private definition with an `ownershipCheck` answers on
the tiers its columns can check (`me` with `contactColumn`, `team` with
`accountColumn`, and `all`); one without answers on all three. Signed-in calls
to `/public/actions/{name}` for such a definition still work but are
deprecated.
