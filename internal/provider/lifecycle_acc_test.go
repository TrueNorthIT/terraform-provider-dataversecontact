package provider

import (
	"encoding/json"
	"fmt"
	"os"
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

// Changes over a resource's life: renames, drift, adopting existing tables,
// editing each part of a schema, and configs with many tables.

func publishedTable(s *fakeapi.Server, route string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if s.Table("default", route) == nil {
			return fmt.Errorf("table %q is not published", route)
		}
		return nil
	}
}

func notPublished(s *fakeapi.Server, route string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if s.Table("default", route) != nil {
			return fmt.Errorf("table %q is still published", route)
		}
		return nil
	}
}

// route_name and scope can't be changed in place: the old route has to go.
func TestAccTable_renameReplaces(t *testing.T) {
	s := fakeapi.New(t)
	renamed := strings.Replace(caseTable(s, baseFields), `route_name      = "case"`, `route_name      = "ticket"`, 1)
	moved := strings.Replace(renamed, `scope           = "default"`, `scope           = "support"`, 1)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{Config: caseTable(s, baseFields), Check: publishedTable(s, "case")},
			{
				Config: renamed,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(caseAddress, plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				Check: resource.ComposeTestCheckFunc(publishedTable(s, "ticket"), notPublished(s, "case")),
			},
			{
				Config: moved,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(caseAddress, plancheck.ResourceActionDestroyBeforeCreate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("id"), knownvalue.StringExact("support/ticket")),
				},
				Check: notPublished(s, "ticket"),
			},
		},
	})
}

// A table edited outside Terraform is planned back to the config, and the
// next apply restores it.
func TestAccTable_driftIsReverted(t *testing.T) {
	s := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			{Config: caseTable(s, baseFields)},
			{
				PreConfig: func() {
					s.EditTable("default", "case", func(m map[string]any) {
						fields := m["fields"].(map[string]any)
						fields["title"].(map[string]any)["description"] = "Edited in Table Manager"
						fields["new_extra"] = map[string]any{"type": "string", "description": "Added by hand"}
						m["publicRead"] = true
					})
				},
				Config: caseTable(s, baseFields),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(caseAddress, plancheck.ResourceActionUpdate),
						plancheck.ExpectKnownValue(caseAddress, tfjsonpath.New("public_read"), knownvalue.Bool(false)),
					},
				},
				Check: func(*terraform.State) error {
					m := s.Table("default", "case")
					fields := m["fields"].(map[string]any)
					if _, ok := fields["new_extra"]; ok || fields["title"].(map[string]any)["description"] != "Title" || m["publicRead"] != nil {
						return fmt.Errorf("drift not reverted: %v", m)
					}
					return nil
				},
			},
		},
	})
}

// A table recorded from the live API, adopted with `terraform import` and a
// hand-written config, must plan clean: no first apply that rewrites it.
func TestAccTable_importRecordedTable(t *testing.T) {
	s := fakeapi.New(t)
	raw, err := os.ReadFile("../testdata/contract/get-table-published.json")
	if err != nil {
		t.Fatal(err)
	}
	var recorded struct {
		Schema map[string]any `json:"schema"`
	}
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatal(err)
	}
	s.SeedTable("default", recorded.Schema)

	config := s.ProviderConfig() + `
resource "dataversecontact_table" "projectnotes" {
  scope                  = "default"
  route_name             = "projectnotes"
  description            = "Delivery notes/updates on your projects"
  dataverse_table        = "annotations"
  dataverse_logical_name = "annotation"
  required_permission    = "project"
  primary_key            = "annotationid"
  permission_group       = "project"
  aliases                = ["projectnote"]
  default_select         = ["annotationid", "subject", "notetext", "objectid", "objecttypecode", "createdon", "modifiedon"]
  lookup_fields          = ["subject"]
  lookup_search_contains = ["subject"]
  filters                = ["objecttypecode eq 'msdyn_project'"]

  fields = {
    annotationid   = { type = "string", description = "Unique note identifier", read_only = true }
    createdon      = { type = "datetime", description = "Date created", read_only = true }
    modifiedon     = { type = "datetime", description = "Date last modified", read_only = true }
    notetext       = { type = "string", description = "Note text" }
    objectid       = { type = "lookup", description = "Regarding project", lookup_table = "project" }
    objecttypecode = { type = "string", description = "Regarding entity type", read_only = true }
    subject        = { type = "string", description = "Note subject" }
  }

  contact_join_step {
    table = "msdyn_projects"
    from  = "objectid_msdyn_project"
    key   = "msdyn_projectid"
  }
  contact_join_step {
    table = "accounts"
    from  = "msdyn_customer"
    key   = "accountid"
  }
  contact_join_step {
    table = "contacts"
    from  = "primarycontactid"
    key   = "contactid"
  }

  team_join_step {
    table = "msdyn_projects"
    from  = "objectid_msdyn_project"
    key   = "msdyn_projectid"
  }
  team_join_step {
    table = "accounts"
    from  = "msdyn_customer"
    key   = "accountid"
  }

  parent_table {
    table               = "project"
    navigation_property = "objectid_msdyn_project"
  }
}
`
	address := "dataversecontact_table.projectnotes"
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       address,
				ImportState:        true,
				ImportStateId:      "default/projectnotes",
				ImportStatePersist: true,
			},
			{
				Config: config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				// The derived bindField came in with the import.
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(address, tfjsonpath.New("fields").AtMapKey("objectid").AtMapKey("bind_field"),
						knownvalue.StringExact("objectid_msdyn_project")),
				},
			},
		},
	})
}

