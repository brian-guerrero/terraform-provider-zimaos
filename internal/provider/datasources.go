package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/example/terraform-provider-zimaos/internal/client"
)

// providerData carries the configured client to resources/datasources in this package.
type providerData struct {
	client *client.Client
}

// --- zimaos_app data source (read-only lookup by name) ---

var _ datasource.DataSource = (*appDataSource)(nil)

type appDataSource struct {
	client *client.Client
}

type appDataSourceModel struct {
	Name            types.String `tfsdk:"name"`
	Status          types.String `tfsdk:"status"`
	UpdateAvailable types.Bool   `tfsdk:"update_available"`
}

func NewAppDataSource() datasource.DataSource { return &appDataSource{} }

func (d *appDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_app"
}

func (d *appDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *providerData, got %T", req.ProviderData))
		return
	}
	d.client = pd.client
}

func (d *appDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Read-only lookup of an installed ZimaOS compose app by name.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{Required: true, Description: "Compose project name."},
			"status": schema.StringAttribute{Computed: true, Description: "Live status."},
			"update_available": schema.BoolAttribute{Computed: true, Description: "Update available."},
		},
	}
}

func (d *appDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg appDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	app, err := d.client.GetCompose(ctx, cfg.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading app", err.Error())
		return
	}
	cfg.Status = types.StringValue(app.Status)
	cfg.UpdateAvailable = types.BoolValue(app.UpdateAvailable)
	resp.Diagnostics.Append(resp.State.Set(ctx, &cfg)...)
}

// --- zimaos_system_info data source (read-only device facts) ---

var _ datasource.DataSource = (*systemInfoDataSource)(nil)

type systemInfoDataSource struct {
	client *client.Client
}

type systemInfoDataSourceModel struct {
	Architecture types.String `tfsdk:"architecture"`
}

func NewSystemInfoDataSource() datasource.DataSource { return &systemInfoDataSource{} }

func (d *systemInfoDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_system_info"
}

func (d *systemInfoDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	pd, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *providerData, got %T", req.ProviderData))
		return
	}
	d.client = pd.client
}

func (d *systemInfoDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Read-only ZimaOS device facts (architecture, etc.) from /info.",
		Attributes: map[string]schema.Attribute{
			"architecture": schema.StringAttribute{Computed: true, Description: "Device architecture, e.g. amd64."},
		},
	}
}

func (d *systemInfoDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state systemInfoDataSourceModel
	info, err := d.client.GetInfo(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reading system info", err.Error())
		return
	}
	state.Architecture = types.StringValue(info.Architecture)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
