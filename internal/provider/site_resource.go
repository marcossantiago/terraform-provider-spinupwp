package provider

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/marcossantiago/terraform-provider-spinupwp/internal/client"
)

const (
	defaultCreateTimeout = 30 * time.Minute
	defaultUpdateTimeout = 15 * time.Minute
	defaultDeleteTimeout = 15 * time.Minute
)

var (
	_                            resource.ResourceWithConfigure      = (*siteResource)(nil)
	_                            resource.ResourceWithImportState    = (*siteResource)(nil)
	_                            resource.ResourceWithValidateConfig = (*siteResource)(nil)
	wordPressInstallationMethods                                     = []string{"wp", "wp_subdirectory", "wp_subdomain"}
	installationMethods                                              = append([]string{"blank"}, wordPressInstallationMethods...)
	phpVersionPattern                                                = regexp.MustCompile(`^\d+\.\d+$`)
)

// NewSiteResource returns the spinupwp_site resource.
func NewSiteResource() resource.Resource { return &siteResource{} }

type siteResource struct {
	client *client.Client
}

type siteModel struct {
	ID                 types.String        `tfsdk:"id"`
	ServerID           types.Int64         `tfsdk:"server_id"`
	Domain             types.String        `tfsdk:"domain"`
	SiteUser           types.String        `tfsdk:"site_user"`
	InstallationMethod types.String        `tfsdk:"installation_method"`
	PHPVersion         types.String        `tfsdk:"php_version"`
	PublicFolder       types.String        `tfsdk:"public_folder"`
	PageCacheEnabled   types.Bool          `tfsdk:"page_cache_enabled"`
	CronInterval       types.Int64         `tfsdk:"cron_interval"`
	DNSProviderID      types.Int64         `tfsdk:"dns_provider_id"`
	Database           *siteDatabaseModel  `tfsdk:"database"`
	WordPress          *siteWordPressModel `tfsdk:"wordpress"`
	BasicAuth          *siteBasicAuthModel `tfsdk:"basic_auth"`
	Backups            *siteBackupsModel   `tfsdk:"backups"`
	DeleteDatabase     types.Bool          `tfsdk:"delete_database"`
	DeleteBackups      types.Bool          `tfsdk:"delete_backups"`
	Status             types.String        `tfsdk:"status"`
	IsWordPress        types.Bool          `tfsdk:"is_wordpress"`
	HTTPSEnabled       types.Bool          `tfsdk:"https_enabled"`
	Timeouts           timeouts.Value      `tfsdk:"timeouts"`
}

type siteDatabaseModel struct {
	Name        types.String `tfsdk:"name"`
	Username    types.String `tfsdk:"username"`
	PasswordWO  types.String `tfsdk:"password_wo"`
	TablePrefix types.String `tfsdk:"table_prefix"`
}

type siteWordPressModel struct {
	Title           types.String `tfsdk:"title"`
	AdminUser       types.String `tfsdk:"admin_user"`
	AdminEmail      types.String `tfsdk:"admin_email"`
	AdminPasswordWO types.String `tfsdk:"admin_password_wo"`
}

func (r *siteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site"
}

// createOnly marks inputs SpinupWP only accepts at creation and never returns. A change
// replaces the site, unless there is no earlier value in state (e.g. right after import),
// so importing a site never plans to destroy it.
const createOnlyNote = " Only used when the site is created; changing it replaces the site."

func replaceIfStateSet[T interface{ IsNull() bool }](state T) bool { return !state.IsNull() }

