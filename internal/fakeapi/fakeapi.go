// Package fakeapi is an in-memory stand-in for the Dataverse Contact API's
// admin endpoints, for running the provider's resources through real
// Terraform plan/apply/import cycles without a live deployment.
//
// It stores schemas as decoded JSON (map[string]any), never as the provider's
// own structs, so a bug in the provider's types cannot be mirrored here and
// cancel itself out. Where the real API rewrites what it is given — deriving a
// lookup's bindField, dropping columns Dataverse doesn't have, deriving a
// polymorphic family's expands — the fake does the same, because those
// rewrites are where the provider's plan/state bugs have come from.
//
// Response shapes are pinned to recordings of the real API by
// TestFakeMatchesContractFixtures, so the fake cannot drift into answering in
// a shape the API never uses.
package fakeapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// Key is the admin connection key the fake accepts.
const Key = "test-connection-key"

// Issue is one validation finding, in the API's ValidationIssue shape.
type Issue struct {
	Severity string `json:"severity"`
	Field    string `json:"field"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

// Server is a running fake. Configure its behaviour fields before the
// Terraform step that needs them; all methods are safe for concurrent use.
type Server struct {
	*httptest.Server

	mu sync.Mutex

	// NavProps maps a lookup field's logical name to the navigation property
	// the API would discover for it. A lookup field with a lookupTable and no
	// bindField gets bindField set from here on save, as the API's
	// enrichLookupBindFields does.
	NavProps map[string]string

	// UnknownColumns lists, per Dataverse logical name, columns the table does
	// not have. Saving a schema that declares one reports FIELD_NOT_FOUND and
	// strips the field, as the API does.
	UnknownColumns map[string][]string

	// Warnings are returned as validation warnings on every table save and
	// publish.
	Warnings []Issue

	// PolymorphicTargets lists, per polymorphic lookup field, the target
	// entities the API would derive expands for on read.
	PolymorphicTargets map[string][]string

	// PublishErrors makes a publish report a per-route error (with a 200)
	// instead of publishing that route.
	PublishErrors map[string]string

	// Fail makes requests answer with a status code instead, keyed by method
	// and the path after the scope, e.g. "PUT table-manager/case" or
	// "GET scopes". For the error paths a failed apply, refresh or destroy
	// takes.
	Fail map[string]int

	tables     map[string]map[string]map[string]any // scope → route → published schema
	drafts     map[string]map[string]map[string]any // scope → route → draft schema
	recycled   map[string]map[string]map[string]any
	apis       map[string]map[string]map[string]any
	apiDrafts  map[string]map[string]map[string]any
	apiRecycle map[string]map[string]map[string]any
	defaults   map[string]map[string]any // scope → last PUT defaults body
	requests   []string
}

// New starts a fake and closes it when the test ends.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{
		tables:     map[string]map[string]map[string]any{},
		drafts:     map[string]map[string]map[string]any{},
		recycled:   map[string]map[string]map[string]any{},
		apis:       map[string]map[string]map[string]any{},
		apiDrafts:  map[string]map[string]map[string]any{},
		apiRecycle: map[string]map[string]map[string]any{},
		defaults:   map[string]map[string]any{},
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// ProviderConfig is the provider block pointing at this fake.
func (s *Server) ProviderConfig() string {
	return fmt.Sprintf(`
provider "dataversecontact" {
  api_url        = %q
  connection_key = %q
}
`, s.URL, Key)
}

// ── Inspection and out-of-band changes ─────────────────────────────────

// CustomApi returns a copy of a published custom API's stored schema, or nil.
func (s *Server) CustomApi(scope, route string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.apis[scope][route])
}

// Defaults returns the last defaults body PUT for a scope, or nil.
func (s *Server) Defaults(scope string) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return clone(s.defaults[scope])
}

// TableCount is the number of published, drafted and binned tables left in a
// scope — zero once a destroy has cleaned up properly.
func (s *Server) TableCount(scope string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tables[scope]) + len(s.drafts[scope]) + len(s.recycled[scope])
}

// ApiCount is TableCount for custom APIs.
func (s *Server) ApiCount(scope string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.apis[scope]) + len(s.apiDrafts[scope]) + len(s.apiRecycle[scope])
}

// DeleteTable removes a table entirely, as if someone deleted it outside
// Terraform.
func (s *Server) DeleteTable(scope, route string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tables[scope], route)
	delete(s.drafts[scope], route)
	delete(s.recycled[scope], route)
}

// SetFail makes requests matching key ("METHOD path-after-scope") answer
// with status until ClearFail. Use it between steps; set Fail directly only
// before the first.
func (s *Server) SetFail(key string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail == nil {
		s.Fail = map[string]int{}
	}
	s.Fail[key] = status
}

// ClearFail removes every injected failure.
func (s *Server) ClearFail() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Fail = nil
}

// DeleteCustomApi is DeleteTable for custom APIs.
func (s *Server) DeleteCustomApi(scope, route string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.apis[scope], route)
	delete(s.apiDrafts[scope], route)
	delete(s.apiRecycle[scope], route)
}

// Requests lists "METHOD /path" for every request served so far.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// ── HTTP ───────────────────────────────────────────────────────────────

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, r.Method+" "+r.URL.Path)

	if r.Header.Get("Authorization") != "Bearer "+Key {
		writeError(w, http.StatusUnauthorized, "Invalid admin connection key")
		return
	}

	rest, ok := strings.CutPrefix(r.URL.Path, "/api/v2/_admin/")
	if !ok {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	scope, path, _ := strings.Cut(rest, "/")
	if rest == "scopes" {
		path = "scopes"
	}
	if status, fail := s.Fail[r.Method+" "+path]; fail {
		writeError(w, status, "Injected failure")
		return
	}
	if rest == "scopes" && r.Method == http.MethodGet {
		s.scopes(w)
		return
	}

	var body map[string]any
	if r.Body != nil && r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON body")
			return
		}
	}

	tables := kind{
		published: s.tables, drafts: s.drafts, recycled: s.recycled,
		listKey: "tables", noun: "table", nouns: "table(s)", label: "Table", publishedSource: "built-in",
	}
	apis := kind{
		published: s.apis, drafts: s.apiDrafts, recycled: s.apiRecycle,
		listKey: "apis", noun: "custom API", nouns: "custom API(s)", label: "Custom API", publishedSource: "published",
	}

	switch {
	case path == "table-definitions" && r.Method == http.MethodGet:
		s.tableDefinitions(w, scope)
	case path == "custom-api-definitions" && r.Method == http.MethodGet:
		s.customApiDefinitions(w, scope)
	case path == "table-manager/defaults" && r.Method == http.MethodPut:
		s.defaults[scope] = body
		writeJSON(w, http.StatusOK, map[string]any{
			"message":  fmt.Sprintf("Updated default permissions for scope %q", scope),
			"scope":    scope,
			"defaults": body,
		})
	case strings.HasPrefix(path, "table-manager/"):
		s.manager(w, r.Method, scope, strings.TrimPrefix(path, "table-manager/"), body, tables)
	case strings.HasPrefix(path, "custom-api-manager/"):
		s.manager(w, r.Method, scope, strings.TrimPrefix(path, "custom-api-manager/"), body, apis)
	default:
		writeError(w, http.StatusNotFound, "Not found")
	}
}

// kind is what differs between the table and custom API managers, which the
// API builds from the same draft lifecycle.
type kind struct {
	published, drafts, recycled map[string]map[string]map[string]any
	listKey, noun, nouns, label string
	publishedSource             string
}

func (k kind) isTable() bool { return k.listKey == "tables" }

func (s *Server) manager(w http.ResponseWriter, method, scope, path string, body map[string]any, k kind) {
	switch {
	case path == "publish" && method == http.MethodPost:
		s.publish(w, scope, names(body, k.listKey), k)
	case path == "unpublish" && method == http.MethodPost:
		batch(w, scope, names(body, k.listKey), "unpublished", "Unpublished %d "+k.nouns, "No published override found",
			func(name string) bool { return take(k.published, scope, name) != nil })
	case path == "remove" && method == http.MethodPost:
		batch(w, scope, names(body, k.listKey), "removed", "Removed %d "+k.nouns+" to recycle bin", "No published or draft schema found",
			func(name string) bool {
				schema := take(k.drafts, scope, name)
				if published := take(k.published, scope, name); published != nil {
					schema = published
				}
				if schema == nil {
					return false
				}
				put(k.recycled, scope, name, schema)
				return true
			})
	case strings.HasPrefix(path, "recycled/") && method == http.MethodDelete:
		name := strings.TrimPrefix(path, "recycled/")
		if take(k.recycled, scope, name) == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("%s not found in recycle bin: %s", k.label, name))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"message":   fmt.Sprintf("%s %q permanently deleted", k.label, name),
			"routeName": name,
		})
	case !strings.Contains(path, "/"):
		s.item(w, method, scope, path, body, k)
	default:
		writeError(w, http.StatusNotFound, "Not found")
	}
}

func (s *Server) item(w http.ResponseWriter, method, scope, name string, body map[string]any, k kind) {
	switch method {
	case http.MethodGet:
		if draft := k.drafts[scope][name]; draft != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"source": "draft",
				"draft":  draftEntry(scope, name, draft, "modified"),
				"schema": draft,
			})
			return
		}
		published := k.published[scope][name]
		if published == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("Unknown %s: %s", k.noun, name))
			return
		}
		schema := clone(published)
		if k.isTable() {
			s.deriveExpands(schema)
		}
		writeJSON(w, http.StatusOK, map[string]any{"source": k.publishedSource, "draft": nil, "schema": schema})

	case http.MethodPut:
		if body == nil || body["routeName"] != name {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("URL %s %q does not match body routeName", k.noun, name))
			return
		}
		changeType := "new"
		if k.published[scope][name] != nil {
			changeType = "modified"
		}
		resp := map[string]any{"message": "Draft saved"}
		if k.isTable() {
			if body["dataverseTable"] == nil || body["primaryKey"] == nil {
				writeError(w, http.StatusBadRequest, "Request body must be a valid schema with routeName, dataverseTable, and primaryKey")
				return
			}
			resp["validation"] = s.validateAndStrip(body)
			s.enrichBindFields(body)
		}
		put(k.drafts, scope, name, body)
		resp["draft"] = draftEntry(scope, name, body, changeType)
		writeJSON(w, http.StatusOK, resp)

	case http.MethodDelete:
		if take(k.drafts, scope, name) == nil {
			writeError(w, http.StatusNotFound, fmt.Sprintf("No draft found for %s: %s", k.noun, name))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"message": "Draft discarded", "routeName": name})

	default:
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) publish(w http.ResponseWriter, scope string, routeNames []string, k kind) {
	if routeNames == nil {
		for name := range k.drafts[scope] {
			routeNames = append(routeNames, name)
		}
		sort.Strings(routeNames)
	}
	published := []string{}
	errors := []map[string]any{}
	validation := map[string]any{}
	for _, name := range routeNames {
		if msg, fail := s.PublishErrors[name]; fail {
			errors = append(errors, map[string]any{"routeName": name, "error": msg})
			continue
		}
		draft := take(k.drafts, scope, name)
		if draft == nil {
			errors = append(errors, map[string]any{"routeName": name, "error": "Draft blob not found"})
			continue
		}
		put(k.published, scope, name, draft)
		published = append(published, name)
		if k.isTable() {
			validation[name] = s.validation(nil)
		}
	}
	if len(published) == 0 && len(errors) == 0 {
		writeError(w, http.StatusNotFound, "No drafts found to publish")
		return
	}
	resp := map[string]any{
		"message":   fmt.Sprintf("Published %d %s", len(published), k.nouns),
		"published": published,
		"errors":    errors,
	}
	if len(validation) > 0 {
		resp["validation"] = validation
	}
	writeJSON(w, http.StatusOK, resp)
}

// batch is the API's createBatchActionHandler: per-route success or error,
// a 200 either way, and a 404 only when nothing at all happened.
func batch(w http.ResponseWriter, scope string, routeNames []string, resultKey, messageFormat, notFound string, action func(string) bool) {
	if len(routeNames) == 0 {
		writeError(w, http.StatusBadRequest, "Request body must include a non-empty list")
		return
	}
	succeeded := []string{}
	errors := []map[string]any{}
	for _, name := range routeNames {
		if action(name) {
			succeeded = append(succeeded, name)
		} else {
			errors = append(errors, map[string]any{"routeName": name, "error": notFound})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"message": fmt.Sprintf(messageFormat, len(succeeded)),
		resultKey: succeeded,
		"errors":  errors,
	})
}

func (s *Server) scopes(w http.ResponseWriter) {
	seen := map[string]bool{}
	for _, m := range []map[string]map[string]map[string]any{s.tables, s.apis} {
		for scope := range m {
			seen[scope] = true
		}
	}
	for scope := range s.defaults {
		seen[scope] = true
	}
	scopes := []string{}
	for scope := range seen {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	writeJSON(w, http.StatusOK, map[string]any{"scopes": scopes})
}

func (s *Server) tableDefinitions(w http.ResponseWriter, scope string) {
	defs := []map[string]any{}
	for _, name := range sortedKeys(s.tables[scope]) {
		h := s.tables[scope][name]
		fields, _ := h["fields"].(map[string]any)
		def := map[string]any{
			"routeName":            name,
			"source":               "published",
			"description":          h["description"],
			"icon":                 h["icon"],
			"dataverseTable":       h["dataverseTable"],
			"dataverseLogicalName": h["dataverseLogicalName"],
			"requiredPermission":   h["requiredPermission"],
			"primaryKey":           h["primaryKey"],
			"aliases":              orEmpty(h["aliases"]),
			"defaultSelect":        h["defaultSelect"],
			"fieldCount":           len(fields),
		}
		defs = append(defs, def)
	}
	writeJSON(w, http.StatusOK, map[string]any{"definitions": defs})
}

func (s *Server) customApiDefinitions(w http.ResponseWriter, scope string) {
	defs := []map[string]any{}
	for _, name := range sortedKeys(s.apis[scope]) {
		def := clone(s.apis[scope][name])
		def["source"] = "published"
		defs = append(defs, def)
	}
	writeJSON(w, http.StatusOK, map[string]any{"definitions": defs})
}

// ── The API's schema rewrites ──────────────────────────────────────────

// validateAndStrip reports FIELD_NOT_FOUND for every declared column the
// table lacks and deletes it from the schema — what the API's PUT does.
func (s *Server) validateAndStrip(schema map[string]any) map[string]any {
	logical, _ := schema["dataverseLogicalName"].(string)
	fields, _ := schema["fields"].(map[string]any)
	var errors []Issue
	for _, col := range s.UnknownColumns[logical] {
		if _, declared := fields[col]; !declared {
			continue
		}
		errors = append(errors, Issue{
			Severity: "error", Field: "fields." + col, Code: "FIELD_NOT_FOUND",
			Message: fmt.Sprintf("Field %q does not exist on %s", col, logical),
		})
		delete(fields, col)
		if sel, ok := schema["defaultSelect"].([]any); ok {
			schema["defaultSelect"] = without(sel, col)
		}
	}
	return s.validation(errors)
}

func (s *Server) validation(errors []Issue) map[string]any {
	if errors == nil {
		errors = []Issue{}
	}
	warnings := s.Warnings
	if warnings == nil {
		warnings = []Issue{}
	}
	return map[string]any{"valid": len(errors) == 0, "errors": errors, "warnings": warnings}
}

// enrichBindFields is the API's enrichLookupBindFields: never overrides a
// set bindField, and only fills one when the navigation property differs
// from the field name.
func (s *Server) enrichBindFields(schema map[string]any) {
	fields, _ := schema["fields"].(map[string]any)
	for name, raw := range fields {
		f, _ := raw.(map[string]any)
		if f == nil || f["type"] != "lookup" || f["lookupTable"] == nil || f["bindField"] != nil {
			continue
		}
		if nav, ok := s.NavProps[name]; ok && nav != name {
			f["bindField"] = nav
		}
	}
}

// deriveExpands adds the `<field>_<target>` expands the API derives onto a
// polymorphic family's anchor table on every read.
func (s *Server) deriveExpands(schema map[string]any) {
	rule, _ := schema["polymorphicLookup"].(map[string]any)
	if rule == nil {
		return
	}
	field, _ := rule["field"].(string)
	expands, _ := schema["expands"].([]any)
	for _, target := range s.PolymorphicTargets[field] {
		expands = append(expands, map[string]any{
			"lookupField":  field + "_" + target,
			"relatedTable": target + "s",
			"fields":       []any{map[string]any{"name": target + "id", "type": "string", "description": "Derived"}},
		})
	}
	if len(expands) > 0 {
		schema["expands"] = expands
	}
}

// ── Helpers ────────────────────────────────────────────────────────────

func draftEntry(scope, name string, schema map[string]any, changeType string) map[string]any {
	entry := map[string]any{
		"routeName":  name,
		"scope":      scope,
		"updatedAt":  time.Now().UTC().Format(time.RFC3339),
		"updatedBy":  "terraform@connection-key",
		"changeType": changeType,
	}
	if entity, ok := schema["dataverseLogicalName"]; ok {
		entry["entity"] = entity
	}
	return entry
}

func names(body map[string]any, key string) []string {
	raw, ok := body[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func put(m map[string]map[string]map[string]any, scope, name string, v map[string]any) {
	if m[scope] == nil {
		m[scope] = map[string]map[string]any{}
	}
	m[scope][name] = v
}

func take(m map[string]map[string]map[string]any, scope, name string) map[string]any {
	v := m[scope][name]
	delete(m[scope], name)
	return v
}

func clone(v map[string]any) map[string]any {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func without(list []any, drop string) []any {
	out := make([]any, 0, len(list))
	for _, v := range list {
		if v != drop {
			out = append(out, v)
		}
	}
	return out
}

func orEmpty(v any) any {
	if v == nil {
		return []any{}
	}
	return v
}

func sortedKeys(m map[string]map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError answers in the API's HttpError shape.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"error":      http.StatusText(status),
		"message":    message,
		"statusCode": status,
	})
}
