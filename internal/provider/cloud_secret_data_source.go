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
var _ datasource.DataSource = &CloudSecretDataSource{}

func NewCloudSecretDataSource() datasource.DataSource {
	return &CloudSecretDataSource{}
}

// CloudSecretDataSource defines the data source implementation.
type CloudSecretDataSource struct {
	cloudInventory CloudInventory
}

// CloudSecretDataSourceModel describes the data source data model.
type CloudSecretDataSourceModel struct {
	SecretName types.String `tfsdk:"secret_name"`
	SecretData types.String `tfsdk:"secret_data"`
}

func (d *CloudSecretDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cloud_secret"
}

func (d *CloudSecretDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a proxmox cloud secret, scoped by target_pve, from the postgres px_cloud_secret table.",

		Attributes: map[string]schema.Attribute{
			"secret_name": schema.StringAttribute{
				MarkdownDescription: "Secret name to fetch.",
				Required:            true,
			},
			// todo: figure out terraforms absurd type system to avoid jsonencode and decode calls to pass / receive dynamic values
			"secret_data": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Secret data as json string, parsed from jsonb inside postgres database. Use jsondecode to access it as dynamic terraform object.",
			},
		},
	}
}

func (d *CloudSecretDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *CloudSecretDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data CloudSecretDataSourceModel

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

	cresp, err := client.GetCloudSecret(ctx, &pb.GetCloudSecretRequest{TargetPve: d.cloudInventory.TargetPve, SecretName: data.SecretName.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get cloud secret, got error: %s", err))
		return
	}

	data.SecretData = types.StringValue(cresp.Secret)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
