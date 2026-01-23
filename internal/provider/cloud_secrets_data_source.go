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
var _ datasource.DataSource = &CloudSecretsDataSource{}

func NewCloudSecretsDataSource() datasource.DataSource {
	return &CloudSecretsDataSource{}
}

// CloudSecretsDataSource defines the data source implementation.
type CloudSecretsDataSource struct {
	cloudInventory CloudInventory
}

// CloudSecretsDataSourceModel describes the data source data model.
type CloudSecretsDataSourceModel struct {
	SecretType  types.String `tfsdk:"secret_type"`
	SecretsData types.String `tfsdk:"secrets_data"`
}

func (d *CloudSecretsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cloud_secrets"
}

func (d *CloudSecretsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a proxmox cloud secrets based on their type, scoped by target_pve, from the postgres px_cloud_secret table.",

		Attributes: map[string]schema.Attribute{
			"secret_type": schema.StringAttribute{
				MarkdownDescription: "Secrets of type to fetch.",
				Required:            true,
			},
			// todo: figure out terraforms absurd type system to avoid jsonencode and decode calls to pass / receive dynamic values
			"secrets_data": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Secrets data as json string, parsed from jsonb inside postgres database. Use jsondecode to access it as dynamic terraform object.",
			},
		},
	}
}

func (d *CloudSecretsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *CloudSecretsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data CloudSecretsDataSourceModel

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

	cresp, err := client.GetCloudSecrets(ctx, &pb.GetCloudSecretsRequest{TargetPve: d.cloudInventory.TargetPve, SecretType: data.SecretType.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get cloud secret, got error: %s", err))
		return
	}

	data.SecretsData = types.StringValue(cresp.Secrets)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
