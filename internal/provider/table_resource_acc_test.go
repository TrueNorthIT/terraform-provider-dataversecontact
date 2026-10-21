package provider

import (
	"fmt"
	"reflect"
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

// #30: a lookup saved before the API derived its bind_field has null in
// state. The next save derives one, so an unrelated change must plan it as
// unknown, not carry the null forward, or apply fails with an inconsistent
// result. Fields the API never derives for stay null in the plan.
func TestAccTable_bindFieldDerivedOnLaterSave(t *testing.T) {
	s := fakeapi.New(t)

	lookups := baseFields + `
    customerid = { type = "lookup", description = "Customer", lookup_table = "contact" }`
	bindOf := func(field string) tfjsonpath.Path {
		return tfjsonpath.New("fields").AtMapKey(field).AtMapKey("bind_field")
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{
				// No navigation property yet, so nothing is derived.
				Config: caseTable(s, lookups),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, bindOf("customerid"), knownvalue.Null()),
				},
			},
			{
				// The API now derives one, and an unrelated change saves the table.
				PreConfig: func() { s.SetNavProps(map[string]string{"customerid": "customerid_contact"}) },
				Config: caseTable(s, lookups+`
    description = { type = "string", description = "Details" }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectUnknownValue(caseAddress, bindOf("customerid")),
						plancheck.ExpectKnownValue(caseAddress, bindOf("title"), knownvalue.Null()),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, bindOf("customerid"), knownvalue.StringExact("customerid_contact")),
				},
			},
		},
	})
}

// An API that keeps derived values out of the schema (Contact API #465)
// reads back what was sent, so an unset bind_field plans as null, known, on
// every change: no "(known after apply)", and nothing to get wrong when the
// API starts deriving later (#30).
func TestAccTable_bindFieldWithAPIOmittingDerived(t *testing.T) {
	s := fakeapi.New(t)
	s.OmitDerived = true

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
					statecheck.ExpectKnownValue(caseAddress, bindOf("customerid"), knownvalue.Null()),
					statecheck.ExpectKnownValue(caseAddress, bindOf("new_siteid"), knownvalue.StringExact("new_site_override")),
				},
			},
			{
				// The API starts deriving one; an unrelated change saves the table.
				PreConfig: func() { s.SetNavProps(map[string]string{"customerid": "customerid_contact"}) },
				Config: caseTable(s, lookups+`
    description = { type = "string", description = "Details" }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectKnownValue(caseAddress, bindOf("customerid"), knownvalue.Null()),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, bindOf("customerid"), knownvalue.Null()),
				},
				Check: func(*terraform.State) error {
					// Stored for requests all the same.
					f := s.Table("default", "case")["fields"].(map[string]any)["customerid"].(map[string]any)
					if f["bindField"] != "customerid_contact" {
						return fmt.Errorf("stored bindField = %v, want customerid_contact", f["bindField"])
					}
					return nil
				},
			},
			{
				// Retargeting the lookup reads back as sent too.
				Config: caseTable(s, strings.Replace(lookups, `lookup_table = "contact"`, `lookup_table = "account"`, 1)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectKnownValue(caseAddress, bindOf("customerid"), knownvalue.Null()),
					},
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

// State written against an older API, which returned a derived bind_field
// inside the schema, must survive the API's upgrade: no plan change, and
// later updates still apply cleanly.
func TestAccTable_bindFieldAcrossAPIUpgrade(t *testing.T) {
	s := fakeapi.New(t)
	s.NavProps = map[string]string{"customerid": "customerid_contact"}

	lookups := baseFields + `
    customerid = { type = "lookup", description = "Customer", lookup_table = "contact" }`
	bindOf := func(field string) tfjsonpath.Path {
		return tfjsonpath.New("fields").AtMapKey(field).AtMapKey("bind_field")
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{
				// Older API: the derived value lands in state.
				Config: caseTable(s, lookups),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, bindOf("customerid"), knownvalue.StringExact("customerid_contact")),
				},
			},
			{
				// Upgraded API, same config: nothing to do.
				PreConfig: func() { s.SetOmitDerived(true) },
				Config:    caseTable(s, lookups),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// An unrelated change keeps the value, and the apply is consistent.
				Config: caseTable(s, lookups+`
    description = { type = "string", description = "Details" }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectKnownValue(caseAddress, bindOf("customerid"), knownvalue.StringExact("customerid_contact")),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, bindOf("customerid"), knownvalue.StringExact("customerid_contact")),
				},
			},
			{
				// Retargeting drops the old value; the API derives a new one out of sight.
				PreConfig: func() { s.SetNavProps(map[string]string{"customerid": "customerid_account"}) },
				Config:    caseTable(s, strings.Replace(lookups, `lookup_table = "contact"`, `lookup_table = "account"`, 1)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectKnownValue(caseAddress, bindOf("customerid"), knownvalue.Null()),
					},
				},
			},
		},
	})
}

// Taking bind_field out of config must reach the API. Terraform carries an
// Optional+Computed value forward when config drops it, so without help the
// removal planned no change and the override stayed.
func TestAccTable_bindFieldRemovedFromConfig(t *testing.T) {
	bindOf := tfjsonpath.New("fields").AtMapKey("new_siteid").AtMapKey("bind_field")
	pinned := baseFields + `
    new_siteid = { type = "lookup", description = "Site", lookup_table = "site", bind_field = "new_site_override" }`
	unpinned := baseFields + `
    new_siteid = { type = "lookup", description = "Site", lookup_table = "site" }`

	for _, tc := range []struct {
		name        string
		omitDerived bool
		planned     plancheck.PlanCheck
		after       knownvalue.Check
	}{
		{"API omitting derived values", true, plancheck.ExpectKnownValue(caseAddress, bindOf, knownvalue.Null()), knownvalue.Null()},
		{"older API", false, plancheck.ExpectUnknownValue(caseAddress, bindOf), knownvalue.StringExact("new_SiteId")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fakeapi.New(t)
			s.OmitDerived = tc.omitDerived
			s.NavProps = map[string]string{"new_siteid": "new_SiteId"}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
				CheckDestroy:             tablesGone(s),
				Steps: []resource.TestStep{
					{
						Config: caseTable(s, pinned),
						ConfigStateChecks: []statecheck.StateCheck{
							statecheck.ExpectKnownValue(caseAddress, bindOf, knownvalue.StringExact("new_site_override")),
						},
					},
					{
						Config: caseTable(s, unpinned),
						ConfigPlanChecks: resource.ConfigPlanChecks{
							PreApply: []plancheck.PlanCheck{
								plancheck.ExpectResourceAction(caseAddress, plancheck.ResourceActionUpdate),
								tc.planned,
							},
						},
						ConfigStateChecks: []statecheck.StateCheck{
							statecheck.ExpectKnownValue(caseAddress, bindOf, tc.after),
						},
						Check: func(*terraform.State) error {
							f := s.Table("default", "case")["fields"].(map[string]any)["new_siteid"].(map[string]any)
							if f["bindField"] != "new_SiteId" {
								return fmt.Errorf("stored bindField = %v, want the derived new_SiteId", f["bindField"])
							}
							return nil
						},
					},
				},
			})
		})
	}
}

// An API rolled back to an older image: the next refresh records it, and the
// older rules apply from there (#30's unknown included).
func TestAccTable_bindFieldAPIRolledBack(t *testing.T) {
	s := fakeapi.New(t)
	s.OmitDerived = true

	lookups := baseFields + `
    customerid = { type = "lookup", description = "Customer", lookup_table = "contact" }`
	bindOf := tfjsonpath.New("fields").AtMapKey("customerid").AtMapKey("bind_field")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{Config: caseTable(s, lookups)},
			{
				PreConfig: func() {
					s.SetOmitDerived(false)
					s.SetNavProps(map[string]string{"customerid": "customerid_contact"})
				},
				Config: caseTable(s, lookups+`
    description = { type = "string", description = "Details" }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectUnknownValue(caseAddress, bindOf)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, bindOf, knownvalue.StringExact("customerid_contact")),
				},
			},
		},
	})
}

