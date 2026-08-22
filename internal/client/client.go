package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client is a thin HTTP client for the ZimaOS APIs.
// Auth: API key sent as the `Authorization` header (confirmed from App Management spec).
type Client struct {
	BaseURL    string
	Token      string
	HTTPClient *http.Client
}

// APIError represents a non-2xx response from the ZimaOS API.
type APIError struct {
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("zimaos api error %d: %s", e.StatusCode, e.Body)
}

// New builds a Client from a host (base URL) and an API token.
func New(host, token string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(host, "/"),
		Token:      token,
		HTTPClient: http.DefaultClient,
	}
}

// Token returns the auth header value. CONFIRMED against a live ZimaOS device
// (2026-08-22): the /v2/app_management/* endpoints reject a "Bearer "-prefixed
// Authorization header (401 "Unauthorized") and accept the bare access token
// (200). Strip any "Bearer " prefix the caller might have pasted in so a token
// copied from elsewhere still works.
func (c *Client) authHeader() string {
	t := strings.TrimSpace(c.Token)
	if t == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(t), "bearer ") {
		return strings.TrimSpace(t[len("bearer "):])
	}
	return t
}

// readAll is a small indirection over io.ReadAll so both Do and doRaw share behavior.
func readAll(r io.Reader) ([]byte, error) { return io.ReadAll(r) }

// TokenSet is the pair returned by POST /login.
type TokenSet struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// Login exchanges a username/password for an access/refresh token pair.
// CONFIRMED against a live ZimaOS device (2026-08-22): POST /v1/users/login
// with a flat body { username, password } (NOT nested under "auth" — that
// shape 400s with "Parameters Error"); response data.token.{access_token,refresh_token}.
// The returned access token can be set on the Client via SetToken so
// subsequent calls authenticate. This avoids hand-pasting a (stale-able)
// token into provider config.
func (c *Client) Login(ctx context.Context, username, password string) (*TokenSet, error) {
	var resp struct {
		Data struct {
			Token TokenSet `json:"token"`
		} `json:"data"`
	}
	if err := c.Do(ctx, http.MethodPost, "/v1/users/login", nil, map[string]string{
		"username": username, "password": password,
	}, &resp); err != nil {
		return nil, err
	}
	if resp.Data.Token.AccessToken == "" {
		return nil, fmt.Errorf("login returned no access_token")
	}
	return &resp.Data.Token, nil
}

// SetToken updates the bearer token carried by the client.
func (c *Client) SetToken(token string) { c.Token = token }

// unmarshal is a small indirection over json.Unmarshal.
func unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// Do performs an authenticated request. query is optional; body is optional (JSON-encoded).
// out, if non-nil, receives the decoded JSON response.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", c.authHeader())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
