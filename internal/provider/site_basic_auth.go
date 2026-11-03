package provider

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/marcossantiago/terraform-provider-spinupwp/internal/client"
)

// siteBasicAuthModel is the basic_auth block of spinupwp_site.
type siteBasicAuthModel struct {
	Username          types.String `tfsdk:"username"`
	PasswordWO        types.String `tfsdk:"password_wo"`
	PasswordWOVersion types.Int64  `tfsdk:"password_wo_version"`
}

func basicAuthAttribute() schema.Attribute {
	return schema.SingleNestedAttribute{
		Description: "HTTP basic authentication, e.g. to keep a staging site private. Removing the block turns it off.",
		Optional:    true,
		Attributes: map[string]schema.Attribute{
			"username": schema.StringAttribute{
				Description: "Username: at least 3 letters, digits, dashes or underscores.",
				Required:    true,
				Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[A-Za-z0-9_-]{3,}$`),
					"must be at least 3 letters, digits, dashes or underscores")},
			},
			"password_wo": schema.StringAttribute{
				Description: "Password, at least 3 characters. Write-only: never stored in state.",
				Required:    true,
				Sensitive:   true,
				WriteOnly:   true,
				Validators:  []validator.String{stringvalidator.LengthAtLeast(3)},
			},
			"password_wo_version": schema.Int64Attribute{
				Description: "Version of `password_wo`." + writeOnlyVersionNote,
				Optional:    true,
			},
		},
	}
}

// syncBasicAuth makes SpinupWP match the planned basic_auth block. cfg holds the write-only password.
func (r *siteResource) syncBasicAuth(ctx context.Context, siteID int64, plan, state, cfg *siteBasicAuthModel) error {
	var (
		eventID int64
		err     error
	)
	switch {
	case plan == nil && state == nil:
		return nil
	case plan == nil:
		eventID, err = r.client.DisableBasicAuth(ctx, siteID)
	case state == nil:
		eventID, err = r.client.EnableBasicAuth(ctx, siteID, plan.Username.ValueString(), cfg.PasswordWO.ValueString())
	default:
		password := "" // empty keeps the current password
		if !plan.PasswordWOVersion.Equal(state.PasswordWOVersion) {
			password = cfg.PasswordWO.ValueString()
		}
		if plan.Username.Equal(state.Username) && password == "" {
			return nil
		}
		eventID, err = r.client.UpdateBasicAuth(ctx, siteID, plan.Username.ValueString(), password)
	}
	if err != nil {
		return err
	}
	return r.client.WaitForEvent(ctx, eventID)
}

// basicAuthFromSite returns the block as SpinupWP reports it. SpinupWP never returns the
// password, so the version is kept from prior state.
func basicAuthFromSite(s *client.Site, prior *siteBasicAuthModel) *siteBasicAuthModel {
	if !s.BasicAuth.Enabled {
		return nil
	}
	m := &siteBasicAuthModel{Username: types.StringValue(s.BasicAuth.Username), PasswordWOVersion: types.Int64Null()}
	if prior != nil {
		m.PasswordWOVersion = prior.PasswordWOVersion
	}
	return m
}
