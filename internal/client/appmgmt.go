package client

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

// AppMgmt holds App Management V2 API calls (spec-confirmed from
// CasaOS-AppManagement/api/app_management/openapi.yaml).
//
// All paths live under the /v2/app_management prefix — CONFIRMED against a
// live ZimaOS device (2026-08-22): the device's gateway only forwards known
// prefixed routes (/v1/*, /v2/*) to backend services; bare paths like
// "/compose" 404 before ever reaching the app-management service, even when
// hitting the device directly on its LAN port.
//
// Confirmed contract (do NOT change without re-checking the spec):
//   POST   /v2/app_management/compose               body: raw YAML (content-type: application/yaml)
//                                                  query: dry_run, check_port_conflict (booleans)
//   GET    /v2/app_management/compose/{id}          response: { data: { status, update_available, compose, store_info } }
//   PUT    /v2/app_management/compose/{id}           body: raw YAML (content-type: application/yaml)
//                                                  query: dry_run, check_port_conflict
//   DELETE /v2/app_management/compose/{id}           query: delete_config_folder (bool, API default true)
//   PUT    /v2/app_management/compose/{id}/status   body: JSON string ("start"|"restart"|"stop")
//   GET    /v2/app_management/info                  response: { architecture }

const appMgmtBase = "/v2/app_management"

// ComposeApp holds the fields the provider reads back from the API.
type ComposeApp struct {
	Name            string         `json:"name,omitempty"`
	Status          string         `json:"status,omitempty"`
	UpdateAvailable bool           `json:"update_available,omitempty"`
	Compose         map[string]any `json:"compose,omitempty"`
}

// ComposeYAML re-serializes the API's own compose object back into a YAML
// string. This is the server's expanded/normalized view (it fills in fields
// like cpu_shares and deploy.resources that a hand-authored compose file
// might omit, and — for CasaOS Store apps — an x-casaos metadata block), so
// it is NOT guaranteed byte-identical to whatever compose_yaml a user
// originally submitted. Returns "" (no error) if the API didn't return a
// compose object (e.g. a stripped-down mock in tests).
//
// yaml.v3 sorts map keys alphabetically when marshaling map[string]any, so
// this is deterministic across calls — verified empirically 2026-08-22 to
// avoid producing spurious plan diffs from key-order churn alone.
func (a *ComposeApp) ComposeYAML() (string, error) {
	if a.Compose == nil {
		return "", nil
	}
	b, err := yaml.Marshal(a.Compose)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ComposeAppDetail is the /compose/{id} response (data-wrapped envelope).
type ComposeAppDetail struct {
	Data ComposeApp `json:"data"`
}

// AppInfo is the /info response.
type AppInfo struct {
	Architecture string `json:"architecture,omitempty"`
}

// InstallCompose installs a compose app by posting the raw YAML body (spec-confirmed
// content-type: application/yaml). dryRun/checkPort mirror the confirmed query params.
func (c *Client) InstallCompose(ctx context.Context, yamlBody string, dryRun, checkPort bool) (*ComposeApp, error) {
	q := url.Values{}
	q.Set("dry_run", b2s(dryRun))
	q.Set("check_port_conflict", b2s(checkPort))
	var out ComposeApp
	if err := c.doRaw(ctx, http.MethodPost, appMgmtBase+"/compose", q, yamlBody, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCompose reads an installed compose app by project name (id). The API returns a
// data-wrapped envelope; we unpack it into ComposeApp.
func (c *Client) GetCompose(ctx context.Context, id string) (*ComposeApp, error) {
	var detail ComposeAppDetail
	if err := c.Do(ctx, http.MethodGet, appMgmtBase+"/compose/"+url.PathEscape(id), nil, nil, &detail); err != nil {
		return nil, err
	}
	return &detail.Data, nil
}

// UpdateCompose applies settings by posting raw YAML to PUT /compose/{id}
// (spec-confirmed content-type: application/yaml).
func (c *Client) UpdateCompose(ctx context.Context, id, yamlBody string, dryRun, checkPort bool) error {
	q := url.Values{}
	q.Set("dry_run", b2s(dryRun))
	q.Set("check_port_conflict", b2s(checkPort))
	return c.doRaw(ctx, http.MethodPut, appMgmtBase+"/compose/"+url.PathEscape(id), q, yamlBody, nil)
}

// SetComposeStatus drives start/restart/stop via PUT /compose/{id}/status
// (body: JSON string enum "start"|"restart"|"stop").
func (c *Client) SetComposeStatus(ctx context.Context, id, status string) error {
	return c.Do(ctx, http.MethodPut, appMgmtBase+"/compose/"+url.PathEscape(id)+"/status", nil, status, nil)
}

// DeleteCompose removes an app. retainConfig maps to delete_config_folder = !retainConfig
// (API default is true; the resource deliberately inverts it to avoid silent data loss).
func (c *Client) DeleteCompose(ctx context.Context, id string, retainConfig bool) error {
	q := url.Values{}
	q.Set("delete_config_folder", b2s(!retainConfig))
	return c.Do(ctx, http.MethodDelete, appMgmtBase+"/compose/"+url.PathEscape(id), q, nil, nil)
}

// GetInfo reads device facts from /info.
func (c *Client) GetInfo(ctx context.Context) (*AppInfo, error) {
	var out AppInfo
	if err := c.Do(ctx, http.MethodGet, appMgmtBase+"/info", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// doRaw posts a raw (YAML) string body with the matching content-type. out, if non-nil,
// receives the decoded JSON response.
func (c *Client) doRaw(ctx context.Context, method, path string, query url.Values, body string, out any) error {
	u := c.BaseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.authHeader())
	req.Header.Set("Content-Type", "application/yaml")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := readAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return &APIError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(data))}
	}
	if out != nil && len(data) > 0 {
		return unmarshal(data, out)
	}
	return nil
}

func b2s(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
