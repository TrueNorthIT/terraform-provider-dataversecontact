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

// fakeSaveAPI answers the save, read-back, publish and discard calls of
// SaveAndPublishTable / SaveAndPublishCustomApi, and records them.
type fakeSaveAPI struct {
	save          any            // PUT body
	savedFields   map[string]any // the draft's fields on read-back
	getStatus     int            // read-back status, if not 200
	getSource     string         // read-back source, if not "draft"
	publish       any            // POST body
	discardStatus int            // DELETE status, if not 200
	reqs          []string
}

func (f *fakeSaveAPI) start(t *testing.T) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.reqs = append(f.reqs, r.Method+" "+strings.TrimPrefix(r.URL.Path, "/api/v2/_admin/default/"))
		switch r.Method {
		case "PUT":
			_ = json.NewEncoder(w).Encode(f.save)
		case "GET":
			if f.getStatus != 0 {
				w.WriteHeader(f.getStatus)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "read failed"})
				return
			}
			source := f.getSource
			if source == "" {
				source = "draft"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"source": source, "draft": map[string]any{"routeName": "case"},
				"schema": map[string]any{"routeName": "case", "fields": f.savedFields},
			})
		case "POST":
			_ = json.NewEncoder(w).Encode(f.publish)
		case "DELETE":
			if f.discardStatus != 0 {
				w.WriteHeader(f.discardStatus)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "blob storage unavailable"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "Draft discarded"})
		}
	}))
	t.Cleanup(server.Close)
	return NewClient(server.URL, "test-key")
}

const sentTable = `{"routeName":"case","dataverseLogicalName":"incident","fields":{"title":{"type":"string"},"new_typo":{"type":"string"}}}`

// The save and the publish can both answer 200 without doing what was asked.
// Each case must fail, name the API's reason, and say what became of the draft.
func TestSaveAndPublishRefusesHalfDoneSaves(t *testing.T) {
	both := map[string]any{"title": map[string]any{}, "new_typo": map[string]any{}}
	onlyTitle := map[string]any{"title": map[string]any{}}
	// What a real PUT answers after a strip: the re-validation of what's left.
	afterStrip := SaveDraftResponse{Validation: &ValidationInfo{Errors: []ValidationIssue{
		{Field: "lookupFields.new_typo", Code: "LOOKUP_FIELD_UNKNOWN", Message: "not defined in fields"},
	}}}
	put, readBack, publish, discard := "PUT table-manager/case", "GET table-manager/case", "POST table-manager/publish", "DELETE table-manager/case"
	cases := []struct {
		name     string
		api      fakeSaveAPI
		wantErr  []string
		wantReqs []string
	}{
		{
			name:     "stripped field, found by reading back",
			api:      fakeSaveAPI{save: afterStrip, savedFields: onlyTitle},
			wantErr:  []string{`new_typo: The API dropped "new_typo" from the saved draft, which it does when incident has no such column`, "(draft discarded)"},
			wantReqs: []string{put, readBack, discard},
		},
		{
			name: "stripped field with the API's own message",
			api: fakeSaveAPI{save: SaveDraftResponse{Validation: &ValidationInfo{Errors: []ValidationIssue{
				{Field: "fields.new_typo", Code: "FIELD_NOT_FOUND", Message: `Field "new_typo" does not exist on incident`},
			}}}, savedFields: onlyTitle},
			wantErr:  []string{`new_typo: Field "new_typo" does not exist on incident`},
			wantReqs: []string{put, readBack, discard},
		},
		{
			name:     "stripped field, discard fails",
			api:      fakeSaveAPI{save: afterStrip, savedFields: onlyTitle, discardStatus: 500},
			wantErr:  []string{`the draft of "case" could not be discarded and is still saved, so a Table Manager "Publish all" would publish it`, "blob storage unavailable"},
			wantReqs: []string{put, readBack, discard},
		},
		{
			name:     "read-back fails",
			api:      fakeSaveAPI{save: SaveDraftResponse{}, getStatus: 500}, // not a status a retry would repeat
			wantErr:  []string{`could not read back the saved draft of "case"`, "nothing was published (draft discarded)"},
			wantReqs: []string{put, readBack, discard},
		},
		{
			name:     "read-back finds no draft",
			api:      fakeSaveAPI{save: SaveDraftResponse{}, savedFields: both, getSource: "published"},
			wantErr:  []string{"answered with the published table instead of the draft"},
			wantReqs: []string{put, readBack, discard},
		},
		{
			name:     "route error",
			api:      fakeSaveAPI{save: SaveDraftResponse{}, savedFields: both, publish: PublishResponse{Errors: []RouteError{{RouteName: "other", Error: "x"}, {RouteName: "case", Error: "Draft blob not found"}}}},
			wantErr:  []string{`reported an error publishing "case", so it may not have been published: Draft blob not found (draft discarded)`},
			wantReqs: []string{put, readBack, publish, discard},
		},
		{
			name:     "route error, draft already gone",
			api:      fakeSaveAPI{save: SaveDraftResponse{}, savedFields: both, publish: PublishResponse{Errors: []RouteError{{RouteName: "case", Error: "boom"}}}, discardStatus: 404},
			wantErr:  []string{"(no draft left)"},
			wantReqs: []string{put, readBack, publish, discard},
		},
		{
			name:     "neither published nor failed",
			api:      fakeSaveAPI{save: SaveDraftResponse{}, savedFields: both, publish: PublishResponse{Published: []string{"other"}}},
			wantErr:  []string{`did not report "case" as published, so it may not have been`},
			wantReqs: []string{put, readBack, publish, discard},
		},
		{
			name:     "other validation errors still publish",
			api:      fakeSaveAPI{save: SaveDraftResponse{Validation: &ValidationInfo{Errors: []ValidationIssue{{Field: "defaultSelect.x", Code: "DEFAULT_SELECT_UNKNOWN"}}}}, savedFields: both, publish: PublishResponse{Published: []string{"case"}}},
			wantReqs: []string{put, readBack, publish},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.api.start(t)
			save, err := c.SaveAndPublishTable(context.Background(), "default", "case", json.RawMessage(sentTable))
			if len(tc.wantErr) == 0 && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, want := range tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Errorf("error %v, want it to contain %q", err, want)
				}
			}
			if save == nil {
				t.Error("the save response must come back, for its warnings")
			}
			if !reflect.DeepEqual(tc.api.reqs, tc.wantReqs) {
				t.Errorf("requests %v, want %v", tc.api.reqs, tc.wantReqs)
			}
		})
	}
}

