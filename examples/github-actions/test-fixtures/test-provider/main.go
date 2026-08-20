package main

import (
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/plugin"
)

func main() {
	plugin.Serve(&plugin.ServeOpts{
		ProviderAddr: "registry.terraform.io/yesdevnull/test",
		ProviderFunc: func() *schema.Provider { return &schema.Provider{} },
	})
}
