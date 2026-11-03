package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/marcossantiago/terraform-provider-spinupwp/internal/provider"
)

// Generate docs/ from the schema and examples/.
//go:generate go tool tfplugindocs generate --provider-name spinupwp

// version is set by GoReleaser.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/marcossantiago/spinupwp",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
