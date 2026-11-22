package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/marcossantiago/terraform-provider-spinupwp/internal/client"
)

// siteDomainModel is one entry of spinupwp_site's additional_domains, keyed by the domain.
type siteDomainModel struct {
	Redirect *siteDomainRedirectModel `tfsdk:"redirect"`
}

type siteDomainRedirectModel struct {
	Type        types.Int64  `tfsdk:"type"`
	Destination types.String `tfsdk:"destination"`
}

func additionalDomainsAttribute() schema.Attribute {
	return schema.MapNestedAttribute{
		Description: "The site's additional domains, keyed by domain. Each one is served by the site, or redirected. " +
			"When set, the site has exactly these: domains added elsewhere are removed, and `{}` removes them all. " +
			"When unset, Terraform leaves the site's additional domains alone. HTTPS isn't managed here: the site's " +
			"certificate must cover the domains too (see `spinupwp_site_https`).",
		Optional: true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"redirect": schema.SingleNestedAttribute{
					Description: "Redirect the domain's requests instead of serving the site.",
					Optional:    true,
					Attributes: map[string]schema.Attribute{
						"type": schema.Int64Attribute{
							Description: "HTTP status of the redirect: 301, 302, 307 or 308. Defaults to 301.",
							Optional:    true,
							Computed:    true,
							Default:     int64default.StaticInt64(301),
							Validators:  []validator.Int64{int64validator.OneOf(301, 302, 307, 308)},
						},
						"destination": schema.StringAttribute{
							Description: "Where to redirect to, a URL or a domain. SpinupWP defaults it to the site's primary domain.",
							Optional:    true,
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

// syncDomains makes the site's additional domains match the planned map. Unset leaves them alone.
func (r *siteResource) syncDomains(ctx context.Context, site *client.Site, plan map[string]siteDomainModel) error {
	if plan == nil {
		return nil
	}
	current := map[string]client.SiteDomain{}
	for _, d := range site.AdditionalDomains {
		current[d.Domain] = d
	}
	wait := func(eventID int64, err error) error {
		if err != nil {
			return err
		}
		return r.client.WaitForEvent(ctx, eventID)
	}
	// Remove first, then add and update.
	for _, name := range sortedKeys(current) {
		if _, ok := plan[name]; ok {
			continue
		}
		if err := wait(r.client.DeleteSiteDomain(ctx, site.ID, current[name].ID)); err != nil && !client.IsNotFound(err) {
			return fmt.Errorf("removing %s: %w", name, err)
		}
	}
	for _, name := range sortedKeys(plan) {
		want := redirectRequest(plan[name].Redirect)
		have, ok := current[name]
		switch {
		case !ok:
			if err := wait(r.client.AddSiteDomain(ctx, site.ID, name, want)); err != nil {
				return fmt.Errorf("adding %s: %w", name, err)
			}
		case !sameRedirect(want, have.Redirect):
			if err := wait(r.client.UpdateSiteDomain(ctx, site.ID, have.ID, want)); err != nil {
				return fmt.Errorf("updating %s: %w", name, err)
			}
		}
	}
	return nil
}

// domainsFromSite returns the site's additional domains, or nil when they aren't managed.
func domainsFromSite(s *client.Site, current map[string]siteDomainModel) map[string]siteDomainModel {
	if current == nil {
		return nil
	}
	out := make(map[string]siteDomainModel, len(s.AdditionalDomains))
	for _, d := range s.AdditionalDomains {
		var m siteDomainModel
		if d.Redirect.Enabled {
			m.Redirect = &siteDomainRedirectModel{
				Type:        types.Int64Value(d.Redirect.Type),
				Destination: types.StringValue(d.Redirect.Destination),
			}
		}
		out[d.Domain] = m
	}
	return out
}

func redirectRequest(m *siteDomainRedirectModel) client.DomainRedirect {
	if m == nil {
		return client.DomainRedirect{}
	}
	return client.DomainRedirect{
		Enabled:     true,
		Type:        m.Type.ValueInt64(),
		Destination: m.Destination.ValueString(), // empty when not configured: SpinupWP picks it
	}
}

// sameRedirect compares a wanted redirect with SpinupWP's. An empty destination accepts any.
func sameRedirect(want, have client.DomainRedirect) bool {
	if want.Enabled != have.Enabled {
		return false
	}
	if !want.Enabled {
		return true
	}
	return want.Type == have.Type && (want.Destination == "" || want.Destination == have.Destination)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
