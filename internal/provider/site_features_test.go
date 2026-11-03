package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

const featureSite = `
resource "spinupwp_site" "test" {
  server_id           = 1
  domain              = "feature.example.com"
  site_user           = "feature"
  installation_method = "blank"
}
`

// siteStays checks that removing a feature resource left the site itself in place.
func siteStays(api *fakeAPI) resource.TestCheckFunc {
	return func(*terraform.State) error {
		if api.siteCount() != 1 {
			return fmt.Errorf("removing a feature must not delete the site: %d sites", api.siteCount())
		}
		return nil
	}
}

func TestSiteHTTPS(t *testing.T) {
	api := newFakeAPI(t)
	https := func(body string) string {
		return api.providerConfig() + featureSite + `
resource "spinupwp_site_https" "test" {
  site_id = spinupwp_site.test.id
  ` + body + `
}
`
	}
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: https(`type = "webroot"`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("spinupwp_site_https.test", tfjsonpath.New("certificate_expires"), knownvalue.StringExact("2027-01-31 00:00:00")),
				},
			},
			{
				// Switching to a custom certificate updates in place and sends the write-only key.
				Config: https(`type = "custom"
  certificate            = "-----BEGIN CERTIFICATE-----"
  private_key_wo         = "-----BEGIN PRIVATE KEY-----"
  private_key_wo_version = 1`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_site_https.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("spinupwp_site_https.test", "private_key_wo"),
					func(*terraform.State) error {
						if api.lastHTTPS["type"] != "custom" || api.lastHTTPS["private_key"] != "-----BEGIN PRIVATE KEY-----" {
							return fmt.Errorf("custom certificate not sent: %v", api.lastHTTPS)
						}
						return nil
					},
				),
			},
			{
				ResourceName:            "spinupwp_site_https.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"type", "certificate", "private_key_wo_version"}, // not returned by the API
			},
			{
				Config: api.providerConfig() + featureSite,
				Check: func(s *terraform.State) error {
					if err := siteStays(api)(s); err != nil {
						return err
					}
					var enabled any
					api.withOnlySite(func(site map[string]any) { enabled = site["https"].(map[string]any)["enabled"] })
					if enabled != false {
						return fmt.Errorf("HTTPS still enabled after removing the resource")
					}
					return nil
				},
			},
		},
	})
}

// Importing HTTPS that is already on must record the settings without reissuing the certificate.
func TestSiteHTTPSImportDoesNotReissue(t *testing.T) {
	api := newFakeAPI(t)
	id := api.addSite(1, "existing.example.com", "existing") // HTTPS already on
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: api.providerConfig() + fmt.Sprintf(`
import {
  to = spinupwp_site_https.test
  id = "%d"
}

resource "spinupwp_site_https" "test" {
  site_id = %d
  type    = "webroot"
}
`, id, id),
			Check: func(*terraform.State) error {
				api.mu.Lock()
				defer api.mu.Unlock()
				if api.httpsCalls != 0 {
					return fmt.Errorf("import sent %d HTTPS requests, want 0", api.httpsCalls)
				}
				return nil
			},
		}},
	})
}

func TestSiteHTTPSValidation(t *testing.T) {
	api := newFakeAPI(t)
	tests := map[string]struct{ body, err string }{
		"dns needs a provider":   {`type = "dns"`, `needs dns_provider_id`},
		"custom needs a cert":    {`type = "custom"`, `needs certificate and private_key_wo`},
		"provider only with dns": {"type = \"webroot\"\n  dns_provider_id = 3", `only used with type = "dns"`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				TerraformVersionChecks:   requireWriteOnlySupport,
				ProtoV6ProviderFactories: protoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      api.providerConfig() + "resource \"spinupwp_site_https\" \"test\" {\n  site_id = 1\n  " + tt.body + "\n}",
					ExpectError: regexp.MustCompile(tt.err),
				}},
			})
		})
	}
}

// siteWith returns the feature test site with extra blocks.
func siteWith(api *fakeAPI, blocks string) string {
	return api.providerConfig() + `
resource "spinupwp_site" "test" {
  server_id           = 1
  domain              = "feature.example.com"
  site_user           = "feature"
  installation_method = "blank"
` + blocks + `
}
`
}

func basicAuthBlock(user, version string) string {
	return fmt.Sprintf(`
  basic_auth = {
    username            = %q
    password_wo         = "pass-%s"
    password_wo_version = %s
  }
`, user, version, version)
}

