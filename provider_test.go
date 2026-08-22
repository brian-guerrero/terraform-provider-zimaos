package terraformproviderzimaos

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

var testProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"zimaos": providerserver.NewProtocol6WithError(New()()),
}

// specMock emulates the confirmed App Management V2 contract in-process so the
// acceptance suite can run without a live ZimaOS device.
func specMock(t *testing.T) *httptest.Server {
	t.Helper()
	apps := map[string]string{}
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter) { w.Header().Set("Content-Type", "application/json") }

	mux.HandleFunc("/v2/app_management/compose", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/yaml" {
			t.Errorf("POST /v2/app_management/compose content-type = %q, want application/yaml", ct)
		}
		b, _ := io.ReadAll(r.Body)
		apps[composeName(string(b))] = string(b)
		ok(w)
		_, _ = w.Write([]byte(`{}`))
	})

	mux.HandleFunc("/v2/app_management/compose/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v2/app_management/compose/"), "/status")
		switch r.Method {
		case http.MethodGet:
			if _, ok2 := apps[id]; !ok2 {
				http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
				return
			}
			ok(w)
			_, _ = w.Write([]byte(`{"data":{"status":"running","update_available":false}}`))
		case http.MethodPut:
			if strings.HasSuffix(r.URL.Path, "/status") {
				ok(w)
				_, _ = w.Write([]byte(`{}`))
				return
			}
			b, _ := io.ReadAll(r.Body)
			apps[id] = string(b)
			ok(w)
			_, _ = w.Write([]byte(`{}`))
		case http.MethodDelete:
			delete(apps, id)
			ok(w)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/v2/app_management/info", func(w http.ResponseWriter, r *http.Request) {
		ok(w)
		_, _ = w.Write([]byte(`{"architecture":"amd64"}`))
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func composeName(yaml string) string {
	for _, line := range strings.Split(yaml, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "name:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		}
	}
	return "app"
}

func testAccProviderConfig(host, token string) string {
	return fmt.Sprintf(`
provider "zimaos" {
  host  = %q
  token = %q
}
`, host, token)
}

const testAccAppConfig = `
resource "zimaos_app" "test" {
  name = "hello"
  compose_yaml = <<-EOT
    name: hello
    services:
      web:
        image: nginx:latest
        ports:
          - "8080:80"
    EOT
  desired_state = "start"
}
`

const testAccAppConfigUpdated = `
resource "zimaos_app" "test" {
  name = "hello"
  compose_yaml = <<-EOT
    name: hello
    services:
      web:
        image: nginx:1.27
        ports:
          - "9090:80"
    EOT
  desired_state = "start"
}
`

const testAccSystemInfoConfig = `
data "zimaos_system_info" "dev" {}
`

func TestAccApp_basic(t *testing.T) {
	// The terraform-plugin-testing harness symlinks the module into a temp work
	// dir, which Windows blocks without Developer Mode. Skip here (before the
	// harness spins up) so `go test ./...` stays green on Windows; run on
	// Linux/CI or with Developer Mode enabled. Logic is also covered by the
	// white-box lifecycle test in internal/provider.
	if runtime.GOOS == "windows" {
		t.Skip("acceptance harness needs symlink support; skip on Windows — see internal/provider white-box test")
	}
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance tests skipped unless TF_ACC=1 is set")
	}

	host, token := accEndpoints(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig(host, token) + testAccAppConfig,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("zimaos_app.test", tfjsonpath.New("name"), knownvalue.StringExact("hello")),
					statecheck.ExpectKnownValue("zimaos_app.test", tfjsonpath.New("id"), knownvalue.StringExact("hello")),
					statecheck.ExpectKnownValue("zimaos_app.test", tfjsonpath.New("desired_state"), knownvalue.StringExact("start")),
					statecheck.ExpectKnownValue("zimaos_app.test", tfjsonpath.New("status"), knownvalue.StringExact("running")),
					statecheck.ExpectKnownValue("zimaos_app.test", tfjsonpath.New("dry_run_on_plan"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue("zimaos_app.test", tfjsonpath.New("retain_config_on_destroy"), knownvalue.Bool(false)),
				},
			},
			{
				ResourceName:      "zimaos_app.test",
				ImportState:       true,
				ImportStateVerify: true,
				// The App Management V2 GET /compose/{id} response returns only
				// status + update_available (id/name come from the identifier).
				// compose_yaml, check_port_conflict, dry_run_on_plan,
				// retain_config_on_destroy and desired_state are create-time
				// params the API does not echo back, so they are null after
				// import and must be re-declared in config. Ignore them here.
				ImportStateVerifyIgnore: []string{
					"compose_yaml",
					"check_port_conflict",
					"dry_run_on_plan",
					"retain_config_on_destroy",
					"desired_state",
				},
				ImportStateId: "hello",
			},
			{
				Config: testAccProviderConfig(host, token) + testAccAppConfigUpdated,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("zimaos_app.test", tfjsonpath.New("status"), knownvalue.StringExact("running")),
				},
			},
			{
				ResourceName:      "zimaos_app.test",
				ImportState:       true,
				ImportStateVerify: true,
				// The App Management V2 GET /compose/{id} response returns only
				// status + update_available (id/name come from the identifier).
				// compose_yaml, check_port_conflict, dry_run_on_plan,
				// retain_config_on_destroy and desired_state are create-time
				// params the API does not echo back, so they are null after
				// import and must be re-declared in config. Ignore them here.
				ImportStateVerifyIgnore: []string{
					"compose_yaml",
					"check_port_conflict",
					"dry_run_on_plan",
					"retain_config_on_destroy",
					"desired_state",
				},
				ImportStateId: "hello",
			},
		},
	})
}

func TestAccSystemInfo_basic(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("acceptance harness needs symlink support; skip on Windows")
	}
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance tests skipped unless TF_ACC=1 is set")
	}
	host, token := accEndpoints(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccProviderConfig(host, token) + testAccSystemInfoConfig,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.zimaos_system_info.dev", tfjsonpath.New("architecture"), knownvalue.StringExact("amd64")),
				},
			},
		},
	})
}

func accEndpoints(t *testing.T) (host, token string) {
	if liveHost := os.Getenv("ZIMAOS_HOST"); liveHost != "" {
		return liveHost, os.Getenv("ZIMAOS_TOKEN")
	}
	return specMock(t).URL, "test-token"
}

func testAccPreCheck(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Fatal("TF_ACC must be set for acceptance tests")
	}
}

var _ = context.Background
