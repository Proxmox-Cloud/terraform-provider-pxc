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
var _ datasource.DataSource = &ProxmoxHostDataSource{}

func NewProxmoxHostDataSource() datasource.DataSource {
	return &ProxmoxHostDataSource{}
}

// ProxmoxHostDataSource defines the data source implementation.
type ProxmoxHostDataSource struct {
	cloudInventory CloudInventory
}

// ProxmoxHostDataSourceModel describes the data source data model.
type ProxmoxHostDataSourceModel struct {
	PveHost types.String `tfsdk:"pve_host"`
}

func (d *ProxmoxHostDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_pve_host"
}

func (d *ProxmoxHostDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a single online ipv4 host address of a proxmox host in target_pve. This can be used for apps that need to connect to a proxmox host directly.",

		Attributes: map[string]schema.Attribute{
			"pve_host": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Online pve host ip",
			},
		},
	}
}

func (d *ProxmoxHostDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ProxmoxHostDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ProxmoxHostDataSourceModel

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
	cresp, err := client.GetProxmoxHost(ctx, &pb.GetProxmoxHostRequest{TargetPve: d.cloudInventory.TargetPve})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get proxmox host, got error: %s", err))
		return
	}

	data.PveHost = types.StringValue(cresp.PveHost)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
