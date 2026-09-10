package main

import (
	"fmt"
	"log"
	"os"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
)

// recordObservedEnvironment writes the environment Terraform handed to the
// plugin process, so a test can observe what reached a Terraform command.
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
	// The variable TEST_EXACT_NAME names is recorded byte for byte beside the
	// summary, so a test can observe a value that one summary line cannot hold.
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
