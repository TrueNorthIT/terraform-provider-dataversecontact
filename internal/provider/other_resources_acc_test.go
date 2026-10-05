package provider

import (
	"fmt"
	"reflect"
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

// ── dataversecontact_custom_api ────────────────────────────────────────

const calendarAddress = "dataversecontact_custom_api.calendar"

func calendarApi(s *fakeapi.Server, description string) string {
	return s.ProviderConfig() + fmt.Sprintf(`
resource "dataversecontact_custom_api" "calendar" {
  scope      = "default"
  route_name = "expand-calendar"
  schema_json = jsonencode({
    routeName              = "expand-calendar"
    dataverseUniqueName    = "ExpandCalendar"
    description            = %q
    requiredPermission     = "expand-calendar:invoke"
    isFunction             = true
    bindingType            = "entity"
    boundEntityLogicalName = "calendar"
    boundEntitySetName     = "calendars"
    requestParameters = [
      { uniqueName = "Start", type = "datetime" },
      { uniqueName = "End", type = "datetime" },
    ]
  })
}
`, description)
}

func TestAccCustomApi_createUpdateImportDestroy(t *testing.T) {
	s := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if n := s.ApiCount("default"); n != 0 {
				return fmt.Errorf("%d custom API(s) left behind after destroy", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: calendarApi(s, "Expands a calendar"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(calendarAddress, tfjsonpath.New("id"), knownvalue.StringExact("default/expand-calendar")),
					statecheck.ExpectKnownValue(calendarAddress, tfjsonpath.New("dataverse_unique_name"), knownvalue.StringExact("ExpandCalendar")),
					statecheck.ExpectKnownValue(calendarAddress, tfjsonpath.New("is_function"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(calendarAddress, tfjsonpath.New("binding_type"), knownvalue.StringExact("entity")),
					statecheck.ExpectKnownValue(calendarAddress, tfjsonpath.New("source"), knownvalue.StringExact("published")),
				},
			},
			{
				Config: calendarApi(s, "Expands a calendar into time slots"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(calendarAddress, plancheck.ResourceActionUpdate),
					},
				},
				Check: func(*terraform.State) error {
					if got := s.CustomApi("default", "expand-calendar")["description"]; got != "Expands a calendar into time slots" {
						return fmt.Errorf("published description = %v", got)
					}
					return nil
				},
			},
			{
				ResourceName:      calendarAddress,
				ImportState:       true,
				ImportStateId:     "default/expand-calendar",
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccCustomApi_deletedOutOfBand(t *testing.T) {
	s := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: calendarApi(s, "Expands a calendar")},
			{
				PreConfig: func() { s.DeleteCustomApi("default", "expand-calendar") },
				Config:    calendarApi(s, "Expands a calendar"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(calendarAddress, plancheck.ResourceActionCreate),
					},
				},
			},
		},
	})
}

// ── dataversecontact_permissions_sync ──────────────────────────────────

func permissionsSync(s *fakeapi.Server, body string) string {
	return s.ProviderConfig() + fmt.Sprintf(`
resource "dataversecontact_permissions_sync" "default" {
  scope = "default"
%s
}
`, body)
}

// expectDefaults checks the defaults.json body the API last received.
func expectDefaults(s *fakeapi.Server, want map[string]any) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := s.Defaults("default"); !reflect.DeepEqual(got, want) {
			return fmt.Errorf("published defaults:\n got:  %v\n want: %v", got, want)
		}
		return nil
	}
}

func defaultsPuts(s *fakeapi.Server) int {
	n := 0
	for _, r := range s.Requests() {
		if r == "PUT /api/v2/_admin/default/table-manager/defaults" {
			n++
		}
	}
	return n
}

