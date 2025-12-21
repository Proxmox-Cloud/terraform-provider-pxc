// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"context"
	"flag"
	"log"

	"github.com/Proxmox-Cloud/terraform-provider-proxmox-cloud/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"fmt"
	"os"
	"os/exec"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "github.com/Proxmox-Cloud/terraform-provider-proxmox-cloud/internal/provider/protos"
	"strconv"
)

var (
	// these will be set by the goreleaser configuration
	// to appropriate values for the compiled binary.
	version string = "dev"

	// goreleaser can pass other information to the main package, such as the specific commit
	// https://goreleaser.com/cookbooks/using-main.version/
)


// we cant do any printing in this function because that messes with the provider schema 
// initialization. but we still want to hook into the main process for starting and
// killing our python grpc server daemon
func startPythonGrpc() *exec.Cmd {

	// todo: implement option to specify pythonpath in provider and pass that up here somehow
	// or find a better solution
	virtualEnv := os.Getenv("VIRTUAL_ENV")
	if virtualEnv == "" {
		log.Fatal("Failed to start Python backend")
	}

	// with this env var we can determine if we are running in a pytest context
	pytestCurrent := os.Getenv("PYTEST_CURRENT_TEST")

	// only install the pypi package if not in e2e scenario (in this case its installed via pip -e .)
	if pytestCurrent == "" {
		// package will be published to pypi with same version tag as provider
		// todo: check against installed version and prevent from removing / missmatching
		pipCmd := exec.Command(fmt.Sprintf("%s/bin/pip", virtualEnv), "install", fmt.Sprintf("rpyc-pve-cloud==%s", version))

		output, err := pipCmd.CombinedOutput()
		if err != nil {
			log.Fatal(fmt.Sprintf("Command failed with error: %v", err), string(output))
		}
	}

	// start pyhon grpc server as daemon
	cmd := exec.Command(fmt.Sprintf("%s/bin/pcrpc", virtualEnv), strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		log.Fatal("Failed to start Python backend", err.Error())
	}

	// wait for rpc to come up and healthcheck to succeed
	deadline := time.Now().Add(10 * time.Second)

	for {
		if time.Now().After(deadline) {
			log.Fatal("Failed to start Python backend - Deadline exceeded")
			return nil
		}

		// try connect via grpc and health check
		conn, err := grpc.NewClient(
			fmt.Sprintf("unix:///tmp/pc-rpc-%d.sock", os.Getpid()),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		defer conn.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		healthClient := pb.NewHealthClient(conn)
		hresp, err := healthClient.Check(ctx, &pb.HealthCheckRequest{})

		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if hresp.Status != pb.HealthCheckResponse_SERVING {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		break // its up and running
	}

	return cmd
}

func main() {
	var debug bool

	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		// TODO: Update this string with the published name of your provider.
		// Also update the tfplugindocs generate command to either remove the
		// -provider-name flag or set its value to the updated provider name.
		Address: "registry.terraform.io/hashicorp/scaffolding",
		Debug:   debug,
	}

	//start our python rpc server here
	cmd := startPythonGrpc()

	// Ensure cleanup of the Python server even if Serve panics or exits unexpectedly
	defer func() {
		// Kill the Python server after Serve finishes (even if it panics)
		if cerr := cmd.Process.Kill(); cerr != nil {
			log.Printf("Failed to kill Python backend: %v", cerr)
		}
	}()

	err := providerserver.Serve(context.Background(), provider.New(version), opts)

	if err != nil {
		log.Fatal(err.Error())
	}
}
