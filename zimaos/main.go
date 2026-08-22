// Package main is the provider entrypoint. It has to live in its own directory
// (not repo root) because provider.go at root declares `package terraformproviderzimaos`,
// and a directory can only hold one non-test package.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	terraformproviderzimaos "github.com/brian-guerrero/terraform-provider-zimaos"
)

// version is overridden via -ldflags at release-build time; "dev" for local builds.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	err := providerserver.Serve(context.Background(), terraformproviderzimaos.New(), providerserver.ServeOpts{
		Address: "registry.terraform.io/brian-guerrero/zimaos",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err.Error())
	}
}
