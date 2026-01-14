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
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &CloudSecretDataSource{}

func NewCloudSecretDataSource() datasource.DataSource {
	return &CloudSecretDataSource{}
}

// CloudSecretDataSource defines the data source implementation.
type CloudSecretDataSource struct {
	providerModel PxcProviderModel
}

// CloudSecretDataSourceModel describes the data source data model.
type CloudSecretDataSourceModel struct {
	SecretName types.String `tfsdk:"secret_name"`
	Secret     types.String `tfsdk:"secret"`
	Rstrip 		 types.Bool `tfsdk:"rstrip"`
}

func (d *CloudSecretDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cloud_secret"
}

func (d *CloudSecretDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches secret from the proxmox cloud secret directory (/etc/pve/cloud/secrets).",

		Attributes: map[string]schema.Attribute{
			"secret_name": schema.StringAttribute{
				MarkdownDescription: "Secret file name to fetch",
				Required:            true,
			},
			"secret": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Cattet raw secret file",
			},
			"rstrip": schema.BoolAttribute{
				MarkdownDescription: "Wheter to rstrip the secret, if not specified defaults to true",
				Optional: true,
			},
		},
	}
}

func (d *CloudSecretDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *CloudSecretDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data CloudSecretDataSourceModel

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
	rstrip := true

	if !data.Rstrip.IsNull() {
		rstrip = data.Rstrip.ValueBool()
	}

	cresp, err := client.GetCloudSecret(ctx, &pb.GetCloudSecretRequest{TargetPve: d.providerModel.TargetPve.ValueString(), SecretName: data.SecretName.ValueString(), Rstrip: rstrip})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read example, got error: %s", err))
		return
	}

	data.Secret = types.StringValue(cresp.Secret)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
