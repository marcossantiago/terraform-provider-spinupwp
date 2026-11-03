package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/marcossantiago/terraform-provider-spinupwp/internal/client"
)

var (
	_ resource.ResourceWithConfigure      = (*siteHTTPSResource)(nil)
	_ resource.ResourceWithImportState    = (*siteHTTPSResource)(nil)
	_ resource.ResourceWithValidateConfig = (*siteHTTPSResource)(nil)
)

// NewSiteHTTPSResource returns the spinupwp_site_https resource.
func NewSiteHTTPSResource() resource.Resource { return &siteHTTPSResource{} }

type siteHTTPSResource struct{ siteFeature }

type siteHTTPSModel struct {
	ID                  types.String   `tfsdk:"id"`
	SiteID              types.Int64    `tfsdk:"site_id"`
	Type                types.String   `tfsdk:"type"`
	DNSProviderID       types.Int64    `tfsdk:"dns_provider_id"`
	Certificate         types.String   `tfsdk:"certificate"`
	PrivateKeyWO        types.String   `tfsdk:"private_key_wo"`
	PrivateKeyWOVersion types.Int64    `tfsdk:"private_key_wo_version"`
	CertificateExpires  types.String   `tfsdk:"certificate_expires"`
	CertificateRenews   types.String   `tfsdk:"certificate_renews"`
	Timeouts            timeouts.Value `tfsdk:"timeouts"`
}

func (r *siteHTTPSResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_site_https"
}