func TestAccPermissionsSync(t *testing.T) {
	s := fakeapi.New(t)
	puts := 0
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: permissionsSync(s, `
  default_permissions = {
    case    = ["me", "write", "create"]
    account = ["team"]
  }`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("dataversecontact_permissions_sync.default", tfjsonpath.New("permission_count"), knownvalue.Int64Exact(2)),
				},
				// Unset optionals are left out, so the API applies its own defaults.
				Check: resource.ComposeTestCheckFunc(
					expectDefaults(s, map[string]any{
						"permissions":       map[string]any{"case": []any{"me", "write", "create"}, "account": []any{"team"}},
						"allowSelfRegister": false,
					}),
					func(*terraform.State) error { puts = defaultsPuts(s); return nil },
				),
			},
			{
				Config: permissionsSync(s, `
  default_permissions  = { case = ["me"] }
  allow_self_register  = true
  contact_email_column = "emailaddress2"
  company_model = {
    strategy            = "associated-accounts"
    associated_accounts = { relationship = "new_contact_account" }
  }
  join = {
    strategy      = "domain-list"
    domain_field  = "new_portaldomains"
    require_match = true
  }`),
				Check: expectDefaults(s, map[string]any{
					"permissions":        map[string]any{"case": []any{"me"}},
					"allowSelfRegister":  true,
					"contactEmailColumn": "emailaddress2",
					"companyModel": map[string]any{
						"strategy":           "associated-accounts",
						"associatedAccounts": map[string]any{"relationship": "new_contact_account"},
					},
					"join": map[string]any{"strategy": "domain-list", "domainField": "new_portaldomains", "requireMatch": true},
				}),
			},
			{
				// A changed trigger re-publishes with nothing else changed.
				Config: permissionsSync(s, `
  default_permissions  = { case = ["me"] }
  allow_self_register  = true
  contact_email_column = "emailaddress2"
  company_model = {
    strategy            = "associated-accounts"
    associated_accounts = { relationship = "new_contact_account" }
  }
  join = {
    strategy      = "domain-list"
    domain_field  = "new_portaldomains"
    require_match = true
  }
  triggers = { tables = "v2" }`),
				Check: func(*terraform.State) error {
					if n := defaultsPuts(s); n <= puts+1 {
						return fmt.Errorf("trigger change did not re-publish: %d PUTs, %d before step 2", n, puts)
					}
					return nil
				},
			},
		},
	})
}

// ── Data sources ───────────────────────────────────────────────────────

func TestAccDataSources(t *testing.T) {
	s := fakeapi.New(t)
	config := caseTable(s, baseFields) + `
data "dataversecontact_scopes" "all" {
  depends_on = [dataversecontact_table.case]
}

data "dataversecontact_table_definitions" "default" {
  scope      = "default"
  depends_on = [dataversecontact_table.case]
}

data "dataversecontact_table" "case" {
  scope      = "default"
  route_name = dataversecontact_table.case.route_name
}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: config,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.dataversecontact_scopes.all", tfjsonpath.New("scopes"),
					knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("default")})),
				statecheck.ExpectKnownValue("data.dataversecontact_table_definitions.default", tfjsonpath.New("definitions").AtSliceIndex(0).AtMapKey("route_name"),
					knownvalue.StringExact("case")),
				statecheck.ExpectKnownValue("data.dataversecontact_table_definitions.default", tfjsonpath.New("definitions").AtSliceIndex(0).AtMapKey("field_count"),
					knownvalue.Int64Exact(2)),
				statecheck.ExpectKnownValue("data.dataversecontact_table.case", tfjsonpath.New("dataverse_table"), knownvalue.StringExact("incidents")),
				statecheck.ExpectKnownValue("data.dataversecontact_table.case", tfjsonpath.New("field_count"), knownvalue.Int64Exact(2)),
			},
			Check: resource.TestCheckResourceAttrWith("data.dataversecontact_table.case", "schema_json", func(v string) error {
				if !strings.Contains(v, `"routeName":"case"`) {
					return fmt.Errorf("schema_json does not look like the case schema: %s", v)
				}
				return nil
			}),
		}},
	})
}
