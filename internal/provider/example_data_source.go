// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"	

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"time"

	pb "github.com/Proxmox-Cloud/terraform-provider-proxmox-cloud/internal/provider/protos"
	"google.golang.org/grpc"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &ExampleDataSource{}

func NewExampleDataSource() datasource.DataSource {
	return &ExampleDataSource{}
}

// ExampleDataSource defines the data source implementation.
type ExampleDataSource struct {
	providerModel ScaffoldingProviderModel
}

// ExampleDataSourceModel describes the data source data model.
type ExampleDataSourceModel struct {
	Config                 types.String `tfsdk:"config"`
}

func (d *ExampleDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_example"
}

func (d *ExampleDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		// This description is used by the documentation generator and the language server.
		MarkdownDescription: "Example data source",

		Attributes: map[string]schema.Attribute{
			"config": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Kubeconfig",
			},
		},
	}
}

func (d *ExampleDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
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

	d.providerModel = providerModel
}

func (d *ExampleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ExampleDataSourceModel

	// Read Terraform configuration data into the model
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

    cresp, err := client.GetMasterKubeconfig(ctx, &pb.GetKubeconfigRequest{TargetPve: d.providerModel.TargetPve.ValueString(), StackName: d.providerModel.K8sStackName.ValueString()})
    if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read example, got error: %s", err))
		return
    }

	data.Config = types.StringValue(cresp.Config)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
