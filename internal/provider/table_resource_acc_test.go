package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/TrueNorthIT/terraform-provider-dataversecontact/internal/fakeapi"
)

// These run the table resource through real Terraform plan/apply/import
// cycles against internal/fakeapi. After every apply the framework plans
// again and fails on any diff, which is how the inconsistent-result and
// perpetual-diff bugs (#8, #9, #12, #13) show up. Each one has a test here.
//
// They are resource.UnitTest, not resource.Test: no TF_ACC and no live API,
// so they run in `go test ./...` on every PR. They need a terraform binary on
// PATH.

const caseAddress = "dataversecontact_table.case"

// caseTable is a minimal table; fields is the body of its fields map.
func caseTable(s *fakeapi.Server, fields string, extra ...string) string {
	return s.ProviderConfig() + fmt.Sprintf(`
resource "dataversecontact_table" "case" {
  scope           = "default"
  route_name      = "case"
  dataverse_table = "incidents"
  primary_key     = "incidentid"
  default_select  = ["incidentid", "title"]
  lookup_fields   = ["title"]

  fields = {
%s
  }

  contact_join_step {
    table = "contacts"
    from  = "customerid_contact"
    key   = "contactid"
  }
%s
}
`, fields, strings.Join(extra, "\n"))
}

const baseFields = `
    incidentid = { type = "string", description = "Case id", read_only = true }
    title      = { type = "string", description = "Title" }`

func tablesGone(s *fakeapi.Server) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if n := s.TableCount("default"); n != 0 {
			return fmt.Errorf("%d table(s) left behind after destroy", n)
		}
		return nil
	}
}

func TestAccTable_createImportDestroy(t *testing.T) {
	s := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{
				Config: caseTable(s, baseFields),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("id"), knownvalue.StringExact("default/case")),
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("field_count"), knownvalue.Int64Exact(2)),
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("dataverse_logical_name"), knownvalue.StringExact("incident")),
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("required_permission"), knownvalue.StringExact("case")),
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("filters"), knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("statecode eq 0")})),
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("source"), knownvalue.StringExact("built-in")),
				},
			},
			{
				ResourceName:      caseAddress,
				ImportState:       true,
				ImportStateId:     "default/case",
				ImportStateVerify: true,
			},
		},
	})
}

// #9, #12: field_count went stale when fields were added or removed.
func TestAccTable_addAndRemoveFields(t *testing.T) {
	s := fakeapi.New(t)
	more := baseFields + `
    prioritycode = { type = "choice", description = "Priority" }
    createdon    = { type = "datetime", description = "Created", read_only = true }
    isescalated  = { type = "boolean", description = "Escalated" }`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{
				Config: caseTable(s, baseFields),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("field_count"), knownvalue.Int64Exact(2)),
				},
			},
			{
				Config: caseTable(s, more),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(caseAddress, plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue(caseAddress, tfjsonpath.New("field_count"), knownvalue.Int64Exact(5)),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("field_count"), knownvalue.Int64Exact(5)),
				},
			},
			{
				Config: caseTable(s, baseFields),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("field_count"), knownvalue.Int64Exact(2)),
				},
			},
		},
	})
}