func TestSaveAndPublishStrippedFieldsError(t *testing.T) {
	api := fakeSaveAPI{save: SaveDraftResponse{}, savedFields: map[string]any{}}
	c := api.start(t)
	_, err := c.SaveAndPublishTable(context.Background(), "default", "case", json.RawMessage(sentTable))
	var stripped *StrippedFieldsError
	if !errors.As(err, &stripped) {
		t.Fatalf("want *StrippedFieldsError, got %v", err)
	}
	if len(stripped.Fields) != 2 || stripped.Fields[0].Name != "new_typo" || stripped.Fields[1].Name != "title" {
		t.Errorf("fields %+v, want new_typo and title, sorted", stripped.Fields)
	}
	if stripped.Route != "case" || stripped.Draft != "draft discarded" {
		t.Errorf("route %q, draft %q", stripped.Route, stripped.Draft)
	}

	// A schema with no fields has nothing to strip, so there's no read-back.
	none := fakeSaveAPI{save: SaveDraftResponse{}, publish: PublishResponse{Published: []string{"case"}}}
	c = none.start(t)
	if _, err := c.SaveAndPublishTable(context.Background(), "default", "case", json.RawMessage(`{"routeName":"case"}`)); err != nil {
		t.Fatal(err)
	}
	if want := []string{"PUT table-manager/case", "POST table-manager/publish"}; !reflect.DeepEqual(none.reqs, want) {
		t.Errorf("requests %v, want %v", none.reqs, want)
	}
}

func TestIsStrippedField(t *testing.T) {
	for _, tc := range []struct {
		issue ValidationIssue
		want  bool
	}{
		{ValidationIssue{Field: "fields.a", Code: "FIELD_NOT_FOUND"}, true},
		{ValidationIssue{Field: "defaultSelect.a", Code: "FIELD_NOT_FOUND"}, false},
		{ValidationIssue{Field: "fields.a", Code: "TYPE_MISMATCH"}, false},
	} {
		if got := IsStrippedField(tc.issue); got != tc.want {
			t.Errorf("IsStrippedField(%+v) = %v", tc.issue, got)
		}
	}
}

func TestSaveAndPublishCustomApiRefusesRouteError(t *testing.T) {
	for _, tc := range []struct {
		name          string
		discardStatus int
		want          string
	}{
		{"discarded", 0, `reported an error publishing "cal", so it may not have been published: Draft blob not found (draft discarded)`},
		{"discard fails", 500, `the draft of "cal" could not be discarded and is still saved, so a Table Manager "Publish all" would publish it`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := fakeSaveAPI{
				save:          CustomApiSaveDraftResponse{Message: "Draft saved"},
				publish:       CustomApiPublishResponse{Errors: []RouteError{{RouteName: "cal", Error: "Draft blob not found"}}},
				discardStatus: tc.discardStatus,
			}
			c := api.start(t)
			save, err := c.SaveAndPublishCustomApi(context.Background(), "default", "cal", json.RawMessage(`{"routeName":"cal"}`))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want it to contain %q", err, tc.want)
			}
			if save == nil {
				t.Error("the save response must come back, for its warnings")
			}
			want := []string{"PUT custom-api-manager/cal", "POST custom-api-manager/publish", "DELETE custom-api-manager/cal"}
			if !reflect.DeepEqual(api.reqs, want) {
				t.Errorf("requests %v, want %v", api.reqs, want)
			}
		})
	}
}