func TestSiteBasicAuth(t *testing.T) {
	api := newFakeAPI(t)
	lastBasicAuth := func() map[string]string {
		api.mu.Lock()
		defer api.mu.Unlock()
		return api.lastBasicAuth
	}
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: siteWith(api, basicAuthBlock("staging", "1")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("spinupwp_site.test", "basic_auth.username", "staging"),
					resource.TestCheckNoResourceAttr("spinupwp_site.test", "basic_auth.password_wo"),
					func(*terraform.State) error {
						if lastBasicAuth()["password"] != "pass-1" {
							return fmt.Errorf("password not sent: %v", lastBasicAuth())
						}
						return nil
					},
				),
			},
			{
				// A new username alone keeps the current password.
				Config: siteWith(api, `
  basic_auth = {
    username            = "preview"
    password_wo         = "pass-1"
    password_wo_version = 1
  }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_site.test", plancheck.ResourceActionUpdate)},
				},
				Check: func(*terraform.State) error {
					if _, sent := lastBasicAuth()["password"]; sent || lastBasicAuth()["username"] != "preview" {
						return fmt.Errorf("username change should not resend the password: %v", lastBasicAuth())
					}
					return nil
				},
			},
			{
				// Bumping the version sends the new password.
				Config: siteWith(api, basicAuthBlock("preview", "2")),
				Check: func(*terraform.State) error {
					if lastBasicAuth()["password"] != "pass-2" {
						return fmt.Errorf("new password not sent: %v", lastBasicAuth())
					}
					return nil
				},
			},
			{
				ResourceName:            "spinupwp_site.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"installation_method", "basic_auth.password_wo_version"},
			},
			{
				// Turned off in the dashboard: Terraform turns it back on.
				PreConfig: func() {
					api.withOnlySite(func(site map[string]any) { site["basic_auth"] = map[string]any{"enabled": false} })
				},
				Config: siteWith(api, basicAuthBlock("preview", "2")),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_site.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr("spinupwp_site.test", "basic_auth.username", "preview"),
			},
			{
				// Removing the block turns basic auth off.
				Config: siteWith(api, ""),
				Check: func(*terraform.State) error {
					var enabled any
					api.withOnlySite(func(site map[string]any) { enabled = site["basic_auth"].(map[string]any)["enabled"] })
					if enabled != false {
						return fmt.Errorf("basic auth still enabled after removing the block")
					}
					return nil
				},
			},
		},
	})
}

const backupStorage = `
    storage_provider_id = 4
    bucket              = "my-backups"
    region              = "nyc3"
    include_database    = true
    paths_to_exclude    = ["node_modules", "/files/vendor", "/files/cache"]
`

func TestSiteBackups(t *testing.T) {
	api := newFakeAPI(t)
	backups := func(schedules string) string {
		return siteWith(api, "\n  backups = {"+backupStorage+schedules+"  }\n")
	}
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: backups(`
    daily = {
      hours          = [3, 15]
      retention_days = 30
    }
    weekly = {
      days     = [0]
      hour     = 4
      database = false
    }
    monthly = {
      days = [{ week = "first", day = 1 }]
      hour = 5
    }
`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups").AtMapKey("bucket"), knownvalue.StringExact("my-backups")),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups").AtMapKey("daily").AtMapKey("hours"),
						knownvalue.SetExact([]knownvalue.Check{knownvalue.Int64Exact(3), knownvalue.Int64Exact(15)})),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups").AtMapKey("weekly").AtMapKey("database"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups").AtMapKey("monthly").AtMapKey("files"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups").AtMapKey("paths_to_exclude").AtSliceIndex(2), knownvalue.StringExact("/files/cache")),
				},
			},
			{
				ResourceName:            "spinupwp_site.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"installation_method"},
			},
			{
				// Removing a schedule turns it off; the rest is updated in place.
				Config: backups(`
    daily = {
      hours = [3]
    }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_site.test", plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups").AtMapKey("weekly"), knownvalue.Null()),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups").AtMapKey("monthly"), knownvalue.Null()),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups").AtMapKey("daily").AtMapKey("retention_days"), knownvalue.Null()),
				},
			},
			{
				// Removing the block turns every schedule off.
				Config: siteWith(api, ""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups"), knownvalue.Null()),
				},
				Check: func(*terraform.State) error {
					var daily map[string]any
					api.withOnlySite(func(site map[string]any) {
						daily = site["backups"].(map[string]any)["schedule"].(map[string]any)["daily"].(map[string]any)
					})
					if hours, _ := daily["time_of_day"].([]any); len(hours) != 0 {
						return fmt.Errorf("daily backups still scheduled after removing the block: %v", daily)
					}
					return nil
				},
			},
		},
	})
}