// #8: bind_field is Optional but the API derives it, so a lookup field
// without one failed apply. It must read back as derived, stay put while the
// field is unchanged, and never override a hand-set value.
func TestAccTable_derivedBindField(t *testing.T) {
	s := fakeapi.New(t)
	s.NavProps = map[string]string{"customerid": "customerid_contact", "new_siteid": "new_SiteId"}

	lookups := baseFields + `
    customerid = { type = "lookup", description = "Customer", lookup_table = "contact" }
    new_siteid = { type = "lookup", description = "Site", lookup_table = "site", bind_field = "new_site_override" }`
	bindOf := func(field string) tfjsonpath.Path {
		return tfjsonpath.New("fields").AtMapKey(field).AtMapKey("bind_field")
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{
				Config: caseTable(s, lookups),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, bindOf("customerid"), knownvalue.StringExact("customerid_contact")),
					statecheck.ExpectKnownValue(caseAddress, bindOf("new_siteid"), knownvalue.StringExact("new_site_override")),
					statecheck.ExpectKnownValue(caseAddress, bindOf("title"), knownvalue.Null()),
				},
			},
			{
				// An unrelated change keeps the derived value planned, not unknown.
				Config: caseTable(s, lookups+`
    description = { type = "string", description = "Details" }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectKnownValue(caseAddress, bindOf("customerid"), knownvalue.StringExact("customerid_contact")),
					},
				},
			},
			{
				// Retargeting the lookup re-derives it.
				Config: caseTable(s, strings.Replace(lookups, `lookup_table = "contact"`, `lookup_table = "account"`, 1)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectUnknownValue(caseAddress, bindOf("customerid")),
					},
				},
			},
		},
	})
}

// #17: validation findings are objects. Any save that drew a warning failed
// to decode.
func TestAccTable_validationWarnings(t *testing.T) {
	s := fakeapi.New(t)
	s.Warnings = []fakeapi.Issue{{
		Severity: "warning", Field: "fields.title", Code: "TYPE_MISMATCH",
		Message: `Field "title" is declared as string but Dataverse type is Memo`,
	}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps:                    []resource.TestStep{{Config: caseTable(s, baseFields)}},
	})
}

// A table deleted outside Terraform must be planned for re-creation, not
// kept in state as if it still existed.
func TestAccTable_deletedOutOfBand(t *testing.T) {
	s := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{Config: caseTable(s, baseFields)},
			{
				PreConfig: func() { s.DeleteTable("default", "case") },
				Config:    caseTable(s, baseFields),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(caseAddress, plancheck.ResourceActionCreate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("field_count"), knownvalue.Int64Exact(2)),
				},
			},
		},
	})
}

// Every optional block and attribute set at once, then imported. Anything
// the provider reads back differently from what it wrote shows up as a plan
// diff after apply or an import mismatch.
func TestAccTable_everyAttribute(t *testing.T) {
	s := fakeapi.New(t)
	s.NavProps = map[string]string{"customerid": "customerid_contact"}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{
				Config: caseTable(s, baseFields+`
    customerid = { type = "lookup", description = "Customer", lookup_table = "contact" }
    parentid   = { type = "lookup", description = "Parent case", lookup_table = "case", value_field = "_parentcaseid_value" }`,
					`
  description            = "Support cases"
  icon                   = "incident.svg"
  permission_group       = "support"
  fetch_xml              = "<fetch><entity name='incident'/></fetch>"
  aliases                = ["cases", "ticket"]
  lookup_search_contains = ["title"]
  filters                = ["statecode eq 0", "casetypecode eq 1"]
  public_choices         = false
  public_read            = true
  public_create          = true
  business_process       = {}

  team_join_step {
    table = "accounts"
    from  = "customerid_account"
    key   = "accountid"
  }

  alternate_contact_join_path {
    step {
      table   = "contacts"
      from    = "incident_customer_contacts"
      key     = "contactid"
      reverse = true
    }
  }

  create_default {
    field      = "customerid_contact"
    bind_to    = "contact"
    entity_set = "contacts"
  }

  parent_table {
    table               = "account"
    navigation_property = "customerid_account"
  }

  expand {
    lookup_field  = "customerid"
    related_table = "contacts"
    field {
      name        = "fullname"
      type        = "string"
      description = "Name"
    }
  }`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("field_count"), knownvalue.Int64Exact(4)),
				},
			},
			{
				ResourceName:      caseAddress,
				ImportState:       true,
				ImportStateId:     "default/case",
				ImportStateVerify: true,
			},
		},
	})
}

// Omitted and empty optional lists read back the same way, so neither
// leaves a diff.
func TestAccTable_emptyLists(t *testing.T) {
	s := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{Config: caseTable(s, baseFields, `  aliases = []
  lookup_search_contains = []`)},
			{Config: caseTable(s, baseFields)},
		},
	})
}

// The API derives `<field>_<target>` expands onto a polymorphic family's
// anchor table on every read. Adopting them as state failed every apply.
func TestAccTable_polymorphicDerivedExpands(t *testing.T) {
	s := fakeapi.New(t)
	s.PolymorphicTargets = map[string][]string{"regardingobjectid": {"sb_missed_bin", "sb_fly_tip"}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{
				Config: caseTable(s, baseFields+`
    regardingobjectid = { type = "lookup", description = "Regarding" }
    customerid        = { type = "lookup", description = "Customer", lookup_table = "contact" }`,
					`
  polymorphic_lookup {
    field               = "regardingobjectid"
    required_permission = "servicerecord"
    route_prefix_strip  = "sb_"
  }

  expand {
    lookup_field  = "customerid"
    related_table = "contacts"
    field {
      name        = "fullname"
      type        = "string"
      description = "Name"
    }
  }`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("expand"), knownvalue.ListSizeExact(1)),
				},
			},
		},
	})
}

func TestAccTable_validateConfig(t *testing.T) {
	s := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: caseTable(s, baseFields+`
    customerid = { type = "lookup", description = "Customer" }`),
				ExpectError: regexp.MustCompile(`Lookup field missing lookup_table`),
			},
			{
				Config:      caseTable(s, baseFields, `  parent_table { table = "account" }`),
				ExpectError: regexp.MustCompile(`Incomplete parent_table block`),
			},
		},
	})
}

// ── Known gaps ─────────────────────────────────────────────────────────
// These describe what should happen and fail today. Remove the Skip once the
// provider is fixed.

// A 200 publish that reports a per-route error has not published anything,
// but the provider ignores PublishResponse.Errors and records success.
func TestAccTable_publishRouteErrorFails(t *testing.T) {
	t.Skip("known gap: SaveAndPublishTable ignores PublishResponse.Errors")
	s := fakeapi.New(t)
	s.PublishErrors = map[string]string{"case": "Draft blob not found"}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      caseTable(s, baseFields),
			ExpectError: regexp.MustCompile(`Draft blob not found`),
		}},
	})
}

// The API strips fields Dataverse doesn't have (FIELD_NOT_FOUND) on save.
// The provider ignores the validation result, so the apply fails with
// Terraform's generic "inconsistent result" instead of naming the column.
func TestAccTable_unknownColumnNamed(t *testing.T) {
	t.Skip("known gap: save validation errors are not surfaced as diagnostics")
	s := fakeapi.New(t)
	s.UnknownColumns = map[string][]string{"incident": {"new_typo"}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: caseTable(s, baseFields+`
    new_typo = { type = "string", description = "Misspelt" }`),
			ExpectError: regexp.MustCompile(`new_typo`),
		}},
	})
}
