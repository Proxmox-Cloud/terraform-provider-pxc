package provider

import (
	"context"
	"fmt"

	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &SshKeyDataSource{}

func NewSshKeyDataSource() datasource.DataSource {
	return &SshKeyDataSource{}
}

// SshKeyDataSource defines the data source implementation.
type SshKeyDataSource struct {
	cloudInventory CloudInventory
}

// SshKeyDataSourceModel describes the data source data model.
type SshKeyDataSourceModel struct {
	KeyType types.String `tfsdk:"key_type"`
	Key     types.String `tfsdk:"key"`
}

func (d *SshKeyDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ssh_key"
}

func (d *SshKeyDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetch different ssh keys from proxmox cloud based on key type.",

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
				MarkdownDescription: "The raw key",
			},
		},
	}
}

func (d *SshKeyDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	client, err := GetCloudRpcService(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to init client, got error: %s", err))
		return
	}

	// perform the request
	cresp, err := client.GetSshKey(ctx, &pb.GetSshKeyRequest{TargetPve: d.cloudInventory.TargetPve, KeyType: pb.GetSshKeyRequest_KeyType(keyTypeInt)})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get ssh key, got error: %s", err))
		return
	}

	data.Key = types.StringValue(cresp.Key)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
