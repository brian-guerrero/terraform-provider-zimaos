package terraformproviderzimaos

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/brian-guerrero/terraform-provider-zimaos/internal/client"
	providerinternal "github.com/brian-guerrero/terraform-provider-zimaos/internal/provider"
)

// Ensure the provider satisfies the framework interfaces.
var _ provider.Provider = (*zimaOSProvider)(nil)

type zimaOSProvider struct{}

func New() func() provider.Provider {
	return func() provider.Provider { return &zimaOSProvider{} }
}

func (p *zimaOSProvider) Metadata(_ context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "zimaos"
}

func (p *zimaOSProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage a ZimaOS device via its OpenAPI (App Management, Local Storage, Users, System).",
		Attributes: map[string]schema.Attribute{
			"host": schema.StringAttribute{
				Required:    true,
				Description: "Base URL of the ZimaOS device API, e.g. http://192.168.1.50:8080",
			},
			"token": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "API access token sent as a bare 'Authorization' header (no 'Bearer ' prefix — confirmed against a live device, see internal/client/client.go). Optional: if omitted, username+password are exchanged for a token via POST /login (tokens are short-lived, so this is the preferred way to avoid stale tokens).",
			},
			"username": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "ZimaOS username. Used with password to obtain a token via POST /login when token is not set.",
			},
			"password": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "ZimaOS password. Used with username to obtain a token via POST /login when token is not set.",
			},
		},
	}
}

func (p *zimaOSProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerSchemaModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	c := client.New(cfg.Host.ValueString(), cfg.Token.ValueString())

	// If no token was supplied, exchange username+password for one (ZimaOS
	// POST /login -> { data: { token: { access_token, refresh_token } } }).
	// Tokens are short-lived, so this is the preferred path over a pasted token.
	if cfg.Token.IsNull() && !cfg.Username.IsNull() && !cfg.Password.IsNull() {
		ts, err := c.Login(ctx, cfg.Username.ValueString(), cfg.Password.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("ZimaOS login failed", err.Error())
			return
		}
		c.SetToken(ts.AccessToken)
	}

	resp.ResourceData = &providerinternal.ProviderData{Client: c}
	resp.DataSourceData = &providerinternal.ProviderData{Client: c}
}

func (p *zimaOSProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		providerinternal.NewAppResource,
	}
}

func (p *zimaOSProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		providerinternal.NewAppDataSource,
		providerinternal.NewSystemInfoDataSource,
	}
}

type providerSchemaModel struct {
	Host     types.String `tfsdk:"host"`
	Token    types.String `tfsdk:"token"`
	Username types.String `tfsdk:"username"`
	Password types.String `tfsdk:"password"`
}
