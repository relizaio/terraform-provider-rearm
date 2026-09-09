package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rearm "github.com/relizaio/rearm-client-go"
)

// Ensure the provider satisfies the framework interfaces.
var _ provider.Provider = &rearmProvider{}

type rearmProvider struct {
	version string
}

// providerModel maps provider configuration to Go values.
type providerModel struct {
	URI      types.String `tfsdk:"uri"`
	APIKeyID types.String `tfsdk:"api_key_id"`
	APIKey   types.String `tfsdk:"api_key"`
}

// New returns a provider factory.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &rearmProvider{version: version} }
}

func (p *rearmProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "rearm"
	resp.Version = p.version
}

func (p *rearmProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage ReARM configuration declaratively. Authenticates with a ReARM API key pair (FREEFORM keys with write access on the organization).",
		Attributes: map[string]schema.Attribute{
			"uri": schema.StringAttribute{
				Description: "Base URL of the ReARM instance, e.g. https://app.rearmhq.com. Falls back to REARM_URI.",
				Optional:    true,
			},
			"api_key_id": schema.StringAttribute{
				Description: "API key id. Falls back to REARM_APIKEYID.",
				Optional:    true,
			},
			"api_key": schema.StringAttribute{
				Description: "API key secret. Falls back to REARM_APIKEY.",
				Optional:    true,
				Sensitive:   true,
			},
		},
	}
}

func (p *rearmProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	uri := firstNonEmpty(cfg.URI.ValueString(), os.Getenv("REARM_URI"))
	keyID := firstNonEmpty(cfg.APIKeyID.ValueString(), os.Getenv("REARM_APIKEYID"))
	key := firstNonEmpty(cfg.APIKey.ValueString(), os.Getenv("REARM_APIKEY"))
	if uri == "" || keyID == "" || key == "" {
		resp.Diagnostics.AddError("Missing ReARM credentials",
			"Set uri, api_key_id and api_key in the provider block or REARM_URI, REARM_APIKEYID and REARM_APIKEY in the environment.")
		return
	}
	client, err := rearm.New(uri, keyID, key, rearm.WithUserAgent("terraform-provider-rearm/"+p.version))
	if err != nil {
		resp.Diagnostics.AddError("Cannot create ReARM client", err.Error())
		return
	}
	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *rearmProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewComponentResource,
		NewBranchResource,
	}
}

func (p *rearmProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
