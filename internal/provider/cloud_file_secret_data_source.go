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
var _ datasource.DataSource = &CloudFileSecretDataSource{}

func NewCloudFileSecretDataSource() datasource.DataSource {
	return &CloudFileSecretDataSource{}
}

// CloudFileSecretDataSource defines the data source implementation.
type CloudFileSecretDataSource struct {
	kubesprayInventory KubesprayInventory
}

// CloudFileSecretDataSourceModel describes the data source data model.
type CloudFileSecretDataSourceModel struct {
	SecretName types.String `tfsdk:"secret_name"`
	Secret     types.String `tfsdk:"secret"`
	Rstrip     types.Bool   `tfsdk:"rstrip"`
}

func (d *CloudFileSecretDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cloud_file_secret"
}

func (d *CloudFileSecretDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a file secret from the proxmox cloud secret directory (/etc/pve/cloud/secrets).",

		Attributes: map[string]schema.Attribute{
			"secret_name": schema.StringAttribute{
				MarkdownDescription: "Secret file name to fetch.",
				Required:            true,
			},
			"secret": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Cat output of raw secret file.",
			},
			"rstrip": schema.BoolAttribute{
				MarkdownDescription: "Wheter to rstrip the secret, removing whitespace and newlines, if not specified defaults to true.",
				Optional:            true,
			},
		},
	}
}

func (d *CloudFileSecretDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	kubesprayInv, ok := req.ProviderData.(KubesprayInventory)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *KubesprayInventory, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	d.kubesprayInventory = kubesprayInv
}

func (d *CloudFileSecretDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data CloudFileSecretDataSourceModel

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
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable init grpc client, got error: %s", err))
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

	cresp, err := client.GetCloudFileSecret(ctx, &pb.GetCloudFileSecretRequest{TargetPve: d.kubesprayInventory.TargetPve, SecretName: data.SecretName.ValueString(), Rstrip: rstrip})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get cloud file secret, got error: %s", err))
		return
	}

	data.Secret = types.StringValue(cresp.Secret)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
