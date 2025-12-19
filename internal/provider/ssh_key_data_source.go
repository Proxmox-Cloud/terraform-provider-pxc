// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	 "github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"

	"time"

	pb "github.com/Proxmox-Cloud/terraform-provider-proxmox-cloud/internal/provider/protos"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &SshKeyDataSource{}

func NewSshKeyDataSource() datasource.DataSource {
	return &SshKeyDataSource{}
}

// SshKeyDataSource defines the data source implementation.
type SshKeyDataSource struct {
	providerModel PxcProviderModel
}

// SshKeyDataSourceModel describes the data source data model.
type SshKeyDataSourceModel struct {
	KeyType types.String `tfsdk:"key_type"`
	Key types.String `tfsdk:"key"`
}

func (d *SshKeyDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (d *SshKeyDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetch different ssh keys from proxmox cloud based on type",

		Attributes: map[string]schema.Attribute{
			"key_type": schema.StringAttribute{
				Required: true,
				Validators: []validator.String{
					stringvalidator.OneOf("AUTOMATION", "PVE_HOST_RSA"),
				},
				MarkdownDescription: "Specified key type enum",
			},
			"key": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The catted key",
			},
		},
	}
}

func (d *SshKeyDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *SshKeyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data SshKeyDataSourceModel

	// Read Terraform configuration data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// convert datasource arg to keytype
	keyTypeInt, ok := pb.GetSshKeyRequest_KeyType_value[data.KeyType.ValueString()]
	if !ok {
		resp.Diagnostics.AddError("Unknown key", fmt.Sprintf("unknown key type: %s", data.KeyType.ValueString()))
		return
	}

	// init rpc client
	conn, err := grpc.NewClient(
		"localhost:50052",
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
	cresp, err := client.GetSshKey(ctx, &pb.GetSshKeyRequest{TargetPve: d.providerModel.TargetPve.ValueString(), KeyType: pb.GetSshKeyRequest_KeyType(keyTypeInt)})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read example, got error: %s", err))
		return
	}

	data.Key = types.StringValue(cresp.Key)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
