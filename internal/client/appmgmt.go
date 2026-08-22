package client

import (
	"context"
	"net/http"
	"net/url"
)

// AppMgmt holds App Management V2 API calls (spec-confirmed from
// CasaOS-AppManagement/api/app_management/openapi.yaml).
//
// Confirmed paths:
//   POST   /compose                        (install; dry_run + check_port_conflict query params)
//   GET    /compose/{id}
//   PUT    /compose/{id}                   (update settings)
//   PATCH  /compose/{id}                   (version bump — explicit action only, never auto)
//   DELETE /compose/{id}                   (delete_config_folder query param)
//   PUT    /compose/{id}/status            (body: start|restart|stop)
//   GET    /info                           (architecture etc.)

// ComposeApp mirrors the minimal fields the provider needs. The API body is the full
// compose-spec Project struct; we only model what the resource schema exposes.
type ComposeApp struct {
	Name              string `json:"name,omitempty"`
	ComposeYAML       string `json:"-"` // sent as raw YAML body, not JSON field
	Status            string `json:"status,omitempty"`
	UpdateAvailable   bool   `json:"update_available,omitempty"`
}

// AppInfo is the /info response (architecture, etc.).
type AppInfo struct {
	Architecture string `json:"architecture,omitempty"`
}

// InstallCompose installs a compose app. dryRun/checkPort mirror the confirmed query params.
func (c *Client) InstallCompose(ctx context.Context, yamlBody string, dryRun, checkPort bool) (*ComposeApp, error) {
	q := url.Values{}
	q.Set("dry_run", b2s(dryRun))
	q.Set("check_port_conflict", b2s(checkPort))
	// The API accepts application/yaml; for simplicity we send JSON-wrapped here and
	// rely on the server accepting the compose content. See AGENTS.md §4.
	var out ComposeApp
	// NOTE: real implementation posts raw YAML to POST /compose. This scaffold posts the
	// structured body to keep the client compilable; replace with raw-YAML POST when wiring.
	err := c.Do(ctx, http.MethodPost, "/compose", q, map[string]string{"compose": yamlBody}, &out)
	return &out, err
}

// GetCompose reads an installed compose app by project name (id).
func (c *Client) GetCompose(ctx context.Context, id string) (*ComposeApp, error) {
	var out ComposeApp
	err := c.Do(ctx, http.MethodGet, "/compose/"+url.PathEscape(id), nil, nil, &out)
	return &out, err
}

// SetComposeStatus drives start/restart/stop via PUT /compose/{id}/status.
func (c *Client) SetComposeStatus(ctx context.Context, id, status string) error {
	return c.Do(ctx, http.MethodPut, "/compose/"+url.PathEscape(id)+"/status", nil, status, nil)
}

// DeleteCompose removes an app. retainConfig maps to delete_config_folder = !retainConfig.
func (c *Client) DeleteCompose(ctx context.Context, id string, retainConfig bool) error {
	q := url.Values{}
	q.Set("delete_config_folder", b2s(!retainConfig))
	return c.Do(ctx, http.MethodDelete, "/compose/"+url.PathEscape(id), q, nil, nil)
}

// GetInfo reads device facts from /info.
func (c *Client) GetInfo(ctx context.Context) (*AppInfo, error) {
	var out AppInfo
	err := c.Do(ctx, http.MethodGet, "/info", nil, nil, &out)
	return &out, err
}

func b2s(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
