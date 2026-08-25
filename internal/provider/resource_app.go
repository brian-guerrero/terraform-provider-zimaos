package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/brian-guerrero/terraform-provider-zimaos/internal/client"
)

var (
	_ resource.Resource                = (*appResource)(nil)
	_ resource.ResourceWithImportState = (*appResource)(nil)
)

// Default polling parameters for waitForAppRegistered. POST .../compose
// (non-dry-run) returns as soon as the API *accepts* the request — CONFIRMED
// against a live ZimaOS device (2026-08-24): the actual `docker compose pull
// && up` runs in the background and a large image pull (observed: several
// minutes for kestra/kestra:latest) can take a while, during which the app
// isn't registered yet and GetCompose 404s "app not found". 3s keeps the
// common case (small/cached images) snappy; 5m gives large pulls real headroom.
const (
	defaultAppPollInterval = 3 * time.Second
	defaultAppPollTimeout  = 5 * time.Minute
)

type appResource struct {
	client *client.Client

	// pollInterval/pollTimeout drive waitForAppRegistered. Left zero-valued in
	// normal construction (NewAppResource fills in the real defaults above);
	// tests can shrink them via a lower-visibility constructor seam so async
	// registration can be simulated without slowing the suite down.
	pollInterval time.Duration
	pollTimeout  time.Duration
}

type appResourceModel struct {
	ID                    types.String `tfsdk:"id"`
	Name                  types.String `tfsdk:"name"`
	ComposeYAML           types.String `tfsdk:"compose_yaml"`
	DryRunOnPlan          types.Bool   `tfsdk:"dry_run_on_plan"`
	CheckPortConflict     types.Bool   `tfsdk:"check_port_conflict"`
	RetainConfigOnDestroy types.Bool   `tfsdk:"retain_config_on_destroy"`
	DesiredState          types.String `tfsdk:"desired_state"`
	Status                types.String `tfsdk:"status"`
	UpdateAvailable       types.Bool   `tfsdk:"update_available"`
}

func NewAppResource() resource.Resource {
	return &appResource{
		pollInterval: defaultAppPollInterval,
		pollTimeout:  defaultAppPollTimeout,
	}
}

func (r *appResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app"
}

func (r *appResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *ProviderData, got %T", req.ProviderData))
		return
	}
	r.client = pd.Client
}

func (r *appResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Docker Compose app on a ZimaOS device (App Management V2, spec-confirmed).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				Description: "Compose project name (stable ID).",
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Compose project name; becomes the ID.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"compose_yaml": schema.StringAttribute{
				Required:    true,
				Description: "Raw Compose specification content.",
			},
			"dry_run_on_plan": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Call API dry-run during plan for early validation (confirmed param).",
			},
			"check_port_conflict": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
				Description: "Passthrough to API check_port_conflict (confirmed param, default true).",
			},
			"retain_config_on_destroy": schema.BoolAttribute{
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				Description: "Keep config folder on destroy. Deliberately defaults false (inverse of API's true default) to avoid silent data loss.",
			},
			"desired_state": schema.StringAttribute{
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("start"),
				Description: "start / stop / restart; drives PUT /compose/{id}/status. Defaults to start.",
				Validators:  []validator.String{oneOfStatusValidator{}},
			},
			"status": schema.StringAttribute{
				Computed:    true,
				Description: "Live status from API.",
			},
			"update_available": schema.BoolAttribute{
				Computed:    true,
				Description: "Whether an AppStore update is available.",
			},
		},
	}
}

