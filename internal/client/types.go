package client

import "encoding/json"

// APIError represents an error response from the API.
type APIError struct {
	StatusCode int    `json:"statusCode"`
	Error      string `json:"error"`
	Message    string `json:"message"`
}

func (e *APIError) String() string {
	return e.Message
}

// --- Table Types ---

// TableDefinition represents a table as returned by GET /table-definitions.
type TableDefinition struct {
	RouteName            string          `json:"routeName"`
	Source               string          `json:"source"`
	Description          string          `json:"description,omitempty"`
	Icon                 string          `json:"icon,omitempty"`
	DataverseTable       string          `json:"dataverseTable"`
	DataverseLogicalName string          `json:"dataverseLogicalName"`
	RequiredPermission   string          `json:"requiredPermission"`
	PrimaryKey           string          `json:"primaryKey"`
	FieldCount           int             `json:"fieldCount"`
	Fields               json.RawMessage `json:"fields,omitempty"`
}

// TableDefinitionsResponse is the response from GET /table-definitions.
type TableDefinitionsResponse struct {
	Definitions []TableDefinition `json:"definitions"`
}

// DraftInfo represents metadata about a draft.
type DraftInfo struct {
	RouteName  string `json:"routeName"`
	Entity     string `json:"entity,omitempty"`
	Scope      string `json:"scope"`
	UpdatedAt  string `json:"updatedAt"`
	UpdatedBy  string `json:"updatedBy"`
	ChangeType string `json:"changeType"`
}

// TableManagerResponse is the response from GET /table-manager/{table}.
type TableManagerResponse struct {
	Source string          `json:"source"`
	Draft  *DraftInfo      `json:"draft"`
	Schema json.RawMessage `json:"schema"`
}

// SaveDraftResponse is the response from PUT /table-manager/{table}.
type SaveDraftResponse struct {
	Message    string          `json:"message"`
	Draft      *DraftInfo      `json:"draft"`
	Validation *ValidationInfo `json:"validation,omitempty"`
}

// ValidationInfo represents validation results.
type ValidationInfo struct {
	Valid    bool     `json:"valid"`
	Errors   []string `json:"errors"`
	Warnings []string `json:"warnings"`
}

// PublishResponse is the response from POST /table-manager/publish.
type PublishResponse struct {
	Message    string                     `json:"message"`
	Published  []string                   `json:"published"`
	Errors     []string                   `json:"errors"`
	Validation map[string]*ValidationInfo `json:"validation,omitempty"`
}

// PublishRequest is the request body for POST /table-manager/publish.
type PublishRequest struct {
	Tables []string `json:"tables"`
}

// UnpublishRequest is the request body for POST /table-manager/unpublish.
type UnpublishRequest struct {
	Tables []string `json:"tables"`
}

// UnpublishResponse is the response from POST /table-manager/unpublish.
type UnpublishResponse struct {
	Message     string   `json:"message"`
	Unpublished []string `json:"unpublished"`
	Errors      []string `json:"errors"`
}

// RemoveRequest is the request body for POST /table-manager/remove.
type RemoveRequest struct {
	Tables []string `json:"tables"`
}

// RemoveResponse is the response from POST /table-manager/remove.
type RemoveResponse struct {
	Message string   `json:"message"`
	Removed []string `json:"removed"`
	Errors  []string `json:"errors"`
}

// DeleteRecycledResponse is the response from DELETE /table-manager/recycled/{table}.
type DeleteRecycledResponse struct {
	Message   string `json:"message"`
	RouteName string `json:"routeName"`
}

// --- Custom API Types ---

// CustomApiDefinition represents a custom API as returned by GET /custom-api-definitions.
type CustomApiDefinition struct {
	RouteName              string          `json:"routeName"`
	Source                 string          `json:"source"`
	DataverseUniqueName    string          `json:"dataverseUniqueName"`
	DisplayName            string          `json:"displayName,omitempty"`
	Description            string          `json:"description,omitempty"`
	RequiredPermission     string          `json:"requiredPermission,omitempty"`
	IsFunction             bool            `json:"isFunction"`
	ApiType                string          `json:"apiType,omitempty"`
	BindingType            string          `json:"bindingType"`
	BoundEntityLogicalName string          `json:"boundEntityLogicalName,omitempty"`
	BoundEntitySetName     string          `json:"boundEntitySetName,omitempty"`
	PublicInvoke           bool            `json:"publicInvoke"`
	RequestParameters      json.RawMessage `json:"requestParameters,omitempty"`
	ResponseProperties     json.RawMessage `json:"responseProperties,omitempty"`
}

// CustomApiDefinitionsResponse is the response from GET /custom-api-definitions.
type CustomApiDefinitionsResponse struct {
	Definitions []CustomApiDefinition `json:"definitions"`
}

// CustomApiManagerResponse is the response from GET /custom-api-manager/{api}.
type CustomApiManagerResponse struct {
	Source string          `json:"source"`
	Draft  *DraftInfo      `json:"draft"`
	Schema json.RawMessage `json:"schema"`
}

// CustomApiSaveDraftResponse is the response from PUT /custom-api-manager/{api}.
type CustomApiSaveDraftResponse struct {
	Message string     `json:"message"`
	Draft   *DraftInfo `json:"draft"`
}

// CustomApiPublishRequest is the request body for POST /custom-api-manager/publish.
type CustomApiPublishRequest struct {
	Apis []string `json:"apis"`
}

// CustomApiPublishResponse is the response from POST /custom-api-manager/publish.
type CustomApiPublishResponse struct {
	Message   string   `json:"message"`
	Published []string `json:"published"`
	Errors    []string `json:"errors"`
}

// CustomApiRemoveRequest is the request body for POST /custom-api-manager/remove.
type CustomApiRemoveRequest struct {
	Apis []string `json:"apis"`
}

// CustomApiRemoveResponse is the response from POST /custom-api-manager/remove.
type CustomApiRemoveResponse struct {
	Message string   `json:"message"`
	Removed []string `json:"removed"`
	Errors  []string `json:"errors"`
}

// --- Permissions Types ---

// PublishDefaultsRequest is the request body for PUT /table-manager/defaults.
// It mirrors the shape of a scope's defaults.json.
type PublishDefaultsRequest struct {
	Permissions       map[string][]string `json:"permissions"`
	AllowSelfRegister bool                `json:"allowSelfRegister"`
	CompanyModel      *CompanyModel       `json:"companyModel,omitempty"`
}

// CompanyModel mirrors the API's per-scope companyModel config. Omitted (nil)
// means the classic parent-account (multi-contact) model.
type CompanyModel struct {
	// "parent-account" or "associated-accounts".
	Strategy           string                   `json:"strategy"`
	AssociatedAccounts *AssociatedAccountsQuery `json:"associatedAccounts,omitempty"`
}

// AssociatedAccountsQuery describes how a single contact's linked accounts are
// resolved for the associated-accounts model. Supply relationship or fetchXml.
type AssociatedAccountsQuery struct {
	Relationship     string `json:"relationship,omitempty"`
	AccountIDField   string `json:"accountIdField,omitempty"`
	AccountNameField string `json:"accountNameField,omitempty"`
	FetchXML         string `json:"fetchXml,omitempty"`
}

// PublishDefaultsResponse is the response from PUT /table-manager/defaults.
type PublishDefaultsResponse struct {
	Message string `json:"message,omitempty"`
}

// --- Scopes Types ---

// ScopesResponse is the response from GET /api/v2/_admin/scopes.
type ScopesResponse struct {
	Scopes []string `json:"scopes"`
}
