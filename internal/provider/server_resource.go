package provider

import (
	"context"
	"regexp"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/marcossantiago/terraform-provider-spinupwp/internal/client"
)

// Provisioning a server takes about 10 minutes.
const defaultServerCreateTimeout = 45 * time.Minute

var (
	_ resource.ResourceWithConfigure   = (*serverResource)(nil)
	_ resource.ResourceWithImportState = (*serverResource)(nil)

	hostnamePattern = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
)

// NewServerResource returns the spinupwp_server resource.
func NewServerResource() resource.Resource { return &serverResource{} }

type serverResource struct {
	client *client.Client
}

type serverResourceModel struct {
	ID                     types.String   `tfsdk:"id"`
	Hostname               types.String   `tfsdk:"hostname"`
	IPAddress              types.String   `tfsdk:"ip_address"`
	ProviderName           types.String   `tfsdk:"provider_name"`
	UbuntuVersion          types.String   `tfsdk:"ubuntu_version"`
	Username               types.String   `tfsdk:"username"`
	SSHPort                types.Int64    `tfsdk:"ssh_port"`
	Timezone               types.String   `tfsdk:"timezone"`
	PostProvisionScript    types.String   `tfsdk:"post_provision_script"`
	DatabaseRootPasswordWO types.String   `tfsdk:"database_root_password_wo"`
	Name                   types.String   `tfsdk:"name"`
	Status                 types.String   `tfsdk:"status"`
	Timeouts               timeouts.Value `tfsdk:"timeouts"`
}

func (r *serverResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server"
}

// serverCreateOnly marks inputs that only matter when SpinupWP provisions the server.
const serverCreateOnlyNote = " Changing it replaces the server, which provisions it again."

