package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/brian-guerrero/terraform-provider-zimaos/internal/client"
)

// appMock emulates the confirmed App Management V2 contract so the resource
// lifecycle can be exercised in-process (no CLI, no symlinks — runs on Windows).
func appMock(t *testing.T) *client.Client {
	t.Helper()
	apps := map[string]string{}
	mux := http.NewServeMux()
	ok := func(w http.ResponseWriter) { w.Header().Set("Content-Type", "application/json") }

	mux.HandleFunc("/v2/app_management/compose", func(w http.ResponseWriter, r *http.Request) {
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
			_, _ = w.Write([]byte(`{"data":{"status":"running","update_available":false,"compose":{"name":"` + id + `","services":{"web":{"image":"nginx:latest"}}}}}`))
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
		}
	})
	mux.HandleFunc("/v2/app_management/info", func(w http.ResponseWriter, r *http.Request) {
		ok(w)
		_, _ = w.Write([]byte(`{"architecture":"amd64"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return client.New(srv.URL, "test-token")
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

func newAppResourceWithClient(c *client.Client) *appResource {
	r := &appResource{}
	var pd ProviderData = ProviderData{Client: c}
	resp := &resource.ConfigureResponse{}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: &pd}, resp)
	return r
}

func appSchema(t *testing.T) resource.SchemaResponse {
	r := NewAppResource()
	resp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema error: %v", resp.Diagnostics)
	}
	return *resp
}

// buildState constructs a tfsdk.State from a model struct using the resource schema.
func buildState(t *testing.T, sch resource.SchemaResponse, model any) tfsdk.State {
	st := &tfsdk.State{Schema: sch.Schema}
	diags := st.Set(context.Background(), model)
	if diags.HasError() {
		t.Fatalf("state set error: %v", diags)
	}
	return *st
}

// buildPlan constructs a tfsdk.Plan from a model struct using the resource schema.
// tfsdk.Plan has no Set(); we build the Raw value via a State then reuse it.
func buildPlan(t *testing.T, sch resource.SchemaResponse, model any) tfsdk.Plan {
	st := &tfsdk.State{Schema: sch.Schema}
	diags := st.Set(context.Background(), model)
	if diags.HasError() {
		t.Fatalf("plan set error: %v", diags)
	}
	return tfsdk.Plan{Schema: sch.Schema, Raw: st.Raw}
}

// buildConfig constructs a tfsdk.Config from a model struct using the resource schema.
// tfsdk.Config has no Set(); we build the Raw value via a State then reuse it.
func buildConfig(t *testing.T, sch resource.SchemaResponse, model any) tfsdk.Config {
	st := &tfsdk.State{Schema: sch.Schema}
	diags := st.Set(context.Background(), model)
	if diags.HasError() {
		t.Fatalf("config set error: %v", diags)
	}
	return tfsdk.Config{Schema: sch.Schema, Raw: st.Raw}
}

func TestAppResource_lifecycle(t *testing.T) {
	c := appMock(t)
	r := newAppResourceWithClient(c)
	sch := appSchema(t)
	ctx := context.Background()

	compose := "name: hello\nservices:\n  web:\n    image: nginx:latest\n    ports:\n      - \"8080:80\"\n"

	model := appResourceModel{
		Name:          types.StringValue("hello"),
		ComposeYAML:   types.StringValue(compose),
		DesiredState:  types.StringValue("start"),
		DryRunOnPlan:  types.BoolValue(true),
		CheckPortConflict: types.BoolValue(true),
		RetainConfigOnDestroy: types.BoolValue(false),
	}

	// CREATE
	createResp := &resource.CreateResponse{State: buildState(t, sch, model)}
	r.Create(ctx, resource.CreateRequest{Config: buildConfig(t, sch, model), Plan: buildPlan(t, sch, model)}, createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("Create error: %v", createResp.Diagnostics)
	}
	var created appResourceModel
	if diags := createResp.State.Get(ctx, &created); diags.HasError() {
		t.Fatalf("created state get: %v", diags)
	}
	// NOTE: id is a Computed attribute populated by Read (the project name is the
	// stable identifier). It is asserted after Read, not here.
	if created.Status.ValueString() != "running" {
		t.Errorf("status = %q, want running", created.Status.ValueString())
	}

	// READ
	readResp := &resource.ReadResponse{State: buildState(t, sch, created)}
	r.Read(ctx, resource.ReadRequest{State: buildState(t, sch, created)}, readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read error: %v", readResp.Diagnostics)
	}
	var read appResourceModel
	if diags := readResp.State.Get(ctx, &read); diags.HasError() {
		t.Fatalf("read state get: %v", diags)
	}
	if read.Status.ValueString() != "running" {
		t.Errorf("read status = %q, want running", read.Status.ValueString())
	}
	if read.ID.ValueString() != "hello" {
		t.Errorf("read id = %q, want hello", read.ID.ValueString())
	}

	// UPDATE
	updatedModel := read
	updatedModel.ComposeYAML = types.StringValue("name: hello\nservices:\n  web:\n    image: nginx:1.27\n    ports:\n      - \"9090:80\"\n")
	updateResp := &resource.UpdateResponse{State: buildState(t, sch, updatedModel)}
	r.Update(ctx, resource.UpdateRequest{
		Config: buildConfig(t, sch, updatedModel),
		Plan:   buildPlan(t, sch, updatedModel),
		State:  buildState(t, sch, read),
	}, updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("Update error: %v", updateResp.Diagnostics)
	}
	var updated appResourceModel
	if diags := updateResp.State.Get(ctx, &updated); diags.HasError() {
		t.Fatalf("updated state get: %v", diags)
	}
	if !strings.Contains(updated.ComposeYAML.ValueString(), "nginx:1.27") {
		t.Errorf("updated compose missing nginx:1.27: %q", updated.ComposeYAML.ValueString())
	}

	// DELETE
	deleteResp := &resource.DeleteResponse{State: buildState(t, sch, updated)}
	r.Delete(ctx, resource.DeleteRequest{State: buildState(t, sch, updated)}, deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("Delete error: %v", deleteResp.Diagnostics)
	}
}

func TestAppResource_import(t *testing.T) {
	c := appMock(t)
	r := newAppResourceWithClient(c)
	sch := appSchema(t)
	ctx := context.Background()

	// Pre-install an app so the import has something to read.
	createResp := &resource.CreateResponse{
		State: buildState(t, sch, appResourceModel{
			Name:                types.StringValue("hello"),
			ComposeYAML:         types.StringValue("name: hello\nservices:\n  web:\n    image: nginx:latest\n"),
			DesiredState:        types.StringValue("start"),
			RetainConfigOnDestroy: types.BoolValue(false),
		}),
	}
	r.Create(ctx, resource.CreateRequest{
		Config: buildConfig(t, sch, appResourceModel{
			Name:                types.StringValue("hello"),
			ComposeYAML:         types.StringValue("name: hello\nservices:\n  web:\n    image: nginx:latest\n"),
			DesiredState:        types.StringValue("start"),
			RetainConfigOnDestroy: types.BoolValue(false),
		}),
		Plan: buildPlan(t, sch, appResourceModel{
			Name:                types.StringValue("hello"),
			ComposeYAML:         types.StringValue("name: hello\nservices:\n  web:\n    image: nginx:latest\n"),
			DesiredState:        types.StringValue("start"),
			RetainConfigOnDestroy: types.BoolValue(false),
		}),
	}, createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("Create error: %v", createResp.Diagnostics)
	}

	// IMPORT (sets name via passthrough; framework then runs Read to populate the rest)
	importResp := &resource.ImportStateResponse{State: buildState(t, sch, appResourceModel{})}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "hello"}, importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("ImportState error: %v", importResp.Diagnostics)
	}
	var imported appResourceModel
	if diags := importResp.State.Get(ctx, &imported); diags.HasError() {
		t.Fatalf("imported state get: %v", diags)
	}
	if imported.Name.ValueString() != "hello" {
		t.Errorf("imported name = %q, want hello", imported.Name.ValueString())
	}

	// REALISTIC IMPORT: Terraform calls Read after ImportState to fill computed attrs.
	readResp := &resource.ReadResponse{State: importResp.State}
	r.Read(ctx, resource.ReadRequest{State: importResp.State}, readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("post-import Read error: %v", readResp.Diagnostics)
	}
	var importedFull appResourceModel
	if diags := readResp.State.Get(ctx, &importedFull); diags.HasError() {
		t.Fatalf("imported full state get: %v", diags)
	}
	if importedFull.Status.ValueString() != "running" {
		t.Errorf("imported status = %q, want running", importedFull.Status.ValueString())
	}
	if !importedFull.UpdateAvailable.IsNull() && importedFull.UpdateAvailable.ValueBool() {
		t.Errorf("imported update_available should be false")
	}
	// compose_yaml is Required (not Computed) and ImportStatePassthroughID
	// only sets name, so without Read() re-deriving it from the API's own
	// compose object, this would still be null after import — which fails
	// `tofu plan -generate-config-out` outright (missing-required-attribute
	// error, not just an empty value).
	if importedFull.ComposeYAML.IsNull() || !strings.Contains(importedFull.ComposeYAML.ValueString(), "nginx:latest") {
		t.Errorf("imported compose_yaml = %q, want it derived from the API's compose object", importedFull.ComposeYAML.ValueString())
	}
}
