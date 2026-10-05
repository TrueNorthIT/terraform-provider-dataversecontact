package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/TrueNorthIT/terraform-provider-dataversecontact/internal/fakeapi"
)

// What a user sees when the API fails partway through an apply, refresh,
// destroy or import. Each failure must stop Terraform with an error naming
// the operation, and leave state such that the next run recovers.

// failStep injects one failure before the step and expects it to surface.
func failStep(s *fakeapi.Server, config, key string, want string) resource.TestStep {
	return resource.TestStep{
		PreConfig:   func() { s.ClearFail(); s.SetFail(key, 500) },
		Config:      config,
		ExpectError: regexp.MustCompile(want),
	}
}

func okStep(s *fakeapi.Server, config string) resource.TestStep {
	return resource.TestStep{PreConfig: s.ClearFail, Config: config}
}

func TestAccTable_apiFailures(t *testing.T) {
	s := fakeapi.New(t)
	config := caseTable(s, baseFields)
	updated := caseTable(s, baseFields+`
    prioritycode = { type = "choice", description = "Priority" }`)
	destroy := failStep(s, config, "DELETE table-manager/recycled/case", "Failed to delete table")
	destroy.Destroy = true

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{
			failStep(s, config, "PUT table-manager/case", "Failed to create table"),
			// A failed publish discards the draft it just saved.
			failStep(s, config, "POST table-manager/publish", `failed to publish \(draft discarded\)`),
			okStep(s, config),
			failStep(s, config, "GET table-manager/case", "Failed to read table"),
			failStep(s, updated, "PUT table-manager/case", "Failed to update table"),
			destroy,
			okStep(s, config),
			{
				ResourceName:  caseAddress,
				ImportState:   true,
				ImportStateId: "no-slash",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestAccCustomApi_apiFailures(t *testing.T) {
	s := fakeapi.New(t)
	config := calendarApi(s, "Expands a calendar")
	updated := calendarApi(s, "Expands a calendar into slots")
	destroy := failStep(s, config, "DELETE custom-api-manager/recycled/expand-calendar", "Failed to delete custom API")
	destroy.Destroy = true

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			failStep(s, config, "PUT custom-api-manager/expand-calendar", "Failed to create custom API"),
			failStep(s, config, "POST custom-api-manager/publish", `failed to publish custom API \(draft discarded\)`),
			okStep(s, config),
			failStep(s, config, "GET custom-api-manager/expand-calendar", "Failed to read custom API"),
			failStep(s, updated, "PUT custom-api-manager/expand-calendar", "Failed to update custom API"),
			destroy,
			okStep(s, config),
			{
				ResourceName:  calendarAddress,
				ImportState:   true,
				ImportStateId: "default/",
				ExpectError:   regexp.MustCompile(`Invalid Import ID`),
			},
		},
	})
}

func TestAccPermissionsSync_apiFailures(t *testing.T) {
	s := fakeapi.New(t)
	config := permissionsSync(s, `  default_permissions = { case = ["me"] }`)
	updated := permissionsSync(s, `  default_permissions = { case = ["me", "write"] }`)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			failStep(s, config, "PUT table-manager/defaults", "Failed to publish permissions"),
			okStep(s, config),
			failStep(s, updated, "PUT table-manager/defaults", "Failed to publish permissions"),
			okStep(s, updated),
		},
	})
}

func TestAccDataSources_apiFailures(t *testing.T) {
	s := fakeapi.New(t)
	ds := func(block string) string { return s.ProviderConfig() + block }
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			failStep(s, ds(`data "dataversecontact_scopes" "all" {}`), "GET scopes", "Failed to read scopes"),
			failStep(s, ds(`data "dataversecontact_table_definitions" "d" { scope = "default" }`),
				"GET table-definitions", "Failed to read table definitions"),
			failStep(s, ds(`data "dataversecontact_table" "t" {
  scope      = "default"
  route_name = "case"
}`), "GET table-manager/case", "Failed to read table"),
		},
	})
}

// ── Provider configuration ─────────────────────────────────────────────

func TestAccProvider_configuration(t *testing.T) {
	s := fakeapi.New(t)
	for _, v := range []string{"DATAVERSE_CONTACT_API_URL", "DATAVERSE_CONTACT_CONNECTION_KEY", "DATAVERSE_CONTACT_API_KEY"} {
		t.Setenv(v, "")
	}
	scopes := `
data "dataversecontact_scopes" "all" {}
`
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      `provider "dataversecontact" { connection_key = "k" }` + scopes,
				ExpectError: regexp.MustCompile(`Missing API URL`),
			},
			{
				Config:      `provider "dataversecontact" { api_url = "` + s.URL + `" }` + scopes,
				ExpectError: regexp.MustCompile(`Missing Admin Connection Key`),
			},
			{
				// The deprecated api_key still authenticates.
				Config: `provider "dataversecontact" {
  api_url = "` + s.URL + `"
  api_key = "` + fakeapi.Key + `"
}` + scopes,
			},
			{
				// So does everything from the environment, legacy key included.
				PreConfig: func() {
					t.Setenv("DATAVERSE_CONTACT_API_URL", s.URL)
					t.Setenv("DATAVERSE_CONTACT_API_KEY", fakeapi.Key)
				},
				Config: `provider "dataversecontact" {}` + scopes,
			},
			{
				PreConfig: func() {
					t.Setenv("DATAVERSE_CONTACT_API_KEY", "")
					t.Setenv("DATAVERSE_CONTACT_CONNECTION_KEY", "wrong-key")
				},
				Config:      `provider "dataversecontact" {}` + scopes,
				ExpectError: regexp.MustCompile(`API error \(401\)`),
			},
		},
	})
}
