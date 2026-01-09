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
var _ datasource.DataSource = &PveApiGetDataSource{}

func NewPveApiGetDataSource() datasource.DataSource {
	return &PveApiGetDataSource{}
}

// PveApiGetDataSource defines the data source implementation.
type PveApiGetDataSource struct {
	providerModel PxcProviderModel
}

// PveApiGetDataSourceModel describes the data source data model.
type PveApiGetDataSourceModel struct {
	ApiPath   types.String `tfsdk:"api_path"`
	GetArgs   types.Map    `tfsdk:"get_args"`
	JsonResp  types.String `tfsdk:"json_resp"`
	TargetPve types.String `tfsdk:"target_pve"`
}

func (d *PveApiGetDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_pve_api_get"
}

func (d *PveApiGetDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Makes a proxmox api get request via pvesh cli tool",

		Attributes: map[string]schema.Attribute{
			"api_path": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Cluster vars as yaml string",
			},
			"get_args": schema.MapAttribute{
				ElementType: types.StringType,
				Optional:    true,
			},
			"target_pve": schema.StringAttribute{
				Optional: true,
			},
			"json_resp": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Proxmox api response in json --output format",
			},
		},
	}
}

func (d *PveApiGetDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *PveApiGetDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data PveApiGetDataSourceModel

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

	// convert tf map to go map
	getArgs := make(map[string]string)
	if !data.GetArgs.IsNull() {
		for k, v := range data.GetArgs.Elements() {
			strVal := v.(types.String)
			getArgs[k] = strVal.ValueString()
		}
	}

	// user might specify other target pve than the initialized providr
	// for cross cluster api calls
	targetPve := d.providerModel.TargetPve.ValueString()
	if !data.TargetPve.IsNull() {
		targetPve = data.TargetPve.ValueString()
	}

	// perform the request
	cresp, err := client.GetProxmoxApi(ctx, &pb.GetProxmoxApiRequest{TargetPve: targetPve, ApiPath: data.ApiPath.ValueString(), GetArgs: getArgs})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read example, got error: %s", err))
		return
	}

	data.JsonResp = types.StringValue(cresp.JsonResp)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
