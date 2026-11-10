// Package provider implements the SpinupWP Terraform provider.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/marcossantiago/terraform-provider-spinupwp/internal/client"
)

const (
	envAPIToken = "SPINUPWP_API_TOKEN"
	envAPIURL   = "SPINUPWP_API_URL"
)

var _ provider.Provider = (*spinupwpProvider)(nil)

type spinupwpProvider struct {
	version string
}

type providerModel struct {
	APIToken types.String `tfsdk:"api_token"`
	APIURL   types.String `tfsdk:"api_url"`
}

// New returns the provider factory.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &spinupwpProvider{version: version}
	}
}

func (p *spinupwpProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "spinupwp"
	resp.Version = p.version
}

func (p *spinupwpProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage [SpinupWP](https://spinupwp.com) servers, sites, HTTPS, basic auth and backups.",
		Attributes: map[string]schema.Attribute{
			"api_token": schema.StringAttribute{
				Description: "SpinupWP API token. Can also be set with the `" + envAPIToken + "` environment variable. " +
					"Creating and changing sites needs a token with write access.",
				Optional:  true,
				Sensitive: true,
			},
			"api_url": schema.StringAttribute{
				Description: "SpinupWP API base URL. Defaults to `" + client.DefaultBaseURL + "`. " +
					"Can also be set with the `" + envAPIURL + "` environment variable.",
				Optional: true,
			},
		},
	}
}

func (p *spinupwpProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if cfg.APIToken.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("api_token"), "Unknown SpinupWP API token",
			"The API token must be known when the provider is configured.")
		return
	}

	token := firstNonEmpty(cfg.APIToken.ValueString(), os.Getenv(envAPIToken))
	if token == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_token"), "Missing SpinupWP API token",
			"Set api_token in the provider block or the "+envAPIToken+" environment variable.")
		return
	}

	opts := []client.Option{client.WithUserAgent("terraform-provider-spinupwp/" + p.version)}
	if u := firstNonEmpty(cfg.APIURL.ValueString(), os.Getenv(envAPIURL)); u != "" {
		opts = append(opts, client.WithBaseURL(u))
	}
	c := client.New(token, opts...)
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *spinupwpProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewServerResource,
		NewSiteResource,
		NewSiteHTTPSResource,
	}
}

func (p *spinupwpProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{NewServerDataSource}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
