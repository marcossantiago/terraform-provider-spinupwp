package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

var protoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"spinupwp": providerserver.NewProtocol6WithError(New("test")()),
}

// Write-only attributes need Terraform 1.11 or later.
var requireWriteOnlySupport = []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)}

const blankSite = `
resource "spinupwp_site" "test" {
  server_id           = 1
  domain              = "blank.example.com"
  site_user           = "blank"
  installation_method = "blank"
  php_version         = %q
  page_cache_enabled  = %t
}
`

func TestSiteLifecycle(t *testing.T) {
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: api.providerConfig() + fmt.Sprintf(blankSite, "8.2", false),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("php_version"), knownvalue.StringExact("8.2")),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("page_cache_enabled"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("status"), knownvalue.StringExact("deployed")),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("is_wordpress"), knownvalue.Bool(false)),
				},
			},
			{
				// PHP version and page cache change in place.
				Config: api.providerConfig() + fmt.Sprintf(blankSite, "8.4", true),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_site.test", plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("php_version"), knownvalue.StringExact("8.4")),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("page_cache_enabled"), knownvalue.Bool(true)),
				},
			},
			{
				ResourceName:            "spinupwp_site.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"installation_method"}, // create-only, not returned by the API
			},
		},
		CheckDestroy: func(*terraform.State) error {
			if n := api.siteCount(); n != 0 {
				return fmt.Errorf("%d sites left after destroy", n)
			}
			if api.lastDelete["delete_database"] != "0" || api.lastDelete["delete_backups"] != "0" {
				return fmt.Errorf("destroy should keep the database and backups by default, sent %v", api.lastDelete)
			}
			return nil
		},
	})
}

func TestSiteWordPressPasswordsAreWriteOnly(t *testing.T) {
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: api.providerConfig() + `
resource "spinupwp_site" "test" {
  server_id           = 1
  domain              = "wp.example.com"
  site_user           = "wp"
  installation_method = "wp"
  delete_database     = true

  database = {
    name        = "wp"
    username    = "wp"
    password_wo = "Db-password-123"
  }

  wordpress = {
    title             = "Example"
    admin_user        = "admin"
    admin_email       = "admin@example.com"
    admin_password_wo = "admin-password-123"
  }
}
`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("spinupwp_site.test", "is_wordpress", "true"),
				resource.TestCheckResourceAttr("spinupwp_site.test", "wordpress.admin_user", "admin"),
				resource.TestCheckNoResourceAttr("spinupwp_site.test", "wordpress.admin_password_wo"),
				resource.TestCheckNoResourceAttr("spinupwp_site.test", "database.password_wo"),
				func(*terraform.State) error {
					// The passwords still reach the API.
					if api.lastCreate["wordpress[admin_password]"] != "admin-password-123" ||
						api.lastCreate["database[password]"] != "Db-password-123" {
						return fmt.Errorf("passwords not sent to the API: %v", api.lastCreate)
					}
					return nil
				},
			),
		}},
		CheckDestroy: func(*terraform.State) error {
			if api.lastDelete["delete_database"] != "1" {
				return fmt.Errorf("delete_database = true not sent: %v", api.lastDelete)
			}
			return nil
		},
	})
}

func TestSiteDomainChangeReplaces(t *testing.T) {
	api := newFakeAPI(t)
	cfg := func(domain string) string {
		return api.providerConfig() + fmt.Sprintf(`
resource "spinupwp_site" "test" {
  server_id           = 1
  domain              = %q
  site_user           = "blank"
  installation_method = "blank"
}
`, domain)
	}
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg("one.example.com")},
			{
				Config: cfg("two.example.com"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_site.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
			},
		},
	})
}

// Importing a site and adding its create-only settings to the config must never plan a replacement.
func TestSiteImportDoesNotReplace(t *testing.T) {
	api := newFakeAPI(t)
	id := api.addSite(1, "existing.example.com", "existing")
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: api.providerConfig() + fmt.Sprintf(`
import {
  to = spinupwp_site.test
  id = "%d"
}

resource "spinupwp_site" "test" {
  server_id           = 1
  domain              = "existing.example.com"
  site_user           = "existing"
  installation_method = "wp"

  database = {
    name     = "existing"
    username = "existing"
  }

  wordpress = {
    title             = "Existing"
    admin_user        = "admin"
    admin_email       = "admin@example.com"
    admin_password_wo = "not-used-on-import"
  }
}
`, id),
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_site.test", plancheck.ResourceActionUpdate)},
			},
		}},
	})
}

