// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"time"

	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &PveInventoryDataSource{}

func NewPveInventoryDataSource() datasource.DataSource {
	return &PveInventoryDataSource{}
}

// PveInventoryDataSource defines the data source implementation.
type PveInventoryDataSource struct {
	providerModel PxcProviderModel
}

// PveInventoryDataSourceModel describes the data source data model.
type PveInventoryDataSourceModel struct {
	Inventory   types.String `tfsdk:"inventory"`
	CloudDomain types.String `tfsdk:"cloud_domain"`
}

func (d *PveInventoryDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_pve_inventory"
}

func (d *PveInventoryDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches the pve inventory of the associated target pves cloud domain",

		Attributes: map[string]schema.Attribute{
			"inventory": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Pve inventory as yaml string",
			},
			"cloud_domain": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The overarching cloud domain of the inventory",
			},
		},
	}
}

func (d *PveInventoryDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	providerModel, ok := req.ProviderData.(PxcProviderModel)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *PxcProviderModel, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	d.providerModel = providerModel
}

func (d *PveInventoryDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data PveInventoryDataSourceModel

	// Read Terraform configuration data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// init rpc client
	conn, err := grpc.NewClient(
		fmt.Sprintf("unix:///tmp/pc-rpc-%d.sock", os.Getpid()),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read example, got error: %s", err))
		return
	}
	defer conn.Close()

	client := pb.NewCloudServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// perform the request
	cresp, err := client.GetPveInventory(ctx, &pb.GetPveInventoryRequest{TargetPve: d.providerModel.TargetPve.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read example, got error: %s", err))
		return
	}

	data.Inventory = types.StringValue(cresp.Inventory)
	data.CloudDomain = types.StringValue(cresp.CloudDomain)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
