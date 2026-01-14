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
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &ClusterVarsDataSource{}

func NewClusterVarsDataSource() datasource.DataSource {
	return &ClusterVarsDataSource{}
}

// ClusterVarsDataSource defines the data source implementation.
type ClusterVarsDataSource struct {
	providerModel PxcProviderModel
}

// ClusterVarsDataSourceModel describes the data source data model.
type ClusterVarsDataSourceModel struct {
	Vars types.String `tfsdk:"vars"`
}

func (d *ClusterVarsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cluster_vars"
}

func (d *ClusterVarsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches the cluster vars, set by ansible, of the associated target_pve. Check out the [cloud inventory schema](https://proxmox-cloud.github.io/pve_cloud/schemas/pve_cloud_inv_schema/) for available variables.",

		Attributes: map[string]schema.Attribute{
			"vars": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Cluster vars as yaml string, use `yamldecode()` to parse",
			},
		},
	}
}

func (d *ClusterVarsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ClusterVarsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ClusterVarsDataSourceModel

	// Read Terraform configuration data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// init rpc client
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
	cresp, err := client.GetClusterVars(ctx, &pb.GetClusterVarsRequest{TargetPve: d.providerModel.TargetPve.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read example, got error: %s", err))
		return
	}

	data.Vars = types.StringValue(cresp.Vars)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
