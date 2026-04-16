package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// auth0TokenResponse is the response from Auth0's /oauth/token endpoint.
type auth0TokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int    `json:"expires_in"`
}

// Auth0TokenExchange performs an OAuth2 client credentials exchange with Auth0
// and returns the access token.
func Auth0TokenExchange(domain, clientID, clientSecret, audience string) (string, error) {
	domain = strings.TrimRight(domain, "/")
	if !strings.HasPrefix(domain, "https://") {
		domain = "https://" + domain
	}
	tokenURL := domain + "/oauth/token"

	reqBody, _ := json.Marshal(map[string]string{
		"client_id":     clientID,
		"client_secret": clientSecret,
		"audience":      audience,
		"grant_type":    "client_credentials",
	})

	httpClient := &http.Client{Timeout: 10 * time.Second}
	resp, err := httpClient.Post(tokenURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read token response: %w", err)
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Auth0 returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp auth0TokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("Auth0 returned empty access token")
	}

	return tokenResp.AccessToken, nil
}