func (r *serverResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := func() []planmodifier.String {
		return []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
			func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
				resp.RequiresReplace = replaceIfStateSet(req.StateValue)
			}, "Replaces the server if changed after creation.", "Replaces the server if changed after creation.")}
	}
	resp.Schema = schema.Schema{
		Description: "A custom server (from any hosting provider) provisioned by SpinupWP over SSH. SpinupWP's SSH key " +
			"(`GET /ssh-key`, shown under **Servers → Connect a custom server**) must already be in the `authorized_keys` " +
			"of `username`; SpinupWP removes it once provisioning is done. Creating the server waits until provisioning " +
			"finishes. Destroying it removes the server from SpinupWP only; the machine is left alone.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "Server ID.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"hostname": schema.StringAttribute{
				Description:   "Hostname SpinupWP sets on the server: letters, numbers, dashes and periods." + serverCreateOnlyNote,
				Required:      true,
				Validators:    []validator.String{stringvalidator.RegexMatches(hostnamePattern, "may only contain letters, numbers, dashes and periods")},
				PlanModifiers: replace(),
			},
			"ip_address": schema.StringAttribute{
				Description:   "Public IPv4 or IPv6 address SpinupWP connects to." + serverCreateOnlyNote,
				Required:      true,
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: replace(),
			},
			"provider_name": schema.StringAttribute{
				Description:   "Name of the hosting company, shown in SpinupWP, e.g. `Example Hosting`." + serverCreateOnlyNote,
				Required:      true,
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: replace(),
			},
			"ubuntu_version": schema.StringAttribute{
				Description:   "Ubuntu version installed on the server, one of the two latest LTS releases, e.g. `26.04`." + serverCreateOnlyNote,
				Required:      true,
				Validators:    []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^\d+\.\d+$`), "must look like 26.04")},
				PlanModifiers: replace(),
			},
			"username": schema.StringAttribute{
				Description:   "User SpinupWP connects as. Defaults to `root`." + serverCreateOnlyNote,
				Optional:      true,
				Computed:      true,
				Default:       stringdefault.StaticString("root"),
				PlanModifiers: replace(),
			},
			"ssh_port": schema.Int64Attribute{
				Description: "SSH port. Defaults to `22`." + serverCreateOnlyNote,
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(22),
				Validators:  []validator.Int64{int64validator.Between(1, 65535)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.Int64Request, resp *int64planmodifier.RequiresReplaceIfFuncResponse) {
						resp.RequiresReplace = replaceIfStateSet(req.StateValue)
					}, "Replaces the server if changed after creation.", "Replaces the server if changed after creation."),
				},
			},
			"timezone": schema.StringAttribute{
				Description:   "Server timezone, e.g. `America/Toronto`. Defaults to SpinupWP's (UTC)." + serverCreateOnlyNote,
				Optional:      true,
				PlanModifiers: replace(),
			},
			"post_provision_script": schema.StringAttribute{
				Description:   "Script SpinupWP runs as root once the server is provisioned." + serverCreateOnlyNote,
				Optional:      true,
				PlanModifiers: replace(),
			},
			"database_root_password_wo": schema.StringAttribute{
				Description: "MySQL root password. Write-only: never stored in state. SpinupWP generates one if unset. " +
					"Only used when the server is provisioned.",
				Optional:  true,
				Sensitive: true,
				WriteOnly: true,
			},
			"name": schema.StringAttribute{
				Description:   "Server name in SpinupWP.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				Description: "Server status reported by SpinupWP, e.g. `provisioned`.",
				Computed:    true,
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Delete: true}),
		},
	}
}

func (r *serverResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *serverResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, cfg serverResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...) // write-only values are only in the config
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, defaultServerCreateTimeout)
	if resp.Diagnostics.Append(diags...); diags.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	server, eventID, err := r.client.CreateCustomServer(ctx, client.CreateCustomServerRequest{
		ProviderName:         plan.ProviderName.ValueString(),
		UbuntuVersion:        plan.UbuntuVersion.ValueString(),
		IPAddress:            plan.IPAddress.ValueString(),
		Username:             plan.Username.ValueString(),
		Hostname:             plan.Hostname.ValueString(),
		SSHPort:              plan.SSHPort.ValueInt64(),
		Timezone:             plan.Timezone.ValueString(),
		PostProvisionScript:  plan.PostProvisionScript.ValueString(),
		DatabaseRootPassword: cfg.DatabaseRootPasswordWO.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating SpinupWP server", err.Error())
		return
	}

	// Save the ID first: if provisioning fails, Terraform marks the server as tainted instead of losing it.
	plan.ID = types.StringValue(strconv.FormatInt(server.ID, 10))
	applyServer(&plan, server)
	if err := r.client.WaitForEvent(ctx, eventID); err != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddError("Error provisioning SpinupWP server "+plan.Hostname.ValueString(), err.Error())
		return
	}

	r.refresh(ctx, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serverResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := parseServerID(state.ID.ValueString(), &resp.Diagnostics)
	if !ok {
		return
	}
	server, err := r.client.GetServer(ctx, id)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading SpinupWP server", err.Error())
		return
	}
	applyServer(&state, server)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update only stores new timeouts or a new write-only password: every other input replaces the server.
func (r *serverResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serverResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID, plan.Name, plan.Status = state.ID, state.Name, state.Status
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serverResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serverResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, defaultDeleteTimeout)
	if resp.Diagnostics.Append(diags...); diags.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	id, ok := parseServerID(state.ID.ValueString(), &resp.Diagnostics)
	if !ok {
		return
	}
	eventID, err := r.client.DeleteServer(ctx, id)
	if client.IsNotFound(err) {
		return
	}
	if err == nil {
		err = r.client.WaitForEvent(ctx, eventID)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting SpinupWP server", err.Error())
	}
}

// ImportState reads an existing server. The inputs SpinupWP doesn't return (username,
// ssh_port, timezone, post_provision_script) take their configured values without replacing it.
func (r *serverResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, ok := parseServerID(req.ID, &resp.Diagnostics)
	if !ok {
		return
	}
	server, err := r.client.GetServer(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Error importing SpinupWP server", err.Error())
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	for attr, value := range map[string]string{
		"hostname":       server.Name,
		"ip_address":     server.IPAddress,
		"provider_name":  server.ProviderName,
		"ubuntu_version": server.UbuntuVersion,
	} {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(attr), value)...)
	}
}

// refresh re-reads the server so computed attributes match SpinupWP.
func (r *serverResource) refresh(ctx context.Context, m *serverResourceModel, diags *diag.Diagnostics) {
	id, ok := parseServerID(m.ID.ValueString(), diags)
	if !ok {
		return
	}
	server, err := r.client.GetServer(ctx, id)
	if err != nil {
		diags.AddError("Error reading SpinupWP server", err.Error())
		return
	}
	applyServer(m, server)
}

// applyServer copies the attributes SpinupWP returns. The inputs keep their configured
// values: SpinupWP may report them differently (e.g. a shortened IPv6 address), and a
// difference there would plan to provision the server again.
func applyServer(m *serverResourceModel, s *client.Server) {
	m.Name = types.StringValue(s.Name)
	m.Status = types.StringValue(s.Status)
}

func parseServerID(id string, diags *diag.Diagnostics) (int64, bool) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n < 1 {
		diags.AddError("Invalid server ID", "Expected a numeric SpinupWP server ID, got "+strconv.Quote(id)+".")
		return 0, false
	}
	return n, true
}