// Each field attribute can be changed on its own, in both directions.
func TestAccTable_fieldEdits(t *testing.T) {
	s := fakeapi.New(t)
	s.NavProps = map[string]string{"customerid": "customerid_contact"}
	field := func(title, customer string) string {
		return fmt.Sprintf(`
    incidentid = { type = "string", description = "Case id", read_only = true }
    title      = %s
    customerid = %s`, title, customer)
	}
	lookup := `{ type = "lookup", description = "Customer", lookup_table = "contact" }`
	steps := []string{
		field(`{ type = "string", description = "Title" }`, lookup),
		field(`{ type = "string", description = "Case title" }`, lookup),
		field(`{ type = "string", description = "Case title", read_only = true }`, lookup),
		field(`{ type = "string", description = "Case title", read_only = false }`, lookup),
		field(`{ type = "choice", description = "Case title" }`, lookup),
		field(`{ type = "string", description = "Case title" }`, `{ type = "lookup", description = "Customer", lookup_table = "contact", bind_field = "customerid_override" }`),
		field(`{ type = "string", description = "Case title" }`, `{ type = "lookup", description = "Customer", lookup_table = "contact", value_field = "_customerid_value" }`),
	}
	var ts []resource.TestStep
	for _, f := range steps {
		ts = append(ts, resource.TestStep{Config: caseTable(s, f)})
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps:                    ts,
	})
}

// Each block can be added, changed and removed again.
func TestAccTable_blockEdits(t *testing.T) {
	s := fakeapi.New(t)
	fields := baseFields + `
    customerid        = { type = "lookup", description = "Customer", lookup_table = "contact", bind_field = "customerid_contact" }
    regardingobjectid = { type = "lookup", description = "Regarding", lookup_table = "activity" }`
	expand := func(name string) string {
		return fmt.Sprintf(`
  expand {
    lookup_field  = "customerid"
    related_table = "contacts"
    field {
      name        = %q
      type        = "string"
      description = "From the contact"
    }
  }`, name)
	}
	blocks := []string{
		``,
		`
  team_join_step {
    table = "accounts"
    from  = "customerid_account"
    key   = "accountid"
  }
  alternate_contact_join_path {
    step {
      table   = "contacts"
      from    = "primarycontactid"
      key     = "contactid"
      reverse = false
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
  polymorphic_lookup {
    field               = "regardingobjectid"
    required_permission = "servicerecord"
  }
  business_process = { expose_as = "stage" }` + expand("fullname"),
		`
  team_join_step {
    table = "accounts"
    from  = "parentcustomerid_account"
    key   = "accountid"
  }
  create_default {
    field      = "customerid_account"
    bind_to    = "account"
    entity_set = "accounts"
  }
  business_process = {}` + expand("emailaddress1"),
		``,
	}
	var ts []resource.TestStep
	for _, b := range blocks {
		ts = append(ts, resource.TestStep{Config: caseTable(s, fields, b)})
	}
	ts = append(ts, resource.TestStep{
		Config: caseTable(s, fields),
		Check: func(*terraform.State) error {
			m := s.Table("default", "case")
			for _, key := range []string{"teamJoinPath", "createDefaults", "parentTable", "polymorphicLookup", "businessProcess", "expands", "alternateContactJoinPaths"} {
				if _, ok := m[key]; ok {
					return fmt.Errorf("%s still published after its block was removed", key)
				}
			}
			return nil
		},
	})
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps:                    ts,
	})
}

