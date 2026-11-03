package provider

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/marcossantiago/terraform-provider-spinupwp/internal/client"
)

// clientFromProviderData returns the client set by the provider's Configure.
// ProviderData is nil while Terraform validates config, before the provider is configured.
func clientFromProviderData(data any, diags *diag.Diagnostics) *client.Client {
	if data == nil {
		return nil
	}
	c, ok := data.(*client.Client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T. Please report this issue.", data))
	}
	return c
}