func (r *appResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan appResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Dry-run validation during plan-time-equivalent here (confirmed params).
	if plan.DryRunOnPlan.ValueBool() {
		if _, err := r.client.InstallCompose(ctx, plan.ComposeYAML.ValueString(), true, plan.CheckPortConflict.ValueBool()); err != nil {
			resp.Diagnostics.AddError("Dry-run validation failed", err.Error())
			return
		}
	}

	if _, err := r.client.InstallCompose(ctx, plan.ComposeYAML.ValueString(), false, plan.CheckPortConflict.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Error installing app", err.Error())
		return
	}

	// The install call above only confirms the API *accepted* the request —
	// the app isn't necessarily registered yet (see waitForAppRegistered doc
	// comment). Wait for it before touching status/reading it back, otherwise
	// SetComposeStatus/GetCompose race the background install and 404.
	if _, err := r.waitForAppRegistered(ctx, plan.Name.ValueString()); err != nil {
		// InstallCompose already succeeded, so something is (or will be)
		// running on the device even though we couldn't confirm it in time.
		// Persist what we safely can — id plus the plan's own values — so
		// this isn't an untracked orphan the user has to `tofu import` back
		// in; a later plan/apply/refresh can reconcile it. Status/
		// update_available are left null (unknown values are not valid in
		// state) since we don't actually know them yet.
		plan.ID = types.StringValue(plan.Name.ValueString())
		plan.Status = types.StringNull()
		plan.UpdateAvailable = types.BoolNull()
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddError(
			"Timed out confirming app installation",
			fmt.Sprintf(
				"The install request for app %q was accepted by the API, but the provider could not confirm "+
					"it was fully registered before giving up: %s\n\n"+
					"The app may still be installing in the background (large image pulls can take several "+
					"minutes). Re-run `plan`/`apply` shortly, or `refresh`, to pick up its state once installation "+
					"finishes. If the underlying error above looks persistent (auth, network, etc.), address that first.",
				plan.Name.ValueString(), err,
			),
		)
		return
	}

	if plan.DesiredState.IsNull() || plan.DesiredState.ValueString() == "" {
		plan.DesiredState = types.StringValue("start")
	}
	if err := r.client.SetComposeStatus(ctx, plan.Name.ValueString(), plan.DesiredState.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error setting app status", err.Error())
		return
	}

	// Read back live status so computed fields are populated immediately.
	app, err := r.client.GetCompose(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading app after create", err.Error())
		return
	}
	// The compose project name is the stable identifier and the required id.
	plan.ID = types.StringValue(plan.Name.ValueString())
	plan.Status = types.StringValue(app.Status)
	plan.UpdateAvailable = types.BoolValue(app.UpdateAvailable)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// waitForAppRegistered polls GetCompose until the app shows up (fast path:
// one call, no delay, if it's already registered), a fatal error occurs, or
// pollTimeout elapses.
//
// A 404 (*client.APIError with StatusCode 404) means "the install hasn't
// registered the app yet" and is retried. Any other error (auth failure,
// 5xx, network error, ctx cancellation, ...) is treated as fatal and
// returned immediately — polling blindly on a real error for the full
// timeout would just be bad UX.
func (r *appResource) waitForAppRegistered(ctx context.Context, name string) (*client.ComposeApp, error) {
	interval := r.pollInterval
	if interval <= 0 {
		interval = defaultAppPollInterval
	}
	timeout := r.pollTimeout
	if timeout <= 0 {
		timeout = defaultAppPollTimeout
	}

	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	for {
		app, err := r.client.GetCompose(ctx, name)
		if err == nil {
			return app, nil
		}

		var apiErr *client.APIError
		if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
			return nil, err
		}
		lastErr = err

		timer := time.NewTimer(interval)
		select {
		case <-pollCtx.Done():
			timer.Stop()
			if !errors.Is(pollCtx.Err(), context.DeadlineExceeded) {
				// pollCtx.Done() fired for a reason other than our own
				// timeout elapsing — i.e. the caller's ctx was cancelled.
				return nil, fmt.Errorf("context cancelled while waiting for app %q to be registered: %w", name, ctx.Err())
			}
			return nil, fmt.Errorf("timed out after %s waiting for app %q to be registered: %w", timeout, name, lastErr)
		case <-timer.C:
		}
	}
}

func (r *appResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state appResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	app, err := r.client.GetCompose(ctx, state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading app", err.Error())
		return
	}
	// The compose project name is the stable identifier. Set it as id so import
	// (which only supplies name via passthrough) yields a complete state.
	if state.Name.ValueString() != "" {
		state.ID = types.StringValue(state.Name.ValueString())
	}
	state.Status = types.StringValue(app.Status)
	state.UpdateAvailable = types.BoolValue(app.UpdateAvailable)
	// Populate compose_yaml from the API's own compose object where available
	// (needed so import — and `tofu plan -generate-config-out` — produce a
	// usable, non-null value for this Required attribute). This is the API's
	// normalized view, not necessarily byte-identical to what a user hand-authored,
	// so expect a one-time diff on the next plan after adopting an existing app
	// this way — see ComposeYAML() doc comment.
	if cy, err := app.ComposeYAML(); err != nil {
		resp.Diagnostics.AddWarning("Could not re-derive compose_yaml from API", err.Error())
	} else if cy != "" {
		state.ComposeYAML = types.StringValue(cy)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *appResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan appResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.UpdateCompose(ctx, plan.Name.ValueString(), plan.ComposeYAML.ValueString(), false, plan.CheckPortConflict.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Error updating app", err.Error())
		return
	}
	if !plan.DesiredState.IsNull() && plan.DesiredState.ValueString() != "" {
		if err := r.client.SetComposeStatus(ctx, plan.Name.ValueString(), plan.DesiredState.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error setting app status", err.Error())
			return
		}
	}
	app, err := r.client.GetCompose(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading app after update", err.Error())
		return
	}
	// Keep the identifier stable across update (name is RequiresReplace, so it
	// cannot change here, but the framework requires id in the apply result).
	plan.ID = types.StringValue(plan.Name.ValueString())
	plan.Status = types.StringValue(app.Status)
	plan.UpdateAvailable = types.BoolValue(app.UpdateAvailable)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *appResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state appResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteCompose(ctx, state.Name.ValueString(), state.RetainConfigOnDestroy.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Error deleting app", err.Error())
	}
}

func (r *appResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