func (r *siteHTTPSResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := siteIDAttributes()
	attrs["type"] = schema.StringAttribute{
		Description: "Certificate type: `webroot` (Let's Encrypt, needs the domain to point at the server), " +
			"`dns` (Let's Encrypt validated through a DNS provider) or `custom`.",
		Required:   true,
		Validators: []validator.String{stringvalidator.OneOf("webroot", "dns", "custom")},
	}
	attrs["dns_provider_id"] = schema.Int64Attribute{
		Description: "ID of a team DNS provider. Required when `type` is `dns`.",
		Optional:    true,
		Validators:  []validator.Int64{int64validator.AtLeast(1)},
	}
	attrs["certificate"] = schema.StringAttribute{
		Description: "PEM certificate (with any intermediates). Required when `type` is `custom`.",
		Optional:    true,
	}
	attrs["private_key_wo"] = schema.StringAttribute{
		Description: "PEM private key. Required when `type` is `custom`. Write-only: never stored in state.",
		Optional:    true,
		Sensitive:   true,
		WriteOnly:   true,
	}
	attrs["private_key_wo_version"] = schema.Int64Attribute{
		Description: "Version of `private_key_wo`." + writeOnlyVersionNote,
		Optional:    true,
	}
	attrs["certificate_expires"] = schema.StringAttribute{
		Description:   "When the certificate expires.",
		Computed:      true,
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	attrs["certificate_renews"] = schema.StringAttribute{
		Description:   "When SpinupWP renews the certificate.",
		Computed:      true,
		PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
	attrs["timeouts"] = timeoutsAttribute(ctx)
	resp.Schema = schema.Schema{
		Description: "HTTPS for a SpinupWP site. Destroying it disables HTTPS. " +
			"SpinupWP doesn't return the certificate type, so after an import the first apply only records " +
			"`type` and the certificate settings; it doesn't reissue the certificate.",
		Attributes: attrs,
	}
}

func (r *siteHTTPSResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg siteHTTPSModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || !known(cfg.Type) {
		return
	}
	t := cfg.Type.ValueString()
	require := func(set bool, attr, msg string) {
		if !set {
			resp.Diagnostics.AddAttributeError(path.Root(attr), "Invalid HTTPS configuration", msg)
		}
	}
	require(t == "dns" || cfg.DNSProviderID.IsNull(), "dns_provider_id", "dns_provider_id is only used with type = \"dns\".")
	require(t != "dns" || !cfg.DNSProviderID.IsNull(), "dns_provider_id", "type = \"dns\" needs dns_provider_id.")
	require(t == "custom" || (cfg.Certificate.IsNull() && cfg.PrivateKeyWO.IsNull()), "certificate",
		"certificate and private_key_wo are only used with type = \"custom\".")
	require(t != "custom" || (!cfg.Certificate.IsNull() && !cfg.PrivateKeyWO.IsNull()), "certificate",
		"type = \"custom\" needs certificate and private_key_wo.")
}

func (r *siteHTTPSResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, cfg siteHTTPSModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, defaultUpdateTimeout)
	if resp.Diagnostics.Append(diags...); diags.HasError() {
		return
	}
	if !r.run(ctx, timeout, "Error enabling HTTPS", &resp.Diagnostics, func(ctx context.Context) (int64, error) {
		return r.client.EnableHTTPS(ctx, plan.SiteID.ValueInt64(), httpsRequest(plan, cfg))
	}) {
		return
	}
	plan.ID = idFor(plan.SiteID)
	r.refresh(ctx, &plan, resp)
}

func (r *siteHTTPSResource) refresh(ctx context.Context, m *siteHTTPSModel, resp *resource.CreateResponse) {
	site := r.site(ctx, m.SiteID, &resp.Diagnostics)
	if site == nil {
		resp.Diagnostics.AddError("Error reading SpinupWP site", "The site no longer exists.")
		return
	}
	applyHTTPS(m, site)
	resp.Diagnostics.Append(resp.State.Set(ctx, m)...)
}

func (r *siteHTTPSResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state siteHTTPSModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	site := r.site(ctx, state.SiteID, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if site == nil || !site.HTTPS.Enabled {
		resp.State.RemoveResource(ctx) // HTTPS is off: the next apply turns it back on
		return
	}
	applyHTTPS(&state, site)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *siteHTTPSResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, cfg siteHTTPSModel
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
	// SpinupWP doesn't return the certificate type, so after an import the state has none:
	// record the configured settings without reissuing the certificate.
	imported := state.Type.IsNull()
	changed := !plan.Type.Equal(state.Type) || !plan.DNSProviderID.Equal(state.DNSProviderID) ||
		!plan.Certificate.Equal(state.Certificate) || !plan.PrivateKeyWOVersion.Equal(state.PrivateKeyWOVersion)
	if changed && !imported {
		if !r.run(ctx, timeout, "Error updating HTTPS", &resp.Diagnostics, func(ctx context.Context) (int64, error) {
			return r.client.UpdateHTTPS(ctx, plan.SiteID.ValueInt64(), httpsRequest(plan, cfg))
		}) {
			return
		}
	}
	site := r.site(ctx, plan.SiteID, &resp.Diagnostics)
	if site != nil {
		applyHTTPS(&plan, site)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *siteHTTPSResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state siteHTTPSModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := state.Timeouts.Delete(ctx, defaultDeleteTimeout)
	if resp.Diagnostics.Append(diags...); diags.HasError() {
		return
	}
	r.run(ctx, timeout, "Error disabling HTTPS", &resp.Diagnostics, func(ctx context.Context) (int64, error) {
		id, err := r.client.DisableHTTPS(ctx, state.SiteID.ValueInt64())
		if client.IsNotFound(err) {
			return 0, nil // site already gone
		}
		return id, err
	})
}

func httpsRequest(plan, cfg siteHTTPSModel) client.HTTPSRequest {
	return client.HTTPSRequest{
		Type:          plan.Type.ValueString(),
		DNSProviderID: knownPointer(plan.DNSProviderID, plan.DNSProviderID.ValueInt64),
		Certificate:   plan.Certificate.ValueString(),
		PrivateKey:    cfg.PrivateKeyWO.ValueString(), // write-only: only in the config
	}
}

func applyHTTPS(m *siteHTTPSModel, s *client.Site) {
	m.CertificateExpires = types.StringValue(s.HTTPS.CertificateExpires)
	m.CertificateRenews = types.StringValue(s.HTTPS.CertificateRenews)
}
