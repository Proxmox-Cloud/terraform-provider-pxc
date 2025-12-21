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
	"google.golang.org/grpc/credentials/insecure"

	"os"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &CephAccessDataSource{}

func NewCephAccessDataSource() datasource.DataSource {
	return &CephAccessDataSource{}
}

// CephAccessDataSource defines the data source implementation.
type CephAccessDataSource struct {
	providerModel PxcProviderModel
}

// CephAccessDataSourceModel describes the data source data model.
type CephAccessDataSourceModel struct {
	CephConf     types.String `tfsdk:"ceph_conf"`
	AdminKeyring types.String `tfsdk:"admin_keyring"`
}

func (d *CephAccessDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ceph_access"
}

func (d *CephAccessDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches the cluster vars of the associated target pve",

		Attributes: map[string]schema.Attribute{
			"ceph_conf": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ceph.conf file from /etc/ceph/",
			},
			"admin_keyring": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ceph.client.admin.keyring file from /etc/pve/priv/",
			},
		},
	}
}

func (d *CephAccessDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *CephAccessDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data CephAccessDataSourceModel

	// Read Terraform configuration data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// init rpc client
	tflog.Info(ctx, fmt.Sprintf("Connecting to unix:///tmp/pc-rpc-%d.sock", os.Getpid()))
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
	cresp, err := client.GetCephAccess(ctx, &pb.GetCephAccessRequest{TargetPve: d.providerModel.TargetPve.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read example, got error: %s", err))
		return
	}

	data.CephConf = types.StringValue(cresp.CephConf)
	data.AdminKeyring = types.StringValue(cresp.AdminKeyring)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
