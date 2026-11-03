package provider

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

// Acceptance tests run against the real SpinupWP API and create real sites. They need:
//
//	TF_ACC=1
//	SPINUPWP_API_TOKEN      token with write access
//	SPINUPWP_ACC_SERVER     name of a server the tests may create sites on
//
// TestAccSiteBackups also needs SPINUPWP_ACC_STORAGE_PROVIDER_ID, SPINUPWP_ACC_BUCKET
// and SPINUPWP_ACC_REGION.
func accServer(t *testing.T) string {
	t.Helper()
	name := os.Getenv("SPINUPWP_ACC_SERVER")
	if os.Getenv("TF_ACC") != "" && (name == "" || os.Getenv(envAPIToken) == "") {
		t.Fatal("SPINUPWP_ACC_SERVER and " + envAPIToken + " must be set for acceptance tests")
	}
	return name
}

// accSite returns a test site with extra blocks, named tfacc<random>.example.com.
func accSite(server, name, body string) string {
	return fmt.Sprintf(`
data "spinupwp_server" "test" { name = %q }

resource "spinupwp_site" "test" {
  server_id       = data.spinupwp_server.test.id
  domain          = "%s.example.com"
  site_user       = %q
  delete_database = true
  delete_backups  = true
%s
}
`, server, name, name, body)
}

func TestAccServerDataSource(t *testing.T) {
	server := accServer(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`data "spinupwp_server" "test" { name = %q }`, server),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.spinupwp_server.test", tfjsonpath.New("name"), knownvalue.StringExact(server)),
				statecheck.ExpectKnownValue("data.spinupwp_server.test", tfjsonpath.New("status"), knownvalue.StringExact("provisioned")),
				statecheck.ExpectKnownValue("data.spinupwp_server.test", tfjsonpath.New("ip_address"), knownvalue.NotNull()),
			},
		}},
	})
}

// TestAccSite covers a blank site with basic auth: create, change in place, import, delete.
func TestAccSite(t *testing.T) {
	server := accServer(t)
	name := "tfacc" + acctest.RandString(8)
	cfg := func(php string, pageCache bool, user string) string {
		return accSite(server, name, fmt.Sprintf(`
  installation_method = "blank"
  php_version         = %q
  page_cache_enabled  = %t

  basic_auth = {
    username    = %q
    password_wo = "tfacc-password"
  }
`, php, pageCache, user))
	}
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg("8.3", false, "tfacc"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("status"), knownvalue.StringExact("deployed")),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("page_cache_enabled"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("basic_auth").AtMapKey("username"), knownvalue.StringExact("tfacc")),
				},
			},
			{
				Config: cfg("8.4", true, "tfaccpreview"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_site.test", plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("php_version"), knownvalue.StringExact("8.4")),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("page_cache_enabled"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("basic_auth").AtMapKey("username"), knownvalue.StringExact("tfaccpreview")),
				},
			},
			{
				ResourceName:            "spinupwp_site.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"installation_method", "delete_database", "delete_backups"},
			},
			{
				// Back to the server's default PHP, without basic auth.
				Config: accSite(server, name, `
  installation_method = "blank"
  php_version         = "8.3"
`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("basic_auth"), knownvalue.Null()),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("php_version"), knownvalue.StringExact("8.3")),
				},
			},
		},
	})
}

// TestAccSiteCron turns cron off, on and changes its interval on a blank site. The site has no
// database and keeps the default delete settings, so it is safe on a server with a shared cluster.
func TestAccSiteCron(t *testing.T) {
	server := accServer(t)
	name := "tfacc" + acctest.RandString(8)
	cfg := func(line string) string {
		return fmt.Sprintf(`
data "spinupwp_server" "test" { name = %q }

resource "spinupwp_site" "test" {
  server_id           = data.spinupwp_server.test.id
  domain              = "%s.example.com"
  site_user           = %q
  installation_method = "blank"
  %s
}
`, server, name, name, line)
	}
	expect := func(minutes int64) []statecheck.StateCheck {
		return []statecheck.StateCheck{statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("cron_interval"), knownvalue.Int64Exact(minutes))}
	}
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: cfg("cron_interval = 0"), ConfigStateChecks: expect(0)},
			{Config: cfg("cron_interval = 1"), ConfigStateChecks: expect(1)},
			{Config: cfg("cron_interval = 5"), ConfigStateChecks: expect(5)},
			{Config: cfg("cron_interval = 0"), ConfigStateChecks: expect(0)},
		},
	})
}

// TestAccSiteWordPress installs WordPress with a database on the server's database host.
func TestAccSiteWordPress(t *testing.T) {
	server := accServer(t)
	name := "tfacc" + acctest.RandString(8)
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: accSite(server, name, fmt.Sprintf(`
  installation_method = "wp"

  database = {
    name        = %q
    username    = %q
    password_wo = "Tfacc-db-1-%s"
  }

  wordpress = {
    title             = "Terraform acceptance test"
    admin_user        = "tfacc"
    admin_email       = "tfacc@example.com"
    admin_password_wo = "Tfacc-admin-%s"
  }
`, name, name, name, name)),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("status"), knownvalue.StringExact("deployed")),
				statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("is_wordpress"), knownvalue.Bool(true)),
			},
		}},
	})
}

