// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"fmt"

	"time"

	pb "github.com/Proxmox-Cloud/terraform-provider-proxmox-cloud/internal/provider/protos"
	"google.golang.org/grpc"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ ephemeral.EphemeralResource = &KubeconfigEphemeralResource{}

func NewKubeconfigEphemeralResource() ephemeral.EphemeralResource {
	return &KubeconfigEphemeralResource{}
}

// KubeconfigEphemeralResource defines the ephemeral resource implementation.
type KubeconfigEphemeralResource struct {
	providerModel ScaffoldingProviderModel
}

// KubeconfigEphemeralResourceModel describes the ephemeral resource data model.
type KubeconfigEphemeralResourceModel struct {
	Config                 types.String `tfsdk:"config"`
}

func (r *KubeconfigEphemeralResource) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubeconfig"
}

func (r *KubeconfigEphemeralResource) Schema(ctx context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get the admin kubeconfig for authenticating k8s related providers.",

		Attributes: map[string]schema.Attribute{
			"config": schema.StringAttribute{
				Computed: true,
				Sensitive:           true,
				MarkdownDescription: "Kubeconfig",
			},
		},
	}
}

func (r *KubeconfigEphemeralResource) Configure(ctx context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
  // Always perform a nil check when handling ProviderData because Terraform
  // sets that data after it calls the ConfigureProvider RPC.
  if req.ProviderData == nil {
    return
  }

	providerModel, ok := req.ProviderData.(ScaffoldingProviderModel)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *ScaffoldingProviderModel, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	r.providerModel = providerModel
}


func (r *KubeconfigEphemeralResource) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var data KubeconfigEphemeralResourceModel

	// Read Terraform config data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
    conn, err := grpc.Dial("localhost:50051", grpc.WithInsecure())
    if err != nil {
        resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read example, got error: %s", err))
		return
    }
    defer conn.Close()

    client := pb.NewCloudServiceClient(conn)

    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    cresp, err := client.GetMasterKubeconfig(ctx, &pb.GetKubeconfigRequest{TargetPve: r.providerModel.TargetPve.ValueString(), StackName: r.providerModel.K8sStackName.ValueString()})
    if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read example, got error: %s", err))
		return
    }

	data.Config = types.StringValue(cresp.Config)

	// Save data into ephemeral result data
	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}
