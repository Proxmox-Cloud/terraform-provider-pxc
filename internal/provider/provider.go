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

	"time"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
    pb "github.com/Proxmox-Cloud/terraform-provider-proxmox-cloud/internal/provider/protos"
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
}

// PxcProviderModel describes the provider data model.
type PxcProviderModel struct {
	TargetPve    types.String `tfsdk:"target_pve"`
	K8sStackName types.String `tfsdk:"k8s_stack_name"`
	PythonPath   types.String `tfsdk:"python_path"`
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
			"python_path": schema.StringAttribute{
				MarkdownDescription: "Path to python interpreters folder for example /usr/local or ~/.pve-cloud-venv, defaults to $VIRUAL_ENV set by pythons venv activate function.",
				Optional:            true,
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

	// todo: make toggable for e2e
	start_pcrpc := false

	if start_pcrpc {
		tflog.Info(ctx, fmt.Sprintf("provider version %s", p.version))

		if data.PythonPath.IsNull() {
			// user didnt specify, try to fallback to VENV
			virtualEnv := os.Getenv("VIRTUAL_ENV")
			if virtualEnv == "" {
				resp.Diagnostics.AddError(
					"Failed to start Python backend",
					"No python_path specified in provider and $VIRTUAL_ENV undefined",
				)
				return
			}
			data.PythonPath = types.StringValue(virtualEnv)
		}

		tflog.Info(ctx, fmt.Sprintf("python path set to %s", data.PythonPath.ValueString()))

		pytestCurrent := os.Getenv("PYTEST_CURRENT_TEST")
		tflog.Info(ctx, fmt.Sprintf("pytest current is %s", pytestCurrent))

		// only install the pypi package if not in e2e scenario (in this case its installed via pip -e .
		if pytestCurrent == "" {
			tflog.Info(ctx, fmt.Sprintf("installing rpyc-pve-cloud==%s", p.version))

			// package will be published to pypi with same version tag as provider
			pipCmd := exec.Command(fmt.Sprintf("%s/bin/pip", data.PythonPath.ValueString()), "install", fmt.Sprintf("rpyc-pve-cloud==%s", p.version))

			output, err := pipCmd.CombinedOutput()
			if err != nil {
				resp.Diagnostics.AddError(
					fmt.Sprintf("Command failed with error: %v", err),
					string(output),
				)
				return
			}
		}

		// start pyhon grpc server as daemon
		cmd := exec.Command(fmt.Sprintf("%s/bin/pcrpc", data.PythonPath.ValueString()))
		tflog.Info(ctx, fmt.Sprintf("started %s/bin/pcrpc", data.PythonPath.ValueString()))

		if err := cmd.Start(); err != nil {
			resp.Diagnostics.AddError(
				"Failed to start Python backend",
				err.Error(),
			)
			return
		}
	}

	// wait for it to be up
	deadline := time.Now().Add(10 * time.Second)

	for {
		if time.Now().After(deadline) {
			resp.Diagnostics.AddError(
				"Failed to start Python backend",
				"Deadline exceeded",
			)
			return
		}

		// try connect via grpc and health check
		conn, err := grpc.NewClient(
			"localhost:50052",
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			select {
				case <-ctx.Done():
					return // return should context get cancelled
				case <-time.After(200 * time.Millisecond):
					tflog.Info(ctx, "pcrpc not yet up... retrying!")
					continue
			}
		}

		defer conn.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

	
		healthClient := pb.NewHealthClient(conn)

		hresp, err := healthClient.Check(ctx, &pb.HealthCheckRequest{})
		if err != nil {
			resp.Diagnostics.AddError(
				"Grpc health check failed, this shouldnt happen!",
				err.Error(),
			)
			return
		}

		if hresp.Status != pb.HealthCheckResponse_SERVING {
			select {
				case <-ctx.Done():
					return // return should context get cancelled
				case <-time.After(200 * time.Millisecond):
					tflog.Info(ctx, "pcrpc not yet serving... retrying!")
					continue
			}
		}

		break // its up and running
	}

}

func (p *PxcProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
	}
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
	return []func() function.Function{
	}
}

func (p *PxcProvider) Actions(ctx context.Context) []func() action.Action {
	return []func() action.Action{
	}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &PxcProvider{
			version: version,
		}
	}
}
