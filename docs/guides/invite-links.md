---
page_title: "Invite links"
subcategory: ""
description: |-
  Let someone without an account do one thing, once: log a record, update one, add a note to it or upload a document to it.
---

# Invite links

An invite link lets whoever holds it do **one thing, once**, with no account:
log a new record, update one record, add a row under it (a note on a case), or
upload a document to it. An officer asks "please add the photos you
mentioned"; the person follows the link, does it, and the link stops working.

-> **Requires Contact API 1.36.0 or later**, and the Contact Portal API
solution 2.5.0.0 or later in Dataverse, which holds the invites. An older API
keeps the settings below without an error but makes no invites.

## What to set

| To allow an invite to… | Set |
|---|---|
| log a new row (e.g. a case) | `invites = ["create"]` on the table |
| update one row, only the fields the invite names | `invites = ["update"]` on the table |
| add one row under a record (e.g. a note on a case) | `invites = ["create"]` on the **child** table, the one with `parent_table` |
| upload one document to a record | `invites = true` in the table's `documents.upload` |
| open on your portal's invite page | `invite_url` on `dataversecontact_permissions_sync` |

```terraform
resource "dataversecontact_table" "case" {
  # ...
  invites = ["create", "update"]

  documents {
    permission = "casedocuments"
    upload {
      invites = true
    }
  }
}

resource "dataversecontact_table" "casenotes" {
  # ...
  invites = ["create"]

  parent_table {
    table               = "case"
    navigation_property = "objectid_incident"
  }
}

resource "dataversecontact_permissions_sync" "this" {
  scope      = var.scope
  invite_url = "https://portal.example.gov.uk/invite#token={token}"
  # ...
}
```

`invites` is a set: the order doesn't matter, and an order saved differently
in the Table Manager is no change. Leave it out for no invites; `[]` is refused
because it means the same thing.

## What the invitee can do

- **create** on a child table adds one row under the record the invite was sent
  for. The invitee sets the child's writable fields that aren't lookups; the
  link to the parent is always the invite's record, through the field whose
  `bind_field` is the child's `parent_table.navigation_property`.
- **create** on any other table logs one new row. A `create_default` that binds
  the contact ties it to the contact the invite was sent for, as if they had
  logged it themselves.
- **update** changes the one row, and only the fields the invite names: writable
  fields that aren't lookups, never a contact's sign-in email column.
- **upload** stages and files one document in the row's SharePoint folder, under
  the `upload` block's size, extension and fixed-column rules.

An invitee never reads the record, lists anything, or sets a lookup. The record
is checked again when the link is used, against the table's `filters` (and a
child's `parent.` filters), so a case closed since the invite was sent turns it
away.

A contact table takes `update` invites but never `create`: a new contact is a
way into somebody's company. The provider refuses `invites = ["create"]` on a
contact table at plan time, because the API can't build such a table and
leaves it out of the scope.

## Who can send one

**From Dataverse** (Power Automate, a canvas app, the Portal Access app): the
Contact Portal API solution's `cpa_SendInviteEmail` and `cpa_CreateInviteLink`
actions. The sender needs the **Portal Invite Sender** role and Dataverse access
to the record, and no permission in this provider. This is how officers usually
send them.

**Through the API** (the portal, or an agent): the sender needs `invite` at
their tier on the subject the invite is about, plus the permission to do it
themselves there: `write` for an update, `create` otherwise.

| Invite | Subject |
|---|---|
| log or update a row | the table's permission (`required_permission`, or `permission_group`) |
| add a row under a record | the child table's permission |
| upload a document | the `documents` block's `permission` |

```terraform
default_permissions = {
  case          = ["team", "write:team", "create:team", "invite:team"]
  casedocuments = ["team", "create:team", "invite:team"]
}
```

`invite` is its own operation: reading, writing or creating don't imply it, and
it implies none of them. `default_permissions` grants every signed-in caller,
so on a citizen portal leave `invite` out and send from Dataverse.

## Where links open

`invite_url` is the portal page that uses invites, with `{token}` where the
invite's token goes. Put the token in the URL's **fragment**:
`https://portal.example.gov.uk/invite#token={token}`. A fragment never leaves
the browser, so the token stays out of the portal's server logs, `Referer`
headers and analytics. It must be an https URL of at most 500 characters; the
provider checks this at plan time, as the API would.

Without it, invites come back with a token but no link. The API copies it into
Dataverse, where the invite actions read it.
