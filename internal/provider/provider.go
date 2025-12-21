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
	// "github.com/hashicorp/terraform-plugin-log/tflog"

	// "fmt"

	// "os"
	// "os/exec"

	// "os/signal"
	// "syscall"

	// "time"
	// "google.golang.org/grpc"
	// "google.golang.org/grpc/credentials/insecure"
	// pb "github.com/Proxmox-Cloud/terraform-provider-proxmox-cloud/internal/provider/protos"
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
