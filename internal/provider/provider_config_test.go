package provider

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/TrueNorthIT/terraform-provider-dataversecontact/internal/client"
	"github.com/TrueNorthIT/terraform-provider-dataversecontact/internal/fakeapi"
)

// configure runs the provider's Configure with api_url and connection_key
// set, plus attrs, and returns the client it built.
func configure(t *testing.T, attrs map[string]tftypes.Value) (*client.Client, string) {
	t.Helper()
	ctx := context.Background()
	p := New("test")()
	var schemaResp provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &schemaResp)
	typ := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)

	vals := map[string]tftypes.Value{}
	for name, at := range typ.AttributeTypes {
		vals[name] = tftypes.NewValue(at, nil)
	}
	vals["api_url"] = tftypes.NewValue(tftypes.String, "https://api.example.com")
	vals["connection_key"] = tftypes.NewValue(tftypes.String, "k")
	for name, v := range attrs {
		vals[name] = v
	}

	var resp provider.ConfigureResponse
	p.Configure(ctx, provider.ConfigureRequest{
		Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(typ, vals)},
	}, &resp)
	var errs []string
	for _, d := range resp.Diagnostics.Errors() {
		errs = append(errs, d.Summary()+": "+d.Detail())
	}
	c, _ := resp.ResourceData.(*client.Client)
	return c, strings.Join(errs, "; ")
}

// #29: timeout and max_retries, from the provider block or the environment.
func TestProviderConfigure_timeoutAndRetries(t *testing.T) {
	str := func(v string) tftypes.Value { return tftypes.NewValue(tftypes.String, v) }
	num := func(v int64) tftypes.Value { return tftypes.NewValue(tftypes.Number, v) }

	cases := []struct {
		name        string
		attrs       map[string]tftypes.Value
		env         map[string]string
		wantTimeout time.Duration
		wantRetries int
		wantErr     string
	}{
		{name: "defaults", wantTimeout: client.DefaultTimeout, wantRetries: client.DefaultMaxRetries},
		{name: "configured",
			attrs:       map[string]tftypes.Value{"timeout": str("2m"), "max_retries": num(0)},
			wantTimeout: 2 * time.Minute, wantRetries: 0},
		{name: "environment",
			env:         map[string]string{"DATAVERSE_CONTACT_TIMEOUT": "90s", "DATAVERSE_CONTACT_MAX_RETRIES": "5"},
			wantTimeout: 90 * time.Second, wantRetries: 5},
		{name: "configuration beats environment",
			attrs:       map[string]tftypes.Value{"timeout": str("45s"), "max_retries": num(1)},
			env:         map[string]string{"DATAVERSE_CONTACT_TIMEOUT": "90s", "DATAVERSE_CONTACT_MAX_RETRIES": "5"},
			wantTimeout: 45 * time.Second, wantRetries: 1},
		{name: "unknown falls back",
			attrs: map[string]tftypes.Value{
				"timeout":     tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
				"max_retries": tftypes.NewValue(tftypes.Number, tftypes.UnknownValue),
			},
			wantTimeout: client.DefaultTimeout, wantRetries: client.DefaultMaxRetries},
		{name: "bad duration", attrs: map[string]tftypes.Value{"timeout": str("soon")},
			wantErr: `Invalid timeout: timeout must be a positive duration such as "90s" or "2m", got "soon".`},
		{name: "zero duration", attrs: map[string]tftypes.Value{"timeout": str("0s")},
			wantErr: "Invalid timeout"},
		{name: "bad duration in environment", env: map[string]string{"DATAVERSE_CONTACT_TIMEOUT": "-1m"},
			wantErr: `DATAVERSE_CONTACT_TIMEOUT must be a positive duration`},
		{name: "negative retries", attrs: map[string]tftypes.Value{"max_retries": num(-1)},
			wantErr: "Invalid max_retries: max_retries must be 0 or more, got -1."},
		{name: "bad retries in environment", env: map[string]string{"DATAVERSE_CONTACT_MAX_RETRIES": "lots"},
			wantErr: `DATAVERSE_CONTACT_MAX_RETRIES must be a whole number, 0 or more, got "lots".`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DATAVERSE_CONTACT_TIMEOUT", "")
			t.Setenv("DATAVERSE_CONTACT_MAX_RETRIES", "")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			c, errs := configure(t, tc.attrs)
			if tc.wantErr != "" {
				if !strings.Contains(errs, tc.wantErr) {
					t.Errorf("errors = %q, want %q", errs, tc.wantErr)
				}
				if c != nil {
					t.Error("an invalid configuration must not hand resources a client")
				}
				return
			}
			if errs != "" {
				t.Fatalf("unexpected errors: %s", errs)
			}
			if c.HTTPClient.Timeout != tc.wantTimeout || c.MaxRetries != tc.wantRetries {
				t.Errorf("timeout = %s, max_retries = %d; want %s and %d",
					c.HTTPClient.Timeout, c.MaxRetries, tc.wantTimeout, tc.wantRetries)
			}
		})
	}
}

// The same settings through Terraform itself.
func TestAccProvider_timeoutAndRetries(t *testing.T) {
	s := fakeapi.New(t)
	t.Setenv("DATAVERSE_CONTACT_TIMEOUT", "")
	t.Setenv("DATAVERSE_CONTACT_MAX_RETRIES", "")
	block := func(extra string) string {
		return `provider "dataversecontact" {
  api_url        = "` + s.URL + `"
  connection_key = "` + fakeapi.Key + `"
` + extra + `
}
data "dataversecontact_scopes" "all" {}
`
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		// The valid step goes last: the test's destroy runs with it.
		Steps: []resource.TestStep{
			{Config: block(`  timeout = "a while"`), ExpectError: regexp.MustCompile(`Invalid timeout`)},
			{Config: block(`  max_retries = -2`), ExpectError: regexp.MustCompile(`Invalid max_retries`)},
			{Config: block(`  timeout = "90s"
  max_retries = 0`)},
		},
	})
}
