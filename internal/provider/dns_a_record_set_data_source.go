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
var _ datasource.DataSource = &DnsARecordSetSource{}

func NewDnsARecordSetSource() datasource.DataSource {
	return &DnsARecordSetSource{}
}

// DnsARecordSetSource defines the data source implementation.
type DnsARecordSetSource struct {
	cloudInventory CloudInventory
}

// DnsARecordSetSourceModel describes the data source data model.
type DnsARecordSetSourceModel struct {
	Host  types.String `tfsdk:"host"`
	Addrs types.List   `tfsdk:"addrs"`
}

func (d *DnsARecordSetSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_a_record_set"
}

func (d *DnsARecordSetSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches dns a records for a specified host from the cloud internals bind dns server. Works just like hashicorp/dns provider.",
		Attributes: map[string]schema.Attribute{
			"host": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The host to fetch records for.",
			},
			"addrs": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "List of IP addresses.",
			},
		},
	}
}

func (d *DnsARecordSetSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	cloudInv, ok := req.ProviderData.(CloudInventory)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected CloudInventory, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	d.cloudInventory = cloudInv
}

func (d *DnsARecordSetSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data DnsARecordSetSourceModel

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
	cresp, err := client.GetDnsARecordSet(ctx, &pb.GetDnsARecordSetRequest{TargetPve: d.cloudInventory.TargetPve, Host: data.Host.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable get a record set, got error: %s", err))
		return
	}

	addrs, diags := types.ListValueFrom(ctx, types.StringType, cresp.Addrs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// set the return of grpc
	data.Addrs = addrs

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
