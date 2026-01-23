package provider

import (
	"context"
	"fmt"

	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &CephAccessDataSource{}

func NewCephAccessDataSource() datasource.DataSource {
	return &CephAccessDataSource{}
}

// CephAccessDataSource defines the data source implementation.
type CephAccessDataSource struct {
	cloudInventory CloudInventory
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
		MarkdownDescription: "Fetches ceph conf and the admin keyring of the associated target_pve from the kubespray inventory" +
			"file passed to the provider during init.",
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

	cloudInv, ok := req.ProviderData.(CloudInventory)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *KubesprayInventory, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	d.cloudInventory = cloudInv
}

func (d *CephAccessDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data CephAccessDataSourceModel

	// Read Terraform configuration data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := GetCloudRpcService(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to init client, got error: %s", err))
		return
	}

	// perform the request
	cresp, err := client.GetCephAccess(ctx, &pb.GetCephAccessRequest{TargetPve: d.cloudInventory.TargetPve})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable get ceph access files, got error: %s", err))
		return
	}

	data.CephConf = types.StringValue(cresp.CephConf)
	data.AdminKeyring = types.StringValue(cresp.AdminKeyring)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
