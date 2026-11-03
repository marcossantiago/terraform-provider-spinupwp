package provider

import (
	"context"
	"strconv"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/marcossantiago/terraform-provider-spinupwp/internal/client"
)

// siteFeature is the shared base of the resources that manage one feature of a site
// (HTTPS, basic auth, backups). Their ID is the site ID, and destroying them turns the
// feature off without touching the site itself.
type siteFeature struct {
	client *client.Client
}

func (f *siteFeature) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	f.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (f *siteFeature) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	siteID, ok := parseID(req.ID, &resp.Diagnostics)
	if !ok {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("site_id"), siteID)...)
}

// site reads the site. It returns nil without an error when the site no longer exists.
func (f *siteFeature) site(ctx context.Context, siteID types.Int64, diags *diag.Diagnostics) *client.Site {
	site, err := f.client.GetSite(ctx, siteID.ValueInt64())
	if client.IsNotFound(err) {
		return nil
	}
	if err != nil {
		diags.AddError("Error reading SpinupWP site", err.Error())
	}
	return site
}

// run starts an operation and waits for its event, within timeout.
func (f *siteFeature) run(ctx context.Context, timeout time.Duration, summary string, diags *diag.Diagnostics, start func(context.Context) (int64, error)) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	eventID, err := start(ctx)
	if err == nil {
		err = f.client.WaitForEvent(ctx, eventID)
	}
	if err != nil {
		diags.AddError(summary, err.Error())
		return false
	}
	return true
}

func siteIDAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Description:   "The site ID.",
			Computed:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"site_id": schema.Int64Attribute{
			Description:   "ID of the site. Changing it moves the setting to another site.",
			Required:      true,
			Validators:    []validator.Int64{int64validator.AtLeast(1)},
			PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
		},
	}
}

func idFor(siteID types.Int64) types.String {
	return types.StringValue(strconv.FormatInt(siteID.ValueInt64(), 10))
}

func timeoutsAttribute(ctx context.Context) schema.Attribute {
	return timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true})
}

// writeOnlyVersionNote explains the usual pattern for rotating a write-only value.
const writeOnlyVersionNote = " Change it to send a new value of the matching write-only argument; " +
	"Terraform can't detect changes to write-only values on its own."