// The plan follows what the last read said about the API, but an instance
// that behaves the other way can answer the apply: old and new side by side
// during a deploy. The apply must still succeed, and the next plan be clean.
func TestAccTable_bindFieldAPIChangesDuringApply(t *testing.T) {
	s := fakeapi.New(t)
	s.OmitDerived = true
	s.NavProps = map[string]string{"customerid": "customerid_contact"}

	lookups := baseFields + `
    customerid = { type = "lookup", description = "Customer", lookup_table = "contact" }`
	bindOf := tfjsonpath.New("fields").AtMapKey("customerid").AtMapKey("bind_field")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{Config: caseTable(s, lookups)},
			{
				// Planned against the new API; saved by an older instance.
				PreConfig: func() { s.SetOnTableSave(func(s *fakeapi.Server) { s.OmitDerived = false }) },
				Config: caseTable(s, lookups+`
    description = { type = "string", description = "Details" }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectKnownValue(caseAddress, bindOf, knownvalue.Null())},
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

  files = {
    table         = "activitymimeattachments"
    primary_key   = "activitymimeattachmentid"
    parent_column = "objectid"
    content       = "body"
    file_name     = "filename"
    mime_type     = "mimetype"
    size          = "filesize"
  }

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
      match   = { participationtypemask = [1, 2] }
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

  documents {
    permission = "casedocuments"
    folder     = "Public"
    columns    = { ShowOnPortal = "boolean", DocType = "string" }
    filters    = ["ShowOnPortal eq true", "name endswith .pdf"]
    upload {
      max_bytes  = 1048576
      extensions = ["pdf"]
      columns    = { ShowOnPortal = "true", DocType = "Citizen upload" }
    }
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
			{
				// Child files name all three of table, primary_key and parent_column.
				Config: caseTable(s, baseFields, `
  files = {
    table     = "activitymimeattachments"
    content   = "body"
    file_name = "filename"
    mime_type = "mimetype"
    size      = "filesize"
  }`),
				ExpectError: regexp.MustCompile(`(?s)Invalid Attribute Combination.*primary_key`),
			},
			{
				Config: caseTable(s, baseFields, `
  files = { content = "Document Body", file_name = "filename", mime_type = "mimetype", size = "filesize" }`),
				ExpectError: regexp.MustCompile(`must be a Dataverse logical name`),
			},
			// documents: what the API's build would refuse, and drop the table for.
			{
				Config:      caseTable(s, baseFields, `  documents { folder = "Public" }`),
				ExpectError: regexp.MustCompile(`Incomplete\s+documents\s+block`),
			},
			{
				Config:      caseTable(s, baseFields, `  documents { permission = "Case Documents" }`),
				ExpectError: regexp.MustCompile(`must\s+be\s+a\s+permission\s+name`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    folder     = "../Private"
  }`),
				ExpectError: regexp.MustCompile(`not\s+a\s+folder\s+path\s+below\s+the\s+row's\s+folder`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    folder     = ""
  }`),
				ExpectError: regexp.MustCompile(`leave\s+folder\s+out`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    columns    = { "Show On Portal" = "boolean" }
  }`),
				ExpectError: regexp.MustCompile(`must\s+be\s+a\s+SharePoint\s+column\s+internal\s+name`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    columns    = { name = "string" }
  }`),
				ExpectError: regexp.MustCompile(`(?s)none of.*name`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    columns    = { ShowOnPortal = "bool" }
  }`),
				ExpectError: regexp.MustCompile(`value\s+must\s+be\s+one\s+of`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    filters    = ["ShowOnPortal eq true"]
  }`),
				ExpectError: regexp.MustCompile(`\"ShowOnPortal\"\s+isn't\s+a\s+document\s+field`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    columns    = { ShowOnPortal = "boolean" }
    filters    = ["ShowOnPortal gt true"]
  }`),
				ExpectError: regexp.MustCompile(`'gt'\s+doesn't\s+apply\s+to\s+boolean\s+field`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    filters    = []
  }`),
				ExpectError: regexp.MustCompile(`at\s+least\s+1`),
			},
			// documents.upload, as the API's contract (1.35.0) would refuse it.
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    upload {
      columns = { ShowOnPortal = "true" }
    }
  }`),
				ExpectError: regexp.MustCompile(`"ShowOnPortal"\s+isn't\s+declared\s+in\s+documents.columns`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    columns    = { ShowOnPortal = "boolean" }
    upload {
      columns = { ShowOnPortal = "yes" }
    }
  }`),
				ExpectError: regexp.MustCompile(`"yes"\s+isn't\s+a\s+boolean`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    columns    = { Rank = "number" }
    upload {
      columns = { Rank = "first" }
    }
  }`),
				ExpectError: regexp.MustCompile(`"first"\s+isn't\s+a\s+number`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    upload {
      max_bytes = 0
    }
  }`),
				ExpectError: regexp.MustCompile(`at\s+least\s+1`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    upload {
      extensions = [".PDF"]
    }
  }`),
				ExpectError: regexp.MustCompile(`lower\s+case,\s+without\s+the\s+dot`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    upload {
      extensions = ["pdf", "exe"]
    }
  }`),
				ExpectError: regexp.MustCompile(`"exe"\s+is\s+an\s+executable\s+type`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    upload {
      extensions = ["pdf", "pdf"]
    }
  }`),
				ExpectError: regexp.MustCompile(`Duplicate\s+List\s+Value`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    upload {
      extensions = []
    }
  }`),
				ExpectError: regexp.MustCompile(`at\s+least\s+1`),
			},
			{
				Config: caseTable(s, baseFields, `
  documents {
    permission = "casedocuments"
    columns    = { ShowOnPortal = "boolean" }
    upload {
      columns = {}
    }
  }`),
				ExpectError: regexp.MustCompile(`at\s+least\s+1`),
			},
		},
	})
}

// documents over its life. A table without it publishes no key and plans
// clean even when the API spells the absence as null; each optional
// attribute is sent only when set; a list-form columns written outside
// Terraform reads back without a diff when it names the configured columns;
// and removing the block removes the key.
func TestAccTable_documents(t *testing.T) {
	s := fakeapi.New(t)
	documents := func(body string) string {
		return fmt.Sprintf("\n  documents {\n%s\n  }", body)
	}
	full := documents(`    permission = "casedocuments"
    folder     = "Public"
    columns    = { ShowOnPortal = "boolean" }
    filters    = ["ShowOnPortal eq true"]`)
	permissionOnly := documents(`    permission = "casedocuments"`)
	typed := documents(`    permission = "casedocuments"
    columns    = { ShowOnPortal = "boolean" }`)

	published := func(want map[string]any) resource.TestCheckFunc {
		return func(*terraform.State) error {
			got, ok := s.Table("default", "case")["documents"]
			if want == nil {
				if ok {
					return fmt.Errorf("documents published without a block: %v", got)
				}
				return nil
			}
			if fmt.Sprint(got) != fmt.Sprint(want) {
				return fmt.Errorf("published documents = %v, want %v", got, want)
			}
			return nil
		}
	}
	setColumns := func(columns any) func() {
		return func() {
			s.EditTable("default", "case", func(m map[string]any) {
				m["documents"].(map[string]any)["columns"] = columns
			})
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{
				Config: caseTable(s, baseFields),
				Check:  published(nil),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("documents"), knownvalue.Null()),
				},
			},
			{
				// The published hint spells "no documents" as null.
				PreConfig: func() { s.EditTable("default", "case", func(m map[string]any) { m["documents"] = nil }) },
				Config:    caseTable(s, baseFields),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				Config: caseTable(s, baseFields, full),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(caseAddress, plancheck.ResourceActionUpdate)},
				},
				Check: published(map[string]any{
					"permission": "casedocuments",
					"folder":     "Public",
					"columns":    map[string]any{"ShowOnPortal": "boolean"},
					"filters":    []any{"ShowOnPortal eq true"},
				}),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("documents").AtMapKey("columns"),
						knownvalue.MapExact(map[string]knownvalue.Check{"ShowOnPortal": knownvalue.StringExact("boolean")})),
				},
			},
			{
				Config: caseTable(s, baseFields, permissionOnly),
				Check:  published(map[string]any{"permission": "casedocuments"}),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("documents").AtMapKey("folder"), knownvalue.Null()),
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("documents").AtMapKey("columns"), knownvalue.Null()),
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("documents").AtMapKey("filters"), knownvalue.Null()),
				},
			},
			{Config: caseTable(s, baseFields, typed)},
			{
				// The same columns as a list, as the Table Manager may save them.
				PreConfig: setColumns([]any{"ShowOnPortal"}),
				Config:    caseTable(s, baseFields, typed),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				// A list naming other columns is drift: put the config back.
				PreConfig: setColumns([]any{"DocType"}),
				Config:    caseTable(s, baseFields, typed),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(caseAddress, plancheck.ResourceActionUpdate)},
				},
				Check: published(map[string]any{
					"permission": "casedocuments",
					"columns":    map[string]any{"ShowOnPortal": "boolean"},
				}),
			},
			{
				ResourceName:      caseAddress,
				ImportState:       true,
				ImportStateId:     "default/case",
				ImportStateVerify: true,
			},
			{
				Config: caseTable(s, baseFields),
				Check:  published(nil),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("documents"), knownvalue.Null()),
				},
			},
		},
	})
}

// documents.upload over its life. A documents block without upload publishes
// no upload key and plans clean even when the API spells the absence as null;
// upload {} is sent as {} and plans clean; fixed column values go out typed by
// their declared type and read back as the strings the config wrote, also
// when the API holds them as strings or re-spells a number; each optional
// attribute is sent only when set; and removing the block removes the key.
func TestAccTable_documentsUpload(t *testing.T) {
	s := fakeapi.New(t)
	documents := func(upload string) string {
		return `
  documents {
    permission = "casedocuments"
    columns    = { ShowOnPortal = "boolean", Rank = "number" }
    filters    = ["ShowOnPortal eq true"]` + upload + `
  }`
	}
	upload := func(body string) string { return "\n    upload {\n" + body + "\n    }" }

	uploadPath := tfjsonpath.New("documents").AtMapKey("upload")
	// published compares the stored upload by value and JSON type, so true
	// and "true" differ. nil wants no upload key.
	published := func(want map[string]any) resource.TestCheckFunc {
		return func(*terraform.State) error {
			documents, _ := s.Table("default", "case")["documents"].(map[string]any)
			if documents == nil {
				return fmt.Errorf("documents not published")
			}
			got, ok := documents["upload"]
			switch {
			case want == nil && ok:
				return fmt.Errorf("upload published without a block: %#v", got)
			case want != nil && !reflect.DeepEqual(got, want):
				return fmt.Errorf("published upload = %#v, want %#v", got, want)
			}
			return nil
		}
	}
	emptyPlan := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}}
	update := resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(caseAddress, plancheck.ResourceActionUpdate)},
	}
	full := upload(`      max_bytes  = 1048576
      extensions = ["pdf", "png"]
      columns    = { ShowOnPortal = "true", Rank = "4" }`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{
				Config: caseTable(s, baseFields, documents("")),
				Check:  published(nil),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, uploadPath, knownvalue.Null()),
				},
			},
			{
				// The published hint spells "no upload" as null.
				PreConfig: func() {
					s.EditTable("default", "case", func(m map[string]any) { m["documents"].(map[string]any)["upload"] = nil })
				},
				Config:           caseTable(s, baseFields, documents("")),
				ConfigPlanChecks: emptyPlan,
			},
			{
				// Upload with the API's defaults.
				Config:           caseTable(s, baseFields, documents("\n    upload {}")),
				ConfigPlanChecks: update,
				Check:            published(map[string]any{}),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, uploadPath, knownvalue.ObjectExact(map[string]knownvalue.Check{
						"max_bytes":  knownvalue.Null(),
						"extensions": knownvalue.Null(),
						"columns":    knownvalue.Null(),
					})),
				},
			},
			{
				Config:           caseTable(s, baseFields, documents(full)),
				ConfigPlanChecks: update,
				Check: published(map[string]any{
					"maxBytes":   float64(1048576),
					"extensions": []any{"pdf", "png"},
					"columns":    map[string]any{"ShowOnPortal": true, "Rank": float64(4)},
				}),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, uploadPath.AtMapKey("columns"), knownvalue.MapExact(map[string]knownvalue.Check{
						"ShowOnPortal": knownvalue.StringExact("true"),
						"Rank":         knownvalue.StringExact("4"),
					})),
					statecheck.ExpectKnownValue(caseAddress, uploadPath.AtMapKey("max_bytes"), knownvalue.Int64Exact(1048576)),
				},
			},
			{
				// The API keeps the number, not the text: 4.0 comes back as 4,
				// and the config's spelling is kept.
				Config: caseTable(s, baseFields, documents(upload(`      columns = { ShowOnPortal = "true", Rank = "4.0" }`))),
				Check: published(map[string]any{
					"columns": map[string]any{"ShowOnPortal": true, "Rank": float64(4)},
				}),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, uploadPath.AtMapKey("columns").AtMapKey("Rank"), knownvalue.StringExact("4.0")),
					statecheck.ExpectKnownValue(caseAddress, uploadPath.AtMapKey("max_bytes"), knownvalue.Null()),
					statecheck.ExpectKnownValue(caseAddress, uploadPath.AtMapKey("extensions"), knownvalue.Null()),
				},
			},
			{
				// The same values held as strings, as the Table Manager may save them.
				PreConfig: func() {
					s.EditTable("default", "case", func(m map[string]any) {
						m["documents"].(map[string]any)["upload"].(map[string]any)["columns"] =
							map[string]any{"ShowOnPortal": "true", "Rank": "4.0"}
					})
				},
				Config:           caseTable(s, baseFields, documents(upload(`      columns = { ShowOnPortal = "true", Rank = "4.0" }`))),
				ConfigPlanChecks: emptyPlan,
			},
			{
				// A value changed outside Terraform is drift: put the config back.
				PreConfig: func() {
					s.EditTable("default", "case", func(m map[string]any) {
						m["documents"].(map[string]any)["upload"].(map[string]any)["columns"] =
							map[string]any{"ShowOnPortal": false, "Rank": float64(4)}
					})
				},
				Config:           caseTable(s, baseFields, documents(full)),
				ConfigPlanChecks: update,
				Check: published(map[string]any{
					"maxBytes":   float64(1048576),
					"extensions": []any{"pdf", "png"},
					"columns":    map[string]any{"ShowOnPortal": true, "Rank": float64(4)},
				}),
			},
			{
				ResourceName:      caseAddress,
				ImportState:       true,
				ImportStateId:     "default/case",
				ImportStateVerify: true,
			},
			{
				Config: caseTable(s, baseFields, documents("")),
				Check:  published(nil),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, uploadPath, knownvalue.Null()),
				},
			},
		},
	})
}

// A read-only lookup needs no lookup_table: it never writes, and a
// polymorphic one (customerid) has no single target. v1.1.0 to v1.5.0
// refused it at plan, which broke our own portal configs (found by
// TestAccRealConfigs).
func TestAccTable_readOnlyLookupWithoutTable(t *testing.T) {
	s := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{{
			Config: caseTable(s, baseFields+`
    customerid = { type = "lookup", description = "Customer (account or contact)", read_only = true }`),
		}},
	})
}

// ── Saves the API only half carries out ────────────────────────────────
// The API answers both of these with a 200. Each must fail the apply, publish
// nothing the config didn't ask for, and leave no draft behind: the table GET
// returns a draft in preference to the published table, so a stray one would
// be read back as if it were live.

// A publish that reports a per-route error has published nothing.
func TestAccTable_publishRouteErrorFails(t *testing.T) {
	s := fakeapi.New(t)
	s.PublishErrors = map[string]string{"case": "Draft blob not found"}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{{
			Config:      caseTable(s, baseFields),
			ExpectError: regexp.MustCompile(`error\s+publishing\s+"case",\s+so\s+it\s+may\s+not\s+have\s+been\s+published:\s+Draft\s+blob\s+not\s+found`),
		}},
	})
}

const typoFields = baseFields + `
    new_typo = { type = "string", description = "Misspelt" }`

// The API strips fields Dataverse doesn't have (FIELD_NOT_FOUND) on save, and
// answers with a validation of what is left, which doesn't mention them. The
// apply must still name the column, and must not publish the stripped table.
func TestAccTable_unknownColumnNamed(t *testing.T) {
	s := fakeapi.New(t)
	s.UnknownColumns = map[string][]string{"incident": {"new_typo"}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(state *terraform.State) error {
			for _, r := range s.Requests() {
				if strings.HasSuffix(r, "/publish") {
					return fmt.Errorf("a table with a stripped field was published: %s", r)
				}
			}
			return tablesGone(s)(state)
		},
		Steps: []resource.TestStep{{
			Config:      caseTable(s, typoFields),
			ExpectError: regexp.MustCompile(`Field not in Dataverse(?s:.*)"new_typo"(?s:.*)incident\s+has\s+no\s+such\s+column(?s:.*)draft\s+discarded`),
		}},
	})
}

// On an update, the table stays published as it was and state keeps the old
// config, so the change is still offered on the next plan.
func TestAccTable_unknownColumnOnUpdateKeepsPublished(t *testing.T) {
	s := fakeapi.New(t)
	s.UnknownColumns = map[string][]string{"incident": {"new_typo"}}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{Config: caseTable(s, baseFields)},
			{
				Config:      caseTable(s, typoFields),
				ExpectError: regexp.MustCompile(`new_typo`),
			},
			{
				Config:             caseTable(s, typoFields),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				// Back to the published config: nothing to do, and no draft left.
				Config: caseTable(s, baseFields),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: func(*terraform.State) error {
					if n := s.TableCount("default"); n != 1 {
						return fmt.Errorf("want the one published table, found %d tables and drafts", n)
					}
					return nil
				},
			},
		},
	})
}
