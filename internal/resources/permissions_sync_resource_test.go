package resources

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// invite_url is held to the API's inviteUrlProblem, so a value the defaults
// PUT would refuse with a 400 fails at plan time instead.
func TestInviteURLProblem(t *testing.T) {
	for value, want := range map[string]string{
		"https://portal.example.gov.uk/invite#token={token}": "",
		"https://portal.example.gov.uk/invite?token={token}": "",
		"https://portal.example.gov.uk/invite/{token}":       "",
		"HTTPS://portal.example.gov.uk/invite#token={token}": "",
		"https://portal.example.gov.uk:8443/a/{token}?x=1#y": "",
		"https://portal.example.gov.uk/invite":               "must contain {token}",
		"http://portal.example.gov.uk/invite#token={token}":  "must be https",
		"ftp://portal.example.gov.uk/{token}":                "must be https",
		"/invite#token={token}":                              "isn't a URL",
		"portal.example.gov.uk/invite#token={token}":         "isn't a URL",
		"https://{token}":                 "",
		"https://exa mple.gov.uk/{token}": "isn't a URL",
		"https://portal.example.gov.uk/" + strings.Repeat("a", 463) + "{token}": "", // 500
		"https://portal.example.gov.uk/" + strings.Repeat("a", 464) + "{token}": "up to 500 characters",
		// Counted as JavaScript counts: é is one UTF-16 unit but two bytes.
		"https://portal.example.gov.uk/" + strings.Repeat("é", 463) + "{token}": "",
		// An emoji is two UTF-16 units.
		"https://portal.example.gov.uk/" + strings.Repeat("😀", 232) + "{token}": "up to 500 characters",
	} {
		got := inviteURLProblem(value)
		switch {
		case want == "" && got != "":
			t.Errorf("%.60q: refused (%s), want accepted", value, got)
		case want != "" && !strings.Contains(got, want):
			t.Errorf("%.60q: got %q, want it to say %q", value, got, want)
		case want != "" && !strings.Contains(got, `#token={token}`):
			t.Errorf("%.60q: %q should show the fragment example", value, got)
		}
	}
}

func TestInviteURLValidator(t *testing.T) {
	ctx := context.Background()
	v := inviteURLValidator{}
	if v.Description(ctx) == "" || v.MarkdownDescription(ctx) != v.Description(ctx) {
		t.Fatal("the validator needs a description, the same in markdown")
	}
	for name, c := range map[string]struct {
		value     types.String
		wantError bool
	}{
		"valid":   {types.StringValue("https://portal.example.gov.uk/invite#token={token}"), false},
		"invalid": {types.StringValue("http://portal.example.gov.uk/invite#token={token}"), true},
		"null":    {types.StringNull(), false},
		"unknown": {types.StringUnknown(), false},
	} {
		t.Run(name, func(t *testing.T) {
			resp := &validator.StringResponse{}
			v.ValidateString(ctx, validator.StringRequest{Path: path.Root("invite_url"), ConfigValue: c.value}, resp)
			if resp.Diagnostics.HasError() != c.wantError {
				t.Fatalf("errors = %v, want error %v", resp.Diagnostics.Errors(), c.wantError)
			}
		})
	}
}