func TestSiteFailedDeploymentIsReported(t *testing.T) {
	api := newFakeAPI(t)
	api.failNextEvent = true
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      api.providerConfig() + fmt.Sprintf(blankSite, "8.3", false),
			ExpectError: regexp.MustCompile(`(?s)Error deploying SpinupWP site.*deploy script exited 1`),
		}},
		// The failed site stays in state as tainted, so the test's destroy cleans it up.
		CheckDestroy: func(*terraform.State) error {
			if n := api.siteCount(); n != 0 {
				return fmt.Errorf("tainted site was not destroyed: %d sites left", n)
			}
			return nil
		},
	})
}

func TestSiteDeletedOutsideTerraformIsRecreated(t *testing.T) {
	api := newFakeAPI(t)
	cfg := api.providerConfig() + fmt.Sprintf(blankSite, "8.3", false)
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig: api.deleteAllSites,
				Config:    cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_site.test", plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

func TestSiteValidation(t *testing.T) {
	api := newFakeAPI(t)
	tests := map[string]struct {
		body string
		err  string
	}{
		"wp needs wordpress": {
			body: `installation_method = "wp"
  database = { name = "x", username = "x" }`,
			err: "needs a wordpress block",
		},
		"wp needs a database": {
			body: `installation_method = "wp"
  wordpress = { title = "x", admin_user = "x", admin_email = "x@example.com", admin_password_wo = "12345678" }`,
			err: "needs a database block",
		},
		"blank rejects wordpress": {
			body: `installation_method = "blank"
  wordpress = { title = "x", admin_user = "x", admin_email = "x@example.com", admin_password_wo = "12345678" }`,
			err: "only used with a WordPress",
		},
		"unknown method": {body: `installation_method = "joomla"`, err: "value must be one of"},
		"weak database password": {body: `installation_method = "blank"
  database = { name = "x", username = "x", password_wo = "lowercase-only" }`, err: "must contain an uppercase letter"},
		"bad php version": {body: `installation_method = "blank"
  php_version = "latest"`, err: "must look like 8.3"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				TerraformVersionChecks:   requireWriteOnlySupport,
				ProtoV6ProviderFactories: protoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config: api.providerConfig() + `
resource "spinupwp_site" "test" {
  server_id = 1
  domain    = "v.example.com"
  site_user = "v"
  ` + tt.body + `
}
`,
					ExpectError: regexp.MustCompile(tt.err),
				}},
			})
		})
	}
}

func TestServerDataSource(t *testing.T) {
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: api.providerConfig() + `
data "spinupwp_server" "by_name" { name = "my-server" }
data "spinupwp_server" "by_id" { id = 1 }
`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.spinupwp_server.by_name", tfjsonpath.New("id"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue("data.spinupwp_server.by_name", tfjsonpath.New("ip_address"), knownvalue.StringExact("192.0.2.10")),
					statecheck.ExpectKnownValue("data.spinupwp_server.by_name", tfjsonpath.New("database_host"), knownvalue.StringExact("db.example.com")),
					statecheck.ExpectKnownValue("data.spinupwp_server.by_id", tfjsonpath.New("name"), knownvalue.StringExact("my-server")),
				},
			},
			{
				Config:      api.providerConfig() + `data "spinupwp_server" "x" { name = "twin" }`,
				ExpectError: regexp.MustCompile(`2 servers are named "twin"`),
			},
			{
				Config:      api.providerConfig() + `data "spinupwp_server" "x" { name = "nope" }`,
				ExpectError: regexp.MustCompile(`no server named "nope"`),
			},
			{
				Config:      api.providerConfig() + `data "spinupwp_server" "x" {}`,
				ExpectError: regexp.MustCompile(`Exactly one of these attributes must be configured`),
			},
			// End on a valid config: the test's final destroy uses the last config.
			{Config: api.providerConfig() + `data "spinupwp_server" "x" { id = 1 }`},
		},
	})
}

func TestProviderRequiresToken(t *testing.T) {
	t.Setenv(envAPIToken, "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      `data "spinupwp_server" "x" { id = 1 }`,
			ExpectError: regexp.MustCompile(`Missing SpinupWP API token`),
		}},
	})
}
