package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/example/terraform-provider-zimaos/internal/client"
)

var (
	_ resource.Resource                = (*appResource)(nil)
	_ resource.ResourceWithImportState = (*appResource)(nil)
)

type appResource struct {
	client *client.Client
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
	return &appResource{}
}

func (r *appResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app"
}

func (r *appResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *providerData, got %T", req.ProviderData))
		return
	}
	r.client = pd.client
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
				Default:     nil, // set below via planmodifier would be cleaner; kept simple
				Description: "start / stop / restart; drives PUT /compose/{id}/status.",
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

	plan.ID = types.StringValue(plan.Name.ValueString())
	if plan.DesiredState.IsNull() || plan.DesiredState.ValueString() == "" {
		plan.DesiredState = types.StringValue("start")
	}
	if err := r.client.SetComposeStatus(ctx, plan.Name.ValueString(), plan.DesiredState.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error setting app status", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
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
	state.Status = types.StringValue(app.Status)
	state.UpdateAvailable = types.BoolValue(app.UpdateAvailable)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *appResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan appResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.client.InstallCompose(ctx, plan.ComposeYAML.ValueString(), false, plan.CheckPortConflict.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Error updating app", err.Error())
		return
	}
	if !plan.DesiredState.IsNull() && plan.DesiredState.ValueString() != "" {
		if err := r.client.SetComposeStatus(ctx, plan.Name.ValueString(), plan.DesiredState.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error setting app status", err.Error())
			return
		}
	}
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
