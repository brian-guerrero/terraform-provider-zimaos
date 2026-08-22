package terraformproviderzimaos

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/example/terraform-provider-zimaos/internal/client"
	providerinternal "github.com/example/terraform-provider-zimaos/internal/provider"
)

// Ensure the provider satisfies the framework interfaces.
var _ provider.Provider = (*zimaOSProvider)(nil)

type zimaOSProvider struct{}

type providerData struct {
	client *client.Client
}

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
				Required:    true,
				Sensitive:   true,
				Description: "API token sent as the Authorization header (confirmed auth scheme).",
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
	resp.ResourceData = &providerData{client: c}
	resp.DataSourceData = &providerData{client: c}
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
	Host  types.String `tfsdk:"host"`
	Token types.String `tfsdk:"token"`
}
