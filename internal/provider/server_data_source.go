package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/marcossantiago/terraform-provider-spinupwp/internal/client"
)

var (
	_ datasource.DataSourceWithConfigure        = (*serverDataSource)(nil)
	_ datasource.DataSourceWithConfigValidators = (*serverDataSource)(nil)
)

// NewServerDataSource returns the spinupwp_server data source.
func NewServerDataSource() datasource.DataSource { return &serverDataSource{} }

type serverDataSource struct {
	client *client.Client
}

type serverModel struct {
	ID             types.Int64  `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	ProviderName   types.String `tfsdk:"provider_name"`
	IPAddress      types.String `tfsdk:"ip_address"`
	UbuntuVersion  types.String `tfsdk:"ubuntu_version"`
	Status         types.String `tfsdk:"status"`
	DatabaseServer types.String `tfsdk:"database_server"`
	DatabaseHost   types.String `tfsdk:"database_host"`
}

func (d *serverDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server"
}

func (d *serverDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Looks up a SpinupWP server by `id` or `name`, e.g. to get the `server_id` for a site.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.Int64Attribute{Description: "Server ID.", Optional: true, Computed: true},
			"name":            schema.StringAttribute{Description: "Server name. Must be unique in the team.", Optional: true, Computed: true},
			"provider_name":   schema.StringAttribute{Description: "Hosting provider, e.g. `DigitalOcean`.", Computed: true},
			"ip_address":      schema.StringAttribute{Description: "Public IP address.", Computed: true},
			"ubuntu_version":  schema.StringAttribute{Description: "Ubuntu version, e.g. `26.04`.", Computed: true},
			"status":          schema.StringAttribute{Description: "Server status, e.g. `provisioned`.", Computed: true},
			"database_server": schema.StringAttribute{Description: "Database server, e.g. `mysql-8.4` or `external`.", Computed: true},
			"database_host":   schema.StringAttribute{Description: "Database host.", Computed: true},
		},
	}
}

func (d *serverDataSource) ConfigValidators(_ context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("name")),
	}
}

func (d *serverDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (d *serverDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg serverModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	server, err := d.find(ctx, cfg)
	if err != nil {
		resp.Diagnostics.AddError("Error looking up SpinupWP server", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, serverModel{
		ID:             types.Int64Value(server.ID),
		Name:           types.StringValue(server.Name),
		ProviderName:   types.StringValue(server.ProviderName),
		IPAddress:      types.StringValue(server.IPAddress),
		UbuntuVersion:  types.StringValue(server.UbuntuVersion),
		Status:         types.StringValue(server.Status),
		DatabaseServer: types.StringValue(server.Database.Server),
		DatabaseHost:   types.StringValue(server.Database.Host),
	})...)
}

func (d *serverDataSource) find(ctx context.Context, cfg serverModel) (*client.Server, error) {
	if !cfg.ID.IsNull() {
		return d.client.GetServer(ctx, cfg.ID.ValueInt64())
	}
	servers, err := d.client.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	var matches []client.Server
	for _, s := range servers {
		if s.Name == cfg.Name.ValueString() {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no server named %q", cfg.Name.ValueString())
	case 1:
		return &matches[0], nil
	default:
		return nil, fmt.Errorf("%d servers are named %q; use id instead", len(matches), cfg.Name.ValueString())
	}
}