func (r *siteResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A SpinupWP site, with its basic auth and backups. Creating a site waits until SpinupWP has finished " +
			"deploying it. HTTPS is managed with `spinupwp_site_https`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "Site ID.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"server_id": schema.Int64Attribute{
				Description:   "ID of the SpinupWP server to create the site on. Changing it replaces the site.",
				Required:      true,
				Validators:    []validator.Int64{int64validator.AtLeast(1)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"domain": schema.StringAttribute{
				Description:   "Primary domain. Changing it replaces the site.",
				Required:      true,
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"site_user": schema.StringAttribute{
				Description:   "Linux user that owns the site's files and runs its PHP. Changing it replaces the site.",
				Required:      true,
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"installation_method": schema.StringAttribute{
				Description: "One of `blank`, `wp`, `wp_subdirectory` or `wp_subdomain`." + createOnlyNote,
				Required:    true,
				Validators:  []validator.String{stringvalidator.OneOf(installationMethods...)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
						resp.RequiresReplace = replaceIfStateSet(req.StateValue)
					}, "Replaces the site if changed after creation.", "Replaces the site if changed after creation."),
				},
			},
			"php_version": schema.StringAttribute{
				Description:   "PHP version, e.g. `8.3`. Defaults to SpinupWP's default. Can be changed in place.",
				Optional:      true,
				Computed:      true,
				Validators:    []validator.String{stringvalidator.RegexMatches(phpVersionPattern, "must look like 8.3")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"public_folder": schema.StringAttribute{
				Description: "Folder Nginx serves, relative to the site's files directory, e.g. `/public` for Laravel. " +
					"Unset keeps SpinupWP's default (`/`). Changing it replaces the site.",
				Optional:   true,
				Computed:   true,
				Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^/`), "must start with /")},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					samePublicFolder{},
					stringplanmodifier.RequiresReplace(),
				},
			},
			"page_cache_enabled": schema.BoolAttribute{
				Description:   "Whether the Nginx page cache is enabled. Can be changed in place.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"cron_interval": schema.Int64Attribute{
				Description: "Minutes between runs of the server-level WordPress cron: `1`, `2`, `5`, `10`, `15`, `30` or `60`, " +
					"or `0` to turn it off. Unset keeps SpinupWP's default. Can be changed in place.",
				Optional:      true,
				Computed:      true,
				Validators:    []validator.Int64{int64validator.OneOf(0, 1, 2, 5, 10, 15, 30, 60)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"dns_provider_id": schema.Int64Attribute{
				Description: "ID of a team DNS provider. When set, SpinupWP points the domain's A record at the server " +
					"and enables HTTPS with a DNS-validated Let's Encrypt certificate." + createOnlyNote,
				Optional:   true,
				Validators: []validator.Int64{int64validator.AtLeast(1)},
				PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.Int64Request, resp *int64planmodifier.RequiresReplaceIfFuncResponse) {
						resp.RequiresReplace = replaceIfStateSet(req.StateValue)
					}, "Replaces the site if changed after creation.", "Replaces the site if changed after creation."),
				},
			},
			"database": schema.SingleNestedAttribute{
				Description: "Database to create with the site, on the server's database host." + createOnlyNote,
				Optional:    true,
				PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.ObjectRequest, resp *objectplanmodifier.RequiresReplaceIfFuncResponse) {
						resp.RequiresReplace = replaceIfStateSet(req.StateValue)
					}, "Replaces the site if changed after creation.", "Replaces the site if changed after creation."),
				},
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Description: "Database name.",
						Required:    true,
					},
					"username": schema.StringAttribute{
						Description: "Database user.",
						Required:    true,
					},
					"password_wo": schema.StringAttribute{
						// No validators: SpinupWP checks the password, and stringvalidator's errors would
						// print it.
						Description: "Database password, checked by SpinupWP. Write-only: never stored in state. " +
							"SpinupWP generates one if unset.",
						Optional:  true,
						Sensitive: true,
						WriteOnly: true,
					},
					"table_prefix": schema.StringAttribute{
						Description: "WordPress table prefix. SpinupWP generates one if unset.",
						Optional:    true,
					},
				},
			},
			"wordpress": schema.SingleNestedAttribute{
				Description: "WordPress install settings. Required when `installation_method` is a WordPress method." + createOnlyNote,
				Optional:    true,
				PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.ObjectRequest, resp *objectplanmodifier.RequiresReplaceIfFuncResponse) {
						resp.RequiresReplace = replaceIfStateSet(req.StateValue)
					}, "Replaces the site if changed after creation.", "Replaces the site if changed after creation."),
				},
				Attributes: map[string]schema.Attribute{
					"title": schema.StringAttribute{
						Description: "Site title.",
						Required:    true,
					},
					"admin_user": schema.StringAttribute{
						Description: "Admin username.",
						Required:    true,
					},
					"admin_email": schema.StringAttribute{
						Description: "Admin email.",
						Required:    true,
					},
					"admin_password_wo": schema.StringAttribute{
						Description: "Admin password, at least 8 characters. Write-only: never stored in state.",
						Required:    true,
						Sensitive:   true,
						WriteOnly:   true,
						Validators:  []validator.String{stringvalidator.LengthAtLeast(8)},
					},
				},
			},
			"basic_auth": basicAuthAttribute(),
			"backups":    backupsAttribute(),
			"delete_database": schema.BoolAttribute{
				Description: "Delete the site's database when the site is destroyed. Defaults to `false`.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"delete_backups": schema.BoolAttribute{
				Description: "Delete the site's backups when the site is destroyed. Defaults to `false`.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"status": schema.StringAttribute{
				Description:   "Site status reported by SpinupWP, e.g. `deployed`.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"is_wordpress": schema.BoolAttribute{
				Description:   "Whether SpinupWP detects WordPress on the site.",
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"https_enabled": schema.BoolAttribute{
				Description:   "Whether HTTPS is enabled, as of the last refresh. Manage HTTPS with `spinupwp_site_https`.",
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"timeouts": timeouts.Attributes(ctx, timeouts.Opts{Create: true, Update: true, Delete: true}),
		},
	}
}

func (r *siteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = clientFromProviderData(req.ProviderData, &resp.Diagnostics)
}

func (r *siteResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// Read attributes individually: whole blocks may still be unknown at validation time.
	var method types.String
	var wordpress, database types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("installation_method"), &method)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("wordpress"), &wordpress)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("database"), &database)...)
	if resp.Diagnostics.HasError() || !known(method) || wordpress.IsUnknown() || database.IsUnknown() {
		return
	}
	isWordPress := false
	for _, m := range wordPressInstallationMethods {
		isWordPress = isWordPress || method.ValueString() == m
	}
	switch {
	case isWordPress && wordpress.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("wordpress"), "Missing wordpress settings",
			"installation_method "+method.ValueString()+" needs a wordpress block.")
	case isWordPress && database.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("database"), "Missing database",
			"A WordPress site needs a database block.")
	case !isWordPress && !wordpress.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("wordpress"), "Unexpected wordpress settings",
			"wordpress is only used with a WordPress installation_method.")
	}
	validateBackupsHaveSchedule(ctx, req, resp)
}

// validateBackupsHaveSchedule rejects a backups block without daily, weekly or monthly:
// without a schedule SpinupWP runs no backups, so the block would never match.
func validateBackupsHaveSchedule(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var backups types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("backups"), &backups)...)
	if !known(backups) {
		return
	}
	for _, name := range []string{"daily", "weekly", "monthly"} {
		var schedule types.Object
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("backups").AtName(name), &schedule)...)
		if !schedule.IsNull() {
			return
		}
	}
	resp.Diagnostics.AddAttributeError(path.Root("backups"), "Missing backup schedule",
		"backups needs at least one of daily, weekly or monthly. Remove the block to turn backups off.")
}

func (r *siteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, cfg siteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...) // write-only values are only in the config
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, defaultCreateTimeout)
	if resp.Diagnostics.Append(diags...); diags.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	site, eventID, err := r.client.CreateSite(ctx, createRequest(plan, cfg))
	if err != nil {
		resp.Diagnostics.AddError("Error creating SpinupWP site", err.Error())
		return
	}

	// Save the ID first: if deploying fails, Terraform marks the site as tainted instead of losing it.
	plan.ID = types.StringValue(strconv.FormatInt(site.ID, 10))
	applyComputed(&plan, site)
	err = r.client.WaitForEvent(ctx, eventID)
	if err == nil {
		_, err = r.client.WaitForSiteDeployed(ctx, site.ID)
	}
	if err != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddError("Error deploying SpinupWP site "+plan.Domain.ValueString(), err.Error())
		return
	}
	if err := r.syncFeatures(ctx, site.ID, plan, siteModel{}, cfg, true); err != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		resp.Diagnostics.AddError("Error configuring SpinupWP site "+plan.Domain.ValueString(), err.Error())
		return
	}

	r.refresh(ctx, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// syncFeatures applies cron and the basic_auth and backups blocks.
func (r *siteResource) syncFeatures(ctx context.Context, id int64, plan, state, cfg siteModel, backupsChanged bool) error {
	if err := r.syncBasicAuth(ctx, id, plan.BasicAuth, state.BasicAuth, cfg.BasicAuth); err != nil {
		return fmt.Errorf("basic auth: %w", err)
	}
	site, err := r.client.GetSite(ctx, id)
	if err != nil {
		return err
	}
	if err := r.syncCron(ctx, site, plan.CronInterval); err != nil {
		return fmt.Errorf("cron: %w", err)
	}
	if plan.Backups == nil && state.Backups == nil {
		return nil
	}
	if err := r.syncBackups(ctx, site, plan.Backups, backupsChanged); err != nil {
		return fmt.Errorf("backups: %w", err)
	}
	return nil
}

func (r *siteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state siteModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, ok := parseID(state.ID.ValueString(), &resp.Diagnostics)
	if !ok {
		return
	}
	site, err := r.client.GetSite(ctx, id)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading SpinupWP site", err.Error())
		return
	}
	applySite(ctx, &state, site, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *siteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, cfg siteModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Update(ctx, defaultUpdateTimeout)
	if resp.Diagnostics.Append(diags...); diags.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	id, ok := parseID(state.ID.ValueString(), &resp.Diagnostics)
	if !ok {
		return
	}
	if known(plan.PHPVersion) && !plan.PHPVersion.Equal(state.PHPVersion) {
		eventID, err := r.client.UpdatePHPVersion(ctx, id, plan.PHPVersion.ValueString())
		if err == nil {
			err = r.client.WaitForEvent(ctx, eventID)
		}
		if err != nil {
			resp.Diagnostics.AddError("Error updating PHP version", err.Error())
			return
		}
	}
	if known(plan.PageCacheEnabled) && !plan.PageCacheEnabled.Equal(state.PageCacheEnabled) {
		eventID, err := r.client.SetPageCache(ctx, id, plan.PageCacheEnabled.ValueBool())
		if err == nil {
			err = r.client.WaitForEvent(ctx, eventID)
		}
		if err != nil {
			resp.Diagnostics.AddError("Error updating page cache", err.Error())
			return
		}
	}
	var planBackups, stateBackups types.Object
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("backups"), &planBackups)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("backups"), &stateBackups)...)
	if err := r.syncFeatures(ctx, id, plan, state, cfg, !planBackups.Equal(stateBackups)); err != nil {
		resp.Diagnostics.AddError("Error updating SpinupWP site", err.Error())
		return
	}

	plan.ID = state.ID
	r.refresh(ctx, &plan, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *siteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state siteModel
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

	id, ok := parseID(state.ID.ValueString(), &resp.Diagnostics)
	if !ok {
		return
	}
	eventID, err := r.client.DeleteSite(ctx, id, client.DeleteSiteOptions{
		DeleteDatabase: state.DeleteDatabase.ValueBool(),
		DeleteBackups:  state.DeleteBackups.ValueBool(),
	})
	if client.IsNotFound(err) {
		return
	}
	if err == nil {
		err = r.client.WaitForEvent(ctx, eventID)
	}
	if err != nil {
		resp.Diagnostics.AddError("Error deleting SpinupWP site", err.Error())
	}
}

func (r *siteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if _, ok := parseID(req.ID, &resp.Diagnostics); !ok {
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("delete_database"), false)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("delete_backups"), false)...)
}

// refresh re-reads the site so computed attributes match SpinupWP after a change.
func (r *siteResource) refresh(ctx context.Context, m *siteModel, diags *diag.Diagnostics) {
	id, ok := parseID(m.ID.ValueString(), diags)
	if !ok {
		return
	}
	site, err := r.client.GetSite(ctx, id)
	if err != nil {
		diags.AddError("Error reading SpinupWP site", err.Error())
		return
	}
	applySite(ctx, m, site, diags)
}

// syncCron makes the site's cron match the planned interval. Unset leaves it alone.
func (r *siteResource) syncCron(ctx context.Context, site *client.Site, planned types.Int64) error {
	if !known(planned) || planned.ValueInt64() == site.Cron.Minutes() {
		return nil
	}
	var (
		eventID int64
		err     error
	)
	switch minutes := planned.ValueInt64(); {
	case minutes == 0:
		eventID, err = r.client.DisableCron(ctx, site.ID)
	case site.Cron.Minutes() == 0:
		eventID, err = r.client.EnableCron(ctx, site.ID, minutes)
	default:
		eventID, err = r.client.UpdateCron(ctx, site.ID, minutes)
	}
	if err != nil {
		return err
	}
	return r.client.WaitForEvent(ctx, eventID)
}

// applySite copies the attributes SpinupWP returns. Create-only inputs keep their configured values.
func applySite(ctx context.Context, m *siteModel, s *client.Site, diags *diag.Diagnostics) {
	m.BasicAuth = basicAuthFromSite(s, m.BasicAuth)
	m.Backups = backupsFromSite(ctx, s, m.Backups, diags)
	m.CronInterval = types.Int64Value(s.Cron.Minutes())
	m.ServerID = types.Int64Value(s.ServerID)
	m.Domain = types.StringValue(s.Domain)
	m.SiteUser = types.StringValue(s.SiteUser)
	m.PHPVersion = types.StringValue(s.PHPVersion)
	m.PublicFolder = publicFolder(m.PublicFolder, s.PublicFolder)
	m.PageCacheEnabled = types.BoolValue(s.PageCache.Enabled)
	m.Status = types.StringValue(s.Status)
	m.IsWordPress = types.BoolValue(s.IsWordPress)
	m.HTTPSEnabled = types.BoolValue(s.HTTPS.Enabled)
}

// publicFolder returns SpinupWP's public folder, keeping the configured spelling when it only
// differs by a trailing slash: SpinupWP stores "/public" as "/public/".
func publicFolder(current types.String, reported string) types.String {
	if known(current) && strings.TrimSuffix(current.ValueString(), "/") == strings.TrimSuffix(reported, "/") {
		return current
	}
	return types.StringValue(reported)
}

// samePublicFolder plans no change when the configured public folder only differs from the
// state by a trailing slash, so that difference never replaces the site.
type samePublicFolder struct{}

func (samePublicFolder) Description(context.Context) string {
	return "Ignores a trailing slash difference from the state."
}

func (m samePublicFolder) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

func (samePublicFolder) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if known(req.PlanValue) && known(req.StateValue) &&
		strings.TrimSuffix(req.PlanValue.ValueString(), "/") == strings.TrimSuffix(req.StateValue.ValueString(), "/") {
		resp.PlanValue = req.StateValue
	}
}

// applyComputed fills the attributes still unknown from the create response, so a site
// that fails to deploy can be saved to state. Planned values stay, to be applied next.
func applyComputed(m *siteModel, s *client.Site) {
	if m.CronInterval.IsUnknown() {
		m.CronInterval = types.Int64Value(s.Cron.Minutes())
	}
	if m.PHPVersion.IsUnknown() {
		m.PHPVersion = types.StringValue(s.PHPVersion)
	}
	m.PublicFolder = publicFolder(m.PublicFolder, s.PublicFolder)
	if m.PageCacheEnabled.IsUnknown() {
		m.PageCacheEnabled = types.BoolValue(s.PageCache.Enabled)
	}
	m.Status = types.StringValue(s.Status)
	m.IsWordPress = types.BoolValue(s.IsWordPress)
	m.HTTPSEnabled = types.BoolValue(s.HTTPS.Enabled)
}

func createRequest(plan, cfg siteModel) client.CreateSiteRequest {
	r := client.CreateSiteRequest{
		ServerID:           plan.ServerID.ValueInt64(),
		Domain:             plan.Domain.ValueString(),
		SiteUser:           plan.SiteUser.ValueString(),
		InstallationMethod: plan.InstallationMethod.ValueString(),
		PHPVersion:         plan.PHPVersion.ValueString(),
		PublicFolder:       plan.PublicFolder.ValueString(),
		PageCacheEnabled:   knownPointer(plan.PageCacheEnabled, plan.PageCacheEnabled.ValueBool),
		DNSProviderID:      knownPointer(plan.DNSProviderID, plan.DNSProviderID.ValueInt64),
	}
	if db := plan.Database; db != nil {
		r.Database = &client.CreateSiteDatabase{
			Name:        db.Name.ValueString(),
			Username:    db.Username.ValueString(),
			TablePrefix: db.TablePrefix.ValueString(),
		}
		if cfg.Database != nil {
			r.Database.Password = cfg.Database.PasswordWO.ValueString()
		}
	}
	if wp := plan.WordPress; wp != nil {
		r.WordPress = &client.CreateSiteWordPress{
			Title:      wp.Title.ValueString(),
			AdminUser:  wp.AdminUser.ValueString(),
			AdminEmail: wp.AdminEmail.ValueString(),
		}
		if cfg.WordPress != nil {
			r.WordPress.AdminPassword = cfg.WordPress.AdminPasswordWO.ValueString()
		}
	}
	return r
}

type maybeValue interface {
	IsNull() bool
	IsUnknown() bool
}

func known(v maybeValue) bool { return !v.IsNull() && !v.IsUnknown() }

// knownPointer returns nil for a null or unknown value, so it is left out of the request.
func knownPointer[T any](v maybeValue, value func() T) *T {
	if !known(v) {
		return nil
	}
	x := value()
	return &x
}

func parseID(id string, diags *diag.Diagnostics) (int64, bool) {
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n < 1 {
		diags.AddError("Invalid site ID", fmt.Sprintf("Expected a numeric SpinupWP site ID, got %q.", id))
		return 0, false
	}
	return n, true
}
