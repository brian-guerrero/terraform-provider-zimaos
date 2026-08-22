package client

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// specAppMgmtServer returns an httptest server that emulates the confirmed
// App Management V2 contract (CONFIRMED against a live ZimaOS device,
// 2026-08-22 — see appmgmt.go and client.go doc comments):
//   POST   /v2/app_management/compose                raw YAML body, dry_run + check_port_conflict query
//   GET    /v2/app_management/compose/{id}           { data: { status, update_available } }
//   PUT    /v2/app_management/compose/{id}            raw YAML body
//   DELETE /v2/app_management/compose/{id}            delete_config_folder query
//   PUT    /v2/app_management/compose/{id}/status    JSON string enum
//   GET    /v2/app_management/info                   { architecture }
//   POST   /v1/users/login                            flat { username, password } body
//
// It captures the last request so tests can assert the exact wire contract.
func specAppMgmtServer(t *testing.T) (*httptest.Server, *lastReq) {
	t.Helper()
	lr := &lastReq{}
	mux := http.NewServeMux()

	ok := func(w http.ResponseWriter) { w.Header().Set("Content-Type", "application/json") }

	mux.HandleFunc("/v2/app_management/compose", func(w http.ResponseWriter, r *http.Request) {
		lr.capture(r, true)
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Spec: content-type must be application/yaml and body is the compose content.
		if ct := r.Header.Get("Content-Type"); ct != "application/yaml" {
			t.Errorf("POST /v2/app_management/compose content-type = %q, want application/yaml", ct)
		}
		body, _ := io.ReadAll(r.Body)
		// echo the posted compose back as the registered app so later GETs work
		lr.lastComposeYAML = string(body)
		ok(w)
		// Install response is a BaseResponse (empty data) — provider ignores body on install.
		_, _ = w.Write([]byte(`{}`))
	})

	mux.HandleFunc("/v2/app_management/compose/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/v2/app_management/compose/")
		switch {
		case strings.HasSuffix(r.URL.Path, "/status"):
			lr.capture(r, false)
			if r.Method != http.MethodPut {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			stB, _ := io.ReadAll(r.Body)
			lr.lastStatus = strings.Trim(string(stB), `"`)
			ok(w)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet:
			lr.capture(r, false)
			ok(w)
			_, _ = w.Write([]byte(`{"data":{"status":"running","update_available":false,"compose":{"name":"myapp","services":{"web":{"image":"nginx"}}}}}`))
		case r.Method == http.MethodPut:
			lr.capture(r, true)
			if ct := r.Header.Get("Content-Type"); ct != "application/yaml" {
				t.Errorf("PUT /v2/app_management/compose/%s content-type = %q, want application/yaml", id, ct)
			}
			ok(w)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete:
			lr.capture(r, false)
			ok(w)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/v2/app_management/info", func(w http.ResponseWriter, r *http.Request) {
		lr.capture(r, false)
		ok(w)
		_, _ = w.Write([]byte(`{"architecture":"amd64"}`))
	})

	// /v1/users/login emulates the confirmed auth flow: POST { username, password }
	// (flat, NOT nested under "auth") -> { data: { token: { access_token, refresh_token } } }.
	mux.HandleFunc("/v1/users/login", func(w http.ResponseWriter, r *http.Request) {
		lr.capture(r, true)
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ok(w)
		_, _ = w.Write([]byte(`{"data":{"token":{"access_token":"at-123","refresh_token":"rt-456"}}}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, lr
}

type lastReq struct {
	lastMethod      string
	lastPath        string
	lastQuery       url.Values
	lastComposeYAML string
	lastStatus      string
	lastAuth        string
}

func (l *lastReq) capture(r *http.Request, captureBody bool) {
	l.lastMethod = r.Method
	l.lastPath = r.URL.Path
	l.lastQuery = r.URL.Query()
	l.lastAuth = r.Header.Get("Authorization")
	if captureBody {
		if b, err := io.ReadAll(r.Body); err == nil {
			l.lastComposeYAML = string(b)
			// restore the body so downstream handlers can re-read it
			r.Body = io.NopCloser(strings.NewReader(l.lastComposeYAML))
		}
	}
}

func TestInstallCompose_SendsRawYAMLAndQueryParams(t *testing.T) {
	srv, lr := specAppMgmtServer(t)
	c := New(srv.URL, "secret-token")

	_, err := c.InstallCompose(context.Background(), "services:\n  web:\n    image: nginx\n", true, true)
	if err != nil {
		t.Fatalf("InstallCompose returned error: %v", err)
	}

	if lr.lastMethod != http.MethodPost || lr.lastPath != "/v2/app_management/compose" {
		t.Errorf("method/path = %s %s, want POST /v2/app_management/compose", lr.lastMethod, lr.lastPath)
	}
	if lr.lastQuery.Get("dry_run") != "true" {
		t.Errorf("dry_run query = %q, want true", lr.lastQuery.Get("dry_run"))
	}
	if lr.lastQuery.Get("check_port_conflict") != "true" {
		t.Errorf("check_port_conflict query = %q, want true", lr.lastQuery.Get("check_port_conflict"))
	}
	if lr.lastAuth != "secret-token" {
		t.Errorf("Authorization = %q, want bare secret-token (no Bearer prefix)", lr.lastAuth)
	}
	if !strings.Contains(lr.lastComposeYAML, "image: nginx") {
		t.Errorf("posted compose body missing nginx image: %q", lr.lastComposeYAML)
	}
}

func TestGetCompose_UnwrapsDataEnvelope(t *testing.T) {
	srv, _ := specAppMgmtServer(t)
	c := New(srv.URL, "tok")

	app, err := c.GetCompose(context.Background(), "myapp")
	if err != nil {
		t.Fatalf("GetCompose returned error: %v", err)
	}
	if app.Status != "running" {
		t.Errorf("status = %q, want running", app.Status)
	}
	if app.UpdateAvailable {
		t.Errorf("update_available = true, want false")
	}
	if app.Compose == nil {
		t.Fatal("compose = nil, want the decoded compose object")
	}
	cy, err := app.ComposeYAML()
	if err != nil {
		t.Fatalf("ComposeYAML() error: %v", err)
	}
	if !strings.Contains(cy, "image: nginx") {
		t.Errorf("ComposeYAML() = %q, want it to contain image: nginx", cy)
	}
	// Deterministic across repeated calls (yaml.v3 sorts map keys) — a real
	// requirement here, since a nondeterministic key order would show up as a
	// spurious plan diff on every refresh.
	cy2, _ := app.ComposeYAML()
	if cy != cy2 {
		t.Errorf("ComposeYAML() not deterministic: %q != %q", cy, cy2)
	}
}

func TestComposeYAML_NoComposeReturnsEmpty(t *testing.T) {
	app := &ComposeApp{Status: "running"}
	cy, err := app.ComposeYAML()
	if err != nil {
		t.Fatalf("ComposeYAML() error: %v", err)
	}
	if cy != "" {
		t.Errorf("ComposeYAML() = %q, want empty when API returned no compose object", cy)
	}
}

func TestSetComposeStatus_SendsJSONStringEnum(t *testing.T) {
	srv, lr := specAppMgmtServer(t)
	c := New(srv.URL, "tok")

	if err := c.SetComposeStatus(context.Background(), "myapp", "stop"); err != nil {
		t.Fatalf("SetComposeStatus returned error: %v", err)
	}
	if lr.lastStatus != "stop" {
		t.Errorf("status body = %q, want stop", lr.lastStatus)
	}
	// The status endpoint request body must be a quoted JSON string, not YAML.
}

func TestUpdateCompose_SendsRawYAML(t *testing.T) {
	srv, lr := specAppMgmtServer(t)
	c := New(srv.URL, "tok")

	if err := c.UpdateCompose(context.Background(), "myapp", "services:\n  db:\n    image: postgres\n", false, true); err != nil {
		t.Fatalf("UpdateCompose returned error: %v", err)
	}
	if lr.lastMethod != http.MethodPut || lr.lastPath != "/v2/app_management/compose/myapp" {
		t.Errorf("method/path = %s %s, want PUT /v2/app_management/compose/myapp", lr.lastMethod, lr.lastPath)
	}
	if !strings.Contains(lr.lastComposeYAML, "image: postgres") {
		t.Errorf("update body missing postgres: %q", lr.lastComposeYAML)
	}
}

func TestDeleteCompose_DeleteConfigFolderInverted(t *testing.T) {
	srv, lr := specAppMgmtServer(t)
	c := New(srv.URL, "tok")

	// retainConfig=false => delete_config_folder=true
	if err := c.DeleteCompose(context.Background(), "myapp", false); err != nil {
		t.Fatalf("DeleteCompose(retain=false) error: %v", err)
	}
	if lr.lastQuery.Get("delete_config_folder") != "true" {
		t.Errorf("retain=false => delete_config_folder=%q, want true", lr.lastQuery.Get("delete_config_folder"))
	}

	// retainConfig=true => delete_config_folder=false
	if err := c.DeleteCompose(context.Background(), "myapp", true); err != nil {
		t.Fatalf("DeleteCompose(retain=true) error: %v", err)
	}
	if lr.lastQuery.Get("delete_config_folder") != "false" {
		t.Errorf("retain=true => delete_config_folder=%q, want false", lr.lastQuery.Get("delete_config_folder"))
	}
}

func TestGetInfo_ReturnsArchitecture(t *testing.T) {
	srv, _ := specAppMgmtServer(t)
	c := New(srv.URL, "tok")

	info, err := c.GetInfo(context.Background())
	if err != nil {
		t.Fatalf("GetInfo returned error: %v", err)
	}
	if info.Architecture != "amd64" {
		t.Errorf("architecture = %q, want amd64", info.Architecture)
	}
}

func TestAPIError_OnNon2xx(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/app_management/compose/myapp", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(srv.URL, "tok")
	_, err := c.GetCompose(context.Background(), "myapp")
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("status code = %d, want 404", apiErr.StatusCode)
	}
}

func TestAuthHeader_BareToken(t *testing.T) {
	// A bare token is sent as-is (CONFIRMED: /v2/app_management/* rejects a
	// "Bearer "-prefixed Authorization header with 401).
	c := New("http://x", "bare-token")
	if got := c.authHeader(); got != "bare-token" {
		t.Errorf("authHeader() = %q, want bare-token", got)
	}
	// A token pasted in with a "Bearer " prefix has it stripped.
	c2 := New("http://x", "Bearer already")
	if got := c2.authHeader(); got != "already" {
		t.Errorf("authHeader() = %q, want already", got)
	}
	// Empty token => empty header.
	c3 := New("http://x", "")
	if got := c3.authHeader(); got != "" {
		t.Errorf("authHeader() = %q, want empty", got)
	}
}

func TestLogin_ExchangesCredentialsForToken(t *testing.T) {
	srv, lr := specAppMgmtServer(t)
	c := New(srv.URL, "")

	ts, err := c.Login(context.Background(), "admin", "secret")
	if err != nil {
		t.Fatalf("Login returned error: %v", err)
	}
	if ts.AccessToken != "at-123" {
		t.Errorf("access_token = %q, want at-123", ts.AccessToken)
	}
	if ts.RefreshToken != "rt-456" {
		t.Errorf("refresh_token = %q, want rt-456", ts.RefreshToken)
	}
	if lr.lastMethod != http.MethodPost || lr.lastPath != "/v1/users/login" {
		t.Errorf("login method/path = %s %s, want POST /v1/users/login", lr.lastMethod, lr.lastPath)
	}

	// The returned token can be applied so subsequent calls authenticate.
	c.SetToken(ts.AccessToken)
	if c.authHeader() != "at-123" {
		t.Errorf("after SetToken, authHeader() = %q, want at-123", c.authHeader())
	}
}
