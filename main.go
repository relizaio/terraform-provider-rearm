// terraform-provider-rearm: a Terraform / OpenTofu provider for ReARM, built on
// github.com/relizaio/rearm-client-go.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/relizaio/terraform-provider-rearm/internal/provider"
)

// version is set by goreleaser at build time.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run the provider with support for debuggers")
	flag.Parse()

	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/relizaio/rearm",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
