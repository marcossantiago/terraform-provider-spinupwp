package provider

import (
	"fmt"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestSiteAdditionalDomains(t *testing.T) {
	api := newFakeAPI(t)
	site := func(domains string) string {
		return api.providerConfig() + `
resource "spinupwp_site" "test" {
  server_id           = 1
  domain              = "feature.example.com"
  site_user           = "feature"
  installation_method = "blank"
  ` + domains + `
}
`
	}
	domains := func(want ...string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			if got := api.domainNames(); !slices.Equal(got, want) {
				return fmt.Errorf("SpinupWP has domains %v, want %v", got, want)
			}
			return nil
		}
	}
	// A new path each time: AtMapKey appends to the same backing array.
	redirect := func(attr string) tfjsonpath.Path {
		return tfjsonpath.New("additional_domains").AtMapKey("old.example.com").AtMapKey("redirect").AtMapKey(attr)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// Created with the site. Without a destination, SpinupWP redirects to the primary domain.
				Config: site(`additional_domains = {
    "www.example.com" = {}
    "old.example.com" = { redirect = {} }
  }`),
				Check: domains("old.example.com", "www.example.com"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("additional_domains").AtMapKey("www.example.com").AtMapKey("redirect"), knownvalue.Null()),
					statecheck.ExpectKnownValue("spinupwp_site.test", redirect("type"), knownvalue.Int64Exact(301)),
					statecheck.ExpectKnownValue("spinupwp_site.test", redirect("destination"), knownvalue.StringExact("feature.example.com")),
				},
			},
			{
				// One removed, one added, one changed, in place.
				Config: site(`additional_domains = {
    "new.example.com" = {}
    "old.example.com" = {
      redirect = {
        type        = 302
        destination = "https://example.org"
      }
    }
  }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_site.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					domains("new.example.com", "old.example.com"),
					func(*terraform.State) error {
						if api.lastDomain["redirect[type]"] != "302" || api.lastDomain["redirect[destination]"] != "https://example.org" {
							return fmt.Errorf("redirect not sent: %v", api.lastDomain)
						}
						return nil
					},
				),
			},
			{
				// A domain added in the dashboard is drift, and the apply removes it.
				PreConfig: func() { api.addDomain("extra.example.com") },
				Config: site(`additional_domains = {
    "new.example.com" = {}
    "old.example.com" = {
      redirect = {
        type        = 302
        destination = "https://example.org"
      }
    }
  }`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_site.test", plancheck.ResourceActionUpdate)},
				},
				Check: domains("new.example.com", "old.example.com"),
			},
			{
				// Unset: Terraform stops managing them and leaves them in place.
				Config: site(""),
				Check:  domains("new.example.com", "old.example.com"),
			},
			{
				PreConfig: func() { api.addDomain("manual.example.com") },
				Config:    site(""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: domains("manual.example.com", "new.example.com", "old.example.com"),
			},
			{
				// {} removes them all.
				Config: site(`additional_domains = {}`),
				Check:  domains(),
			},
		},
	})
}

// Managing domains that already exist, e.g. after an import, adds nothing and changes only what differs.
func TestSiteAdditionalDomainsAdoptsExisting(t *testing.T) {
	api := newFakeAPI(t)
	cfg := func(domains string) string {
		return api.providerConfig() + `
resource "spinupwp_site" "test" {
  server_id           = 1
  domain              = "feature.example.com"
  site_user           = "feature"
  installation_method = "blank"
  ` + domains + `
}
`
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg("")},
			{
				PreConfig: func() {
					api.addDomain("www.example.com")
					api.domainCalls = 0
				},
				Config: cfg(`additional_domains = { "www.example.com" = {} }`),
				Check: func(*terraform.State) error {
					if api.domainCalls != 0 {
						return fmt.Errorf("%d domain calls for a domain that already matched", api.domainCalls)
					}
					return domains(api, "www.example.com")
				},
			},
		},
	})
}

func domains(api *fakeAPI, want ...string) error {
	if got := api.domainNames(); !slices.Equal(got, want) {
		return fmt.Errorf("SpinupWP has domains %v, want %v", got, want)
	}
	return nil
}
