package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// The save and the publish can both answer 200 without doing what was asked.
// Each case must fail, name the API's reason, and discard the draft.
func TestSaveAndPublishRefusesHalfDoneSaves(t *testing.T) {
	notFound := ValidationIssue{Severity: "error", Field: "fields.new_typo", Code: "FIELD_NOT_FOUND", Message: `Field "new_typo" does not exist on incident`}
	kept := ValidationIssue{Severity: "error", Field: "defaultSelect", Code: "DEFAULT_SELECT_UNKNOWN", Message: "not a field"}
	cases := []struct {
		name     string
		save     SaveDraftResponse
		publish  PublishResponse
		wantErr  string
		wantReqs []string
	}{
		{
			name:     "stripped field",
			save:     SaveDraftResponse{Validation: &ValidationInfo{Errors: []ValidationIssue{notFound, kept}}},
			wantErr:  `new_typo: Field "new_typo" does not exist on incident`,
			wantReqs: []string{"PUT table-manager/case", "DELETE table-manager/case"},
		},
		{
			name:     "route error",
			publish:  PublishResponse{Errors: []RouteError{{RouteName: "other", Error: "x"}, {RouteName: "case", Error: "Draft blob not found"}}},
			wantErr:  `could not publish "case": Draft blob not found`,
			wantReqs: []string{"PUT table-manager/case", "POST table-manager/publish", "DELETE table-manager/case"},
		},
		{
			name:     "neither published nor failed",
			publish:  PublishResponse{Published: []string{"other"}},
			wantErr:  `did not report "case" as published`,
			wantReqs: []string{"PUT table-manager/case", "POST table-manager/publish", "DELETE table-manager/case"},
		},
		{
			name:     "other validation errors still publish",
			save:     SaveDraftResponse{Validation: &ValidationInfo{Errors: []ValidationIssue{kept}}},
			publish:  PublishResponse{Published: []string{"case"}},
			wantReqs: []string{"PUT table-manager/case", "POST table-manager/publish"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var reqs []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reqs = append(reqs, r.Method+" "+strings.TrimPrefix(r.URL.Path, "/api/v2/_admin/default/"))
				switch r.Method {
				case "PUT":
					_ = json.NewEncoder(w).Encode(tc.save)
				case "POST":
					_ = json.NewEncoder(w).Encode(tc.publish)
				case "DELETE":
					_ = json.NewEncoder(w).Encode(map[string]string{"message": "Draft discarded"})
				}
			}))
			defer server.Close()

			c := NewClient(server.URL, "test-key")
			save, err := c.SaveAndPublishTable(context.Background(), "default", "case", json.RawMessage(`{"routeName":"case"}`))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %v, want it to contain %q", err, tc.wantErr)
			}
			if save == nil {
				t.Error("the save response must come back, for its warnings")
			}
			if !reflect.DeepEqual(reqs, tc.wantReqs) {
				t.Errorf("requests %v, want %v", reqs, tc.wantReqs)
			}
		})
	}
}

func TestStrippedFieldsErrorListsOnlyStrippedFields(t *testing.T) {
	err := strippedFields("case", &ValidationInfo{Errors: []ValidationIssue{
		{Field: "fields.a", Code: "FIELD_NOT_FOUND", Message: "a is gone"},
		{Field: "defaultSelect", Code: "FIELD_NOT_FOUND", Message: "not stripped: not under fields"},
		{Field: "fields.b", Code: "TYPE_MISMATCH", Message: "not stripped: other code"},
	}})
	var stripped *StrippedFieldsError
	if !errors.As(err, &stripped) {
		t.Fatalf("want *StrippedFieldsError, got %v", err)
	}
	if len(stripped.Fields) != 1 || stripped.FieldName(stripped.Fields[0]) != "a" {
		t.Errorf("stripped %+v, want only a", stripped.Fields)
	}
	if strippedFields("case", nil) != nil || strippedFields("case", &ValidationInfo{}) != nil {
		t.Error("no validation, or no errors, strips nothing")
	}
}

func TestSaveAndPublishCustomApiRefusesRouteError(t *testing.T) {
	var reqs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs = append(reqs, r.Method+" "+strings.TrimPrefix(r.URL.Path, "/api/v2/_admin/default/"))
		switch r.Method {
		case "PUT":
			_ = json.NewEncoder(w).Encode(CustomApiSaveDraftResponse{Message: "Draft saved"})
		case "POST":
			_ = json.NewEncoder(w).Encode(CustomApiPublishResponse{Errors: []RouteError{{RouteName: "cal", Error: "Draft blob not found"}}})
		case "DELETE":
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Draft discarded"})
		}
	}))
	defer server.Close()

	c := NewClient(server.URL, "test-key")
	save, err := c.SaveAndPublishCustomApi(context.Background(), "default", "cal", json.RawMessage(`{"routeName":"cal"}`))
	if err == nil || !strings.Contains(err.Error(), `could not publish "cal": Draft blob not found`) {
		t.Fatalf("error %v", err)
	}
	if save == nil {
		t.Error("the save response must come back, for its warnings")
	}
	want := []string{"PUT custom-api-manager/cal", "POST custom-api-manager/publish", "DELETE custom-api-manager/cal"}
	if !reflect.DeepEqual(reqs, want) {
		t.Errorf("requests %v, want %v", reqs, want)
	}
}