// An existing site's basic auth and backups are picked up on import.
func TestSiteImportReadsFeatures(t *testing.T) {
	api := newFakeAPI(t)
	id := api.addSite(1, "existing.example.com", "existing")
	api.withOnlySite(func(site map[string]any) {
		site["basic_auth"] = map[string]any{"enabled": true, "username": "preview"}
		b := site["backups"].(map[string]any)
		b["storage_provider"] = map[string]any{"id": 4, "bucket": "my-backups", "region": "nyc3"}
		b["database"] = true
		b["schedule"].(map[string]any)["daily"] = map[string]any{"time_of_day": []int64{3}, "backup_files": true, "backup_database": true, "retention_period": 30}
	})
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
  installation_method = "blank"

  basic_auth = {
    username    = "preview"
    password_wo = "not-sent"
  }

  backups = {
    storage_provider_id = 4
    bucket              = "my-backups"
    daily = {
      hours          = [3]
      retention_days = 30
    }
  }
}
`, id),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("basic_auth").AtMapKey("username"), knownvalue.StringExact("preview")),
				statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups").AtMapKey("region"), knownvalue.StringExact("nyc3")),
			},
			Check: func(*terraform.State) error {
				api.mu.Lock()
				defer api.mu.Unlock()
				if api.lastBasicAuth != nil {
					return fmt.Errorf("import resent basic auth: %v", api.lastBasicAuth)
				}
				return nil
			},
		}},
	})
}

func TestSiteBackupsValidation(t *testing.T) {
	api := newFakeAPI(t)
	tests := map[string]struct{ body, err string }{
		"needs a schedule":  {"", `needs at least one of daily, weekly or monthly`},
		"hour out of range": {"daily = { hours = [24] }", `between 0 and 23`},
		"no hours":          {"daily = { hours = [] }", `at least 1`},
		"bad week":          {`monthly = { days = [{ week = "fifth", day = 1 }], hour = 2 }`, `value\s+must\s+be\s+one\s+of`},
		"bad weekday":       {"weekly = { days = [7], hour = 2 }", `between 0 and 6`},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				TerraformVersionChecks:   requireWriteOnlySupport,
				ProtoV6ProviderFactories: protoV6ProviderFactories,
				Steps: []resource.TestStep{{
					Config:      siteWith(api, "  backups = {\n    storage_provider_id = 4\n    bucket = \"b\"\n    "+tt.body+"\n  }"),
					ExpectError: regexp.MustCompile(tt.err),
				}},
			})
		})
	}
}

func TestSiteCron(t *testing.T) {
	api := newFakeAPI(t)
	cron := func(line string) string { return siteWith(api, "  "+line) }
	expect := func(minutes int64) []statecheck.StateCheck {
		return []statecheck.StateCheck{statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("cron_interval"), knownvalue.Int64Exact(minutes))}
	}
	update := resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_site.test", plancheck.ResourceActionUpdate)},
	}
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: cron(""), ConfigStateChecks: expect(1)}, // unset keeps the default
			{Config: cron("cron_interval = 0"), ConfigPlanChecks: update, ConfigStateChecks: expect(0)},
			{Config: cron("cron_interval = 5"), ConfigPlanChecks: update, ConfigStateChecks: expect(5)},
			{Config: cron("cron_interval = 15"), ConfigPlanChecks: update, ConfigStateChecks: expect(15)},
			{Config: cron(""), ConfigStateChecks: expect(15)}, // removing it leaves cron as it is
			{
				ResourceName:            "spinupwp_site.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"installation_method"},
			},
			{Config: cron("cron_interval = 3"), ExpectError: regexp.MustCompile(`cron_interval`)},
			{Config: cron("cron_interval = 0"), ConfigStateChecks: expect(0)},
		},
	})
}

// A new site created with cron off never runs it, even when SpinupWP turns it on by default.
func TestSiteCreatedWithCronOff(t *testing.T) {
	api := newFakeAPI(t)
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: siteWith(api, "  cron_interval = 0"),
			Check: func(*terraform.State) error {
				var enabled any
				api.withOnlySite(func(site map[string]any) { enabled = site["cron"].(map[string]any)["enabled"] })
				if enabled != false {
					return fmt.Errorf("cron still on")
				}
				return nil
			},
		}},
	})
}
