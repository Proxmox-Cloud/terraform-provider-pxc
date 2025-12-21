// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"flag"
	"log"

	"github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

var (
	// these will be set by the goreleaser configuration
	// to appropriate values for the compiled binary.
	version string = "dev"

	// goreleaser can pass other information to the main package, such as the specific commit
	// https://goreleaser.com/cookbooks/using-main.version/
)

// util for debugging
// func debugFile(message string) {

// 	timestamp := time.Now().Format("2006-01-02_15-04-05.000")

// 	// Create a filename with the timestamp
// 	filename := fmt.Sprintf("/home/cloud/pve-cloud/terraform-pxc-backup/%s_%s.txt", message, timestamp)

// 	file, err := os.Create(filename)
// 	if err != nil {
// 		log.Fatal(err)
// 	}
// 	defer file.Close()

// 	// Write some content to the file
// 	_, err = file.WriteString(message)
// 	if err != nil {
// 		log.Fatal(err)
// 	}
// }


func main() {
	var debug bool

	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		// TODO: Update this string with the published name of your provider.
		// Also update the tfplugindocs generate command to either remove the
		// -provider-name flag or set its value to the updated provider name.
		Address: "registry.terraform.io/proxmox-cloud/proxmox-cloud",
		Debug:   debug,
	}

	exitCh := make(chan bool)
	
	err := providerserver.Serve(context.Background(), provider.New(version, exitCh), opts)

	exitCh <- true // send exit signal to rypc goroutne
	<-exitCh // wait for kill done response
 
	if err != nil {
		log.Fatal(err.Error())
	}
}
