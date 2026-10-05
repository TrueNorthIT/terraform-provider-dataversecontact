package provider

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/TrueNorthIT/terraform-provider-dataversecontact/internal/fakeapi"
)

// Plan-time checks that warn rather than fail must still apply cleanly, and
// round-trip whatever they warned about.
func TestAccTable_validateConfigWarnings(t *testing.T) {
	s := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             tablesGone(s),
		Steps: []resource.TestStep{{
			Config: caseTable(s, baseFields+`
    regardingobjectid = { type = "lookup", description = "Regarding" }
    progress          = { type = "string", description = "Clashes with the process", lookup_table = "ignored" }`,
				`
  business_process = { expose_as = "title" }

  polymorphic_lookup {
    field               = "regardingobjectid"
    required_permission = "servicerecord"
    read_only           = false
    exclude_targets     = ["sb_internal"]
    business_process    = {}
  }`),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("business_process").AtMapKey("expose_as"), knownvalue.StringExact("title")),
				statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("polymorphic_lookup").AtMapKey("read_only"), knownvalue.Bool(false)),
				statecheck.ExpectKnownValue(caseAddress, tfjsonpath.New("fields").AtMapKey("progress").AtMapKey("lookup_table"), knownvalue.StringExact("ignored")),
			},
		}},
	})
}

func TestAccTable_twoProcessesOneName(t *testing.T) {
	s := fakeapi.New(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: caseTable(s, baseFields+`
    regardingobjectid = { type = "lookup", description = "Regarding" }`,
				`
  business_process = { expose_as = "stage" }

  polymorphic_lookup {
    field               = "regardingobjectid"
    required_permission = "servicerecord"
    business_process    = { expose_as = "stage" }
  }`),
			ExpectError: regexp.MustCompile(`Two business processes under one name`),
		}},
	})
}
