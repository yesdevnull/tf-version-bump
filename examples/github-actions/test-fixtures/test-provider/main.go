package main

import (
	"fmt"
	"log"
	"os"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
)

// recordObservedEnvironment writes the two channel variables Terraform handed
// to the plugin process, so a test can observe what reached a Terraform command.
// Terraform launches this provider during validate but not during init, and
// recording stays inert unless TEST_OBSERVATION_PATH is supplied.
func recordObservedEnvironment() {
	path := os.Getenv("TEST_OBSERVATION_PATH")
	if path == "" {
		return
	}
	observation := fmt.Sprintf("input=%s secret=%s\n",
		os.Getenv("TEST_INPUT_CHANNEL"), os.Getenv("TEST_SECRET_CHANNEL"))
	if err := os.WriteFile(path, []byte(observation), 0o600); err != nil {
		log.Fatalf("could not record the observed environment: %v", err)
	}
	// When TEST_EXACT_NAME names a variable, that variable's value is also written
	// byte for byte to a second file, because the summary line cannot hold a
	// multi-line value.
	name := os.Getenv("TEST_EXACT_NAME")
	if name == "" {
		return
	}
	if err := os.WriteFile(path+".exact", []byte(os.Getenv(name)), 0o600); err != nil {
		log.Fatalf("could not record the observed value: %v", err)
	}
}

func main() {
	recordObservedEnvironment()
	plugin.Serve(&plugin.ServeOpts{
		ProviderAddr: "registry.terraform.io/yesdevnull/test",
		ProviderFunc: func() *schema.Provider { return &schema.Provider{} },
	})
}