// TestAccSiteBackups sets up storage and all three schedules, changes them, then turns them off.
func TestAccSiteBackups(t *testing.T) {
	server := accServer(t)
	storage, bucket, region := os.Getenv("SPINUPWP_ACC_STORAGE_PROVIDER_ID"), os.Getenv("SPINUPWP_ACC_BUCKET"), os.Getenv("SPINUPWP_ACC_REGION")
	if os.Getenv("TF_ACC") != "" && (storage == "" || bucket == "" || region == "") {
		t.Skip("set SPINUPWP_ACC_STORAGE_PROVIDER_ID, SPINUPWP_ACC_BUCKET and SPINUPWP_ACC_REGION to test backups")
	}
	name := "tfacc" + acctest.RandString(8)
	cfg := func(schedules string) string {
		return accSite(server, name, fmt.Sprintf(`
  installation_method = "blank"

  backups = {
    storage_provider_id = %s
    bucket              = %q
    region              = %q
    include_database    = false
    paths_to_exclude    = ["node_modules", "/files/vendor", "/files/cache"]
%s
  }
`, storage, bucket, region, schedules))
	}
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg(`
    daily = {
      hours          = [3, 15]
      retention_days = 7
      database       = false
    }
    weekly = {
      days           = [0, 3]
      hour           = 4
      retention_days = 30
      database       = false
    }
    monthly = {
      days     = [{ week = "first", day = 1 }, { week = "last", day = 5 }]
      hour     = 5
      database = false
    }
`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups").AtMapKey("bucket"), knownvalue.StringExact(bucket)),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups").AtMapKey("daily").AtMapKey("hours"),
						knownvalue.SetExact([]knownvalue.Check{knownvalue.Int64Exact(3), knownvalue.Int64Exact(15)})),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups").AtMapKey("monthly").AtMapKey("days"), knownvalue.SetSizeExact(2)),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups").AtMapKey("paths_to_exclude").AtSliceIndex(2), knownvalue.StringExact("/files/cache")),
				},
			},
			{
				ResourceName:            "spinupwp_site.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"installation_method", "delete_database", "delete_backups"},
			},
			{
				Config: cfg(`
    daily = {
      hours    = [2]
      database = false
    }
`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups").AtMapKey("weekly"), knownvalue.Null()),
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups").AtMapKey("monthly"), knownvalue.Null()),
				},
			},
			{
				Config: accSite(server, name, `  installation_method = "blank"`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("spinupwp_site.test", tfjsonpath.New("backups"), knownvalue.Null()),
				},
			},
		},
	})
}

// TestAccSiteHTTPSCustom uses a self-signed certificate, so no DNS is needed.
func TestAccSiteHTTPSCustom(t *testing.T) {
	server := accServer(t)
	name := "tfacc" + acctest.RandString(8)
	cert, key := selfSignedCert(t, name+".example.com")
	cfg := func(version int) string {
		return accSite(server, name, `  installation_method = "blank"`) + fmt.Sprintf(`
resource "spinupwp_site_https" "test" {
  site_id                = spinupwp_site.test.id
  type                   = "custom"
  certificate            = %q
  private_key_wo         = %q
  private_key_wo_version = %d
}
`, cert, key, version)
	}
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg(1),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("spinupwp_site_https.test", tfjsonpath.New("certificate_expires"), knownvalue.NotNull()),
				},
			},
			{
				// Rotating the key with the same certificate sends an update.
				Config: cfg(2),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_site_https.test", plancheck.ResourceActionUpdate)},
				},
			},
			{
				ResourceName:            "spinupwp_site_https.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"type", "certificate", "private_key_wo_version"},
			},
			{
				// Removing the resource disables HTTPS and leaves the site.
				Config: accSite(server, name, `  installation_method = "blank"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("spinupwp_site_https.test", plancheck.ResourceActionDestroy),
						plancheck.ExpectResourceAction("spinupwp_site.test", plancheck.ResourceActionNoop),
					},
				},
			},
		},
	})
}

// TestAccSiteHTTPSWebroot gets a real Let's Encrypt certificate. SPINUPWP_ACC_WEBROOT_DOMAIN must
// already resolve to the server (through Cloudflare is fine).
func TestAccSiteHTTPSWebroot(t *testing.T) {
	server := accServer(t)
	domain := os.Getenv("SPINUPWP_ACC_WEBROOT_DOMAIN")
	if os.Getenv("TF_ACC") != "" && domain == "" {
		t.Skip("set SPINUPWP_ACC_WEBROOT_DOMAIN to a domain pointing at the server to test Let's Encrypt")
	}
	user := "tfacc" + acctest.RandString(8)
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`
data "spinupwp_server" "test" { name = %q }

resource "spinupwp_site" "test" {
  server_id           = data.spinupwp_server.test.id
  domain              = %q
  site_user           = %q
  installation_method = "blank"
  delete_database     = true
  delete_backups      = true
}

resource "spinupwp_site_https" "test" {
  site_id = spinupwp_site.test.id
  type    = "webroot"
}
`, server, domain, user),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("spinupwp_site_https.test", tfjsonpath.New("certificate_expires"), knownvalue.NotNull()),
				statecheck.ExpectKnownValue("spinupwp_site_https.test", tfjsonpath.New("certificate_renews"), knownvalue.NotNull()),
			},
		}},
	})
}

// TestAccSiteHTTPSFailureKeepsSite requests a certificate for a domain that doesn't point at the
// server: HTTPS fails, and the site is not tainted.
func TestAccSiteHTTPSFailureKeepsSite(t *testing.T) {
	server := accServer(t)
	name := "tfacc" + acctest.RandString(8)
	site := accSite(server, name, `  installation_method = "blank"`)
	resource.Test(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: site + `
resource "spinupwp_site_https" "test" {
  site_id = spinupwp_site.test.id
  type    = "webroot"
}
`,
				ExpectError: regexp.MustCompile(`Error enabling HTTPS`),
			},
			{
				// The site is fine and needs no change; only HTTPS is left to retry.
				Config: site,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_site.test", plancheck.ResourceActionNoop)},
				},
			},
		},
	})
}

func selfSignedCert(t *testing.T, domain string) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: domain},
		DNSNames:     []string{domain},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}
