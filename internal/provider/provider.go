// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
)

// Ensure PxcProvider satisfies various provider interfaces.
var _ provider.Provider = &PxcProvider{}
var _ provider.ProviderWithFunctions = &PxcProvider{}
var _ provider.ProviderWithEphemeralResources = &PxcProvider{}
var _ provider.ProviderWithActions = &PxcProvider{}

// PxcProvider defines the provider implementation.
type PxcProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
	exitCh chan bool
}

// PxcProviderModel describes the provider data model.
type PxcProviderModel struct {
	TargetPve    types.String `tfsdk:"target_pve"`
	K8sStackName types.String `tfsdk:"k8s_stack_name"`
	exitCh chan bool
}

func (p *PxcProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "pxc"
	resp.Version = p.version
}

func (p *PxcProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"target_pve": schema.StringAttribute{
				MarkdownDescription: "Target proxmox cloud environment (e.g. your-cluster.your-cloud.domain).",
				Required:            true,
			},
			"k8s_stack_name": schema.StringAttribute{
				MarkdownDescription: "Stack name of your kubespray cluster defined in the custom inventory file.",
				Required:            true,
			},
		},
	}

}

func (p *PxcProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data PxcProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// simply pass the full model as data
	resp.DataSourceData = data
	resp.ResourceData = data
	resp.EphemeralResourceData = data

	// launch our python grpc server

	// todo: implement option to specify pythonpath in provider and pass that up here somehow
	// or find a better solution
	virtualEnv := os.Getenv("VIRTUAL_ENV")
	if virtualEnv == "" {
		resp.Diagnostics.AddError("Client Error", "VIRTUAL_ENV not defined, cant launch gprc")
		return
	}

	// with this env var we can determine if we are running in a pytest context
	pytestCurrent := os.Getenv("PYTEST_CURRENT_TEST")

	// only install the pypi package if not in e2e scenario (in this case its installed via pip -e .)
	if pytestCurrent == "" && p.version != "dev" {
		// package will be published to pypi with same version tag as provider
		// todo: check against installed version and prevent from removing / missmatching
		pipCmd := exec.Command(fmt.Sprintf("%s/bin/pip", virtualEnv), "install", fmt.Sprintf("rpyc-pve-cloud==%s", p.version))

		output, err := pipCmd.CombinedOutput()
		if err != nil {
			resp.Diagnostics.AddError("Could not launch rpc server", fmt.Sprintf("Command failed with error: %v - %s", err, string(output)))
			return
		}
	}

	// start pyhon grpc server as daemon
	tflog.Info(ctx, fmt.Sprintf("Launching python rpc server on unix:///tmp/pc-rpc-%d.sock", os.Getpid()))
	cmd := exec.Command(fmt.Sprintf("%s/bin/pcrpc", virtualEnv), strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		resp.Diagnostics.AddError("Failed to start Python backend", err.Error())
		return
	}

	// launch routine to kill the server
	go func(){
		<-p.exitCh // wait for exit signal
		
		cmd.Process.Kill() // kill

		p.exitCh <- true // call finished
	}()

	// wait for rpc to come up and healthcheck to succeed
	deadline := time.Now().Add(10 * time.Second)

	for {
		if time.Now().After(deadline) {
			resp.Diagnostics.AddError("Failed to start python grpc server", "Deadline exceeded")
			return
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
		hresp, err := healthClient.Check(ctx, &pb.HealthCheckRequest{TargetPve: data.TargetPve.ValueString()})

		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if hresp.Status == pb.HealthCheckResponse_MISSMATCH {
			resp.Diagnostics.AddError("Failed to start python grpc server", hresp.ErrorMessage)
			return
		}

		// this case should never hit.
		// todo: refactor
		if hresp.Status != pb.HealthCheckResponse_SERVING {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		break // its up and running
	}

}

func (p *PxcProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{}
}

func (p *PxcProvider) EphemeralResources(ctx context.Context) []func() ephemeral.EphemeralResource {
	return []func() ephemeral.EphemeralResource{
		NewKubeconfigEphemeralResource,
	}
}

func (p *PxcProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewClusterVarsDataSource,
		NewCloudSecretDataSource,
		NewCephAccessDataSource,
		NewSshKeyDataSource,
		NewPveApiGetDataSource,
		NewProxmoxHostDataSource,
		NewPveInventoryDataSource,
	}
}

func (p *PxcProvider) Functions(ctx context.Context) []func() function.Function {
	return []func() function.Function{}
}

func (p *PxcProvider) Actions(ctx context.Context) []func() action.Action {
	return []func() action.Action{}
}

func New(version string, exitCh chan bool) func() provider.Provider {
	return func() provider.Provider {
		return &PxcProvider{
			version: version,
			exitCh: exitCh,
		}
	}
}