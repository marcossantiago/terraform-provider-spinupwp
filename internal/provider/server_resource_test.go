package provider

import (
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const customServer = `
resource "spinupwp_server" "test" {
  hostname                  = %q
  ip_address                = "203.0.113.10"
  provider_name             = "Example Hosting"
  ubuntu_version            = "26.04"
  timezone                  = "UTC"
  database_root_password_wo = "Root-password-123"
}
`

func TestServerLifecycle(t *testing.T) {
	api := newFakeAPI(t)
	var firstID string
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: api.providerConfig() + fmt.Sprintf(customServer, "my-server"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("spinupwp_server.test", "name", "my-server"),
					resource.TestCheckResourceAttr("spinupwp_server.test", "status", "provisioned"),
					resource.TestCheckResourceAttr("spinupwp_server.test", "username", "root"),
					resource.TestCheckResourceAttr("spinupwp_server.test", "ssh_port", "22"),
					resource.TestCheckNoResourceAttr("spinupwp_server.test", "database_root_password_wo"),
					resource.TestCheckResourceAttrWith("spinupwp_server.test", "id", func(v string) error { firstID = v; return nil }),
					func(*terraform.State) error {
						want := map[string]string{"hostname": "my-server", "ip_address": "203.0.113.10", "provider_name": "Example Hosting",
							"ubuntu_version": "26.04", "username": "root", "ssh_port": "22", "auth_method": "publickey",
							"timezone": "UTC", "database_root_password": "Root-password-123"}
						for k, v := range want {
							if api.lastServer[k] != v {
								return fmt.Errorf("sent %s=%q, want %q (form %v)", k, api.lastServer[k], v, api.lastServer)
							}
						}
						if _, ok := api.lastServer["post_provision_script"]; ok {
							return fmt.Errorf("unset post_provision_script was sent")
						}
						return nil
					},
				),
			},
			{
				// Every input is create-only: a new hostname provisions a new server.
				Config: api.providerConfig() + fmt.Sprintf(customServer, "my-other-server"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_server.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: func(*terraform.State) error {
					id, _ := strconv.ParseInt(firstID, 10, 64)
					if api.serverExists(id) {
						return fmt.Errorf("replaced server %d still in SpinupWP", id)
					}
					return nil
				},
			},
			{
				ResourceName:            "spinupwp_server.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"username", "ssh_port", "timezone"}, // not returned by the API
			},
		},
		CheckDestroy: func(*terraform.State) error {
			if len(api.servers) != 3 {
				return fmt.Errorf("%d servers left after destroy, want the 3 fixtures", len(api.servers))
			}
			return nil
		},
	})
}

func TestServerProvisioningFailureIsTainted(t *testing.T) {
	api := newFakeAPI(t)
	api.failNextEvent = true
	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks:   requireWriteOnlySupport,
		ProtoV6ProviderFactories: protoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      api.providerConfig() + fmt.Sprintf(customServer, "my-server"),
				ExpectError: regexp.MustCompile(`Error provisioning SpinupWP server my-server`),
			},
			{
				// Kept in state as tainted, so the next apply replaces it instead of leaving it behind.
				Config: api.providerConfig() + fmt.Sprintf(customServer, "my-server"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("spinupwp_server.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
			},
		},
	})
}