// Configs like ours have a dozen tables and one permissions publish that
// depends on all of them.
func TestAccTable_manyTables(t *testing.T) {
	s := fakeapi.New(t)
	const n = 15
	// count, not for_each: plugin-testing's state checks can't address
	// for_each instances.
	config := s.ProviderConfig() + fmt.Sprintf(`
resource "dataversecontact_table" "all" {
  count           = %d
  scope           = "default"
  route_name      = "t${count.index}"
  dataverse_table = "new_entity${count.index}s"
  primary_key     = "id"
  default_select  = ["id", "name"]
  lookup_fields   = ["name"]
  fields = {
    id   = { type = "string", description = "Id", read_only = true }
    name = { type = "string", description = "Name of t${count.index}" }
  }
  contact_join_step {
    table = "contacts"
    from  = "new_contactid"
    key   = "contactid"
  }
}

resource "dataversecontact_permissions_sync" "default" {
  scope               = "default"
  default_permissions = { for t in dataversecontact_table.all : t.route_name => ["me", "write"] }
  triggers            = { for t in dataversecontact_table.all : t.route_name => t.field_count }
}
`, n)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{{
			Config: config,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("dataversecontact_permissions_sync.default", tfjsonpath.New("permission_count"), knownvalue.Int64Exact(n)),
			},
			Check: func(*terraform.State) error {
				if got := s.TableCount("default"); got != n {
					return fmt.Errorf("%d tables published, want %d", got, n)
				}
				if got := s.Table("default", "t7")["dataverseLogicalName"]; got != "new_entity7" {
					return fmt.Errorf("t7 logical name = %v", got)
				}
				return nil
			},
		}},
	})
}

// A hand-formatted schema_json must not diff against the API's compact copy.
func TestAccCustomApi_prettyJSON(t *testing.T) {
	s := fakeapi.New(t)
	config := s.ProviderConfig() + `
resource "dataversecontact_custom_api" "calendar" {
  scope       = "default"
  route_name  = "expand-calendar"
  schema_json = <<-JSON
    {
      "routeName":           "expand-calendar",
      "dataverseUniqueName": "ExpandCalendar",
      "isFunction":          true,
      "bindingType":         "global"
    }
  JSON
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config},
			{
				// Changing what the schema says updates the computed attributes.
				Config: strings.Replace(strings.Replace(config, `true,`, `false,`, 1), `"global"`, `"entity"`, 1),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(calendarAddress, tfjsonpath.New("is_function"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(calendarAddress, tfjsonpath.New("binding_type"), knownvalue.StringExact("entity")),
				},
			},
		},
	})
}

func TestAccCustomApi_invalidJSON(t *testing.T) {
	s := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: s.ProviderConfig() + `
resource "dataversecontact_custom_api" "calendar" {
  scope       = "default"
  route_name  = "expand-calendar"
  schema_json = "[1, 2]"
}
`,
			ExpectError: regexp.MustCompile(`(?i)json object`),
		}},
	})
}

// Settings removed from the config are removed from defaults.json, not left
// as they were.
func TestAccPermissionsSync_removeSettings(t *testing.T) {
	s := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: permissionsSync(s, `
  default_permissions  = { case = ["me"] }
  allow_self_register  = true
  contact_email_column = "emailaddress2"
  company_model        = { strategy = "parent-account" }
  join                 = { strategy = "domain-list", domain_field = "new_portaldomains" }`),
			},
			{
				Config: permissionsSync(s, ``),
				Check: expectDefaults(s, map[string]any{
					"permissions":       map[string]any{},
					"allowSelfRegister": false,
				}),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("dataversecontact_permissions_sync.default", tfjsonpath.New("permission_count"), knownvalue.Int64Exact(0)),
				},
			},
		},
	})
}

func TestAccPermissionsSync_invalidConfig(t *testing.T) {
	s := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      permissionsSync(s, `  contact_email_column = "EmailAddress2"`),
				ExpectError: regexp.MustCompile(`logical name`),
			},
			{
				Config:      permissionsSync(s, `  company_model = { strategy = "holding-company" }`),
				ExpectError: regexp.MustCompile(`parent-account`),
			},
			{
				Config: permissionsSync(s, `
  company_model = {
    strategy            = "associated-accounts"
    associated_accounts = { account_id_field = "accountid" }
  }`),
				ExpectError: regexp.MustCompile(`fetch_xml`),
			},
		},
	})
}

func TestAccDataSourceTable_missing(t *testing.T) {
	s := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: s.ProviderConfig() + `
data "dataversecontact_table" "nope" {
  scope      = "default"
  route_name = "nope"
}
`,
			ExpectError: regexp.MustCompile(`Unknown table: nope`),
		}},
	})
}
