package fakeapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestFakeMatchesContractFixtures sends the fake the request behind each
// contract fixture and checks the answer: every key the fake emits must also
// be in the real response, holding the same JSON type. The fake may say less
// than the API but nothing different, so a provider bug cannot hide behind a
// fake that answers in a shape the API never uses.
//
// The echoed schema and defaults bodies are not compared. They are whatever
// the provider sent, and the schema round-trip test covers them.
func TestFakeMatchesContractFixtures(t *testing.T) {
	s := New(t)
	s.Warnings = []Issue{{Severity: "warning", Field: "fields.title", Code: "TYPE_MISMATCH", Message: "m"}}
	s.UnknownColumns = map[string][]string{"incident": {"new_gone"}}
	s.PublishErrors = map[string]string{"contact": "Draft blob not found"}

	table := map[string]any{
		"routeName": "case", "dataverseTable": "incidents", "dataverseLogicalName": "incident",
		"primaryKey": "incidentid", "requiredPermission": "case",
		"defaultSelect": []any{"incidentid", "new_gone"}, "lookupFields": []any{"title"},
		"fields": map[string]any{
			"incidentid": map[string]any{"type": "string", "description": "Id"},
			"new_gone":   map[string]any{"type": "string", "description": "Not in Dataverse"},
		},
	}
	api := map[string]any{"routeName": "expand-calendar", "dataverseUniqueName": "ExpandCalendar", "isFunction": true}

	for _, step := range []struct {
		fixture, method, path string
		body                  any
		status                int
	}{
		{"put-table-draft", "PUT", "default/table-manager/case", table, 200},
		{"post-table-publish", "POST", "default/table-manager/publish", map[string]any{"tables": []string{"case", "contact"}}, 200},
		{"get-table-published", "GET", "default/table-manager/case", nil, 200},
		{"get-table-definitions", "GET", "default/table-definitions", nil, 200},
		{"get-table-404", "GET", "default/table-manager/nope", nil, 404},
		{"post-table-unpublish", "POST", "default/table-manager/unpublish", map[string]any{"tables": []string{"case", "contact"}}, 200},
		{"put-table-draft", "PUT", "default/table-manager/case", table, 200},
		{"post-table-remove", "POST", "default/table-manager/remove", map[string]any{"tables": []string{"case", "contact"}}, 200},
		{"delete-table-recycled", "DELETE", "default/table-manager/recycled/case", nil, 200},
		{"put-custom-api-draft", "PUT", "default/custom-api-manager/expand-calendar", api, 200},
		{"post-custom-api-publish", "POST", "default/custom-api-manager/publish", map[string]any{"apis": []string{"expand-calendar", "other"}}, 200},
		{"get-custom-api-published", "GET", "default/custom-api-manager/expand-calendar", nil, 200},
		{"get-custom-api-definitions", "GET", "default/custom-api-definitions", nil, 200},
		{"get-custom-api-404", "GET", "default/custom-api-manager/nope", nil, 404},
		{"post-custom-api-remove", "POST", "default/custom-api-manager/remove", map[string]any{"apis": []string{"expand-calendar", "other"}}, 200},
		{"put-defaults", "PUT", "default/table-manager/defaults", map[string]any{"permissions": map[string]any{"case": []string{"me"}}}, 200},
		{"get-scopes", "GET", "scopes", nil, 200},
	} {
		got := call(t, s, step.method, step.path, step.body, step.status)
		want := fixture(t, step.fixture)
		keyed := keyedByRoute[step.fixture]
		fakeShape, realShape := shape(got, keyed), shape(want, keyed)
		var wrong []string
		for path, kind := range fakeShape {
			if apiKind, ok := realShape[path]; !ok {
				wrong = append(wrong, path+": the API never sends this")
			} else if kind != apiKind && kind != "null" && apiKind != "null" {
				wrong = append(wrong, path+": fake sends "+kind+", the API sends "+apiKind)
			}
		}
		sort.Strings(wrong)
		for _, w := range wrong {
			t.Errorf("%s %s (%s) %s", step.method, step.path, step.fixture, w)
		}
	}
}

func call(t *testing.T, s *Server, method, path string, body any, status int) any {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, s.URL+"/api/v2/_admin/"+path, r)
	req.Header.Set("Authorization", "Bearer "+Key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != status {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s: status %d, want %d: %s", method, path, resp.StatusCode, status, b)
	}
	var v any
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func fixture(t *testing.T, name string) any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "testdata", "contract", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// keyedByRoute lists, per fixture, the objects whose keys are route names
// rather than fields, so their children are compared under "*".
var keyedByRoute = map[string]map[string]bool{
	"post-table-publish": {".validation": true},
}

// opaque are subtrees echoed back from the request, not part of the contract.
var opaque = map[string]bool{".schema": true, ".defaults": true}

// shape flattens a JSON value into path → JSON type. Array elements share a
// path, so every element must fit the same shape.
func shape(v any, keyedByRoute map[string]bool) map[string]string {
	out := map[string]string{}
	var walk func(v any, path string)
	walk = func(v any, path string) {
		switch x := v.(type) {
		case map[string]any:
			out[path] = "object"
			if opaque[path] {
				return
			}
			for k, child := range x {
				if keyedByRoute[path] {
					k = "*"
				}
				walk(child, path+"."+k)
			}
		case []any:
			out[path] = "array"
			for _, child := range x {
				walk(child, path+"[]")
			}
		case string:
			out[path] = "string"
		case float64:
			out[path] = "number"
		case bool:
			out[path] = "bool"
		case nil:
			if _, seen := out[path]; !seen {
				out[path] = "null"
			}
		}
	}
	walk(v, "")
	delete(out, "")
	return out
}
