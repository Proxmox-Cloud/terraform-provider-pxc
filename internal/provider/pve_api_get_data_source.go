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
var _ datasource.DataSource = &PveApiGetDataSource{}

func NewPveApiGetDataSource() datasource.DataSource {
	return &PveApiGetDataSource{}
}

// PveApiGetDataSource defines the data source implementation.
type PveApiGetDataSource struct {
	cloudInventory CloudInventory
}

// PveApiGetDataSourceModel describes the data source data model.
type PveApiGetDataSourceModel struct {
	ApiPath  types.String `tfsdk:"api_path"`
	GetArgs  types.Map    `tfsdk:"get_args"`
	JsonResp types.String `tfsdk:"json_resp"`
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
				MarkdownDescription: "Api path that is inserted after pvesh get ...",
			},
			"get_args": schema.MapAttribute{
				ElementType:         types.StringType,
				MarkdownDescription: "CLI args that are inserted after the api_path",
				Optional:            true,
			},
			"target_pve": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Target proxmox cluster that is used to execute the command. Defaults to what the pxc provider was initialized with.",
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

func (d *PveApiGetDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data PveApiGetDataSourceModel

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

	// convert tf map to go map
	// todo: use ElementsAs ?
	getArgs := make(map[string]string)
	if !data.GetArgs.IsNull() {
		for k, v := range data.GetArgs.Elements() {
			strVal := v.(types.String)
			getArgs[k] = strVal.ValueString()
		}
	}

	// perform the request
	cresp, err := client.GetProxmoxApi(ctx, &pb.GetProxmoxApiRequest{TargetPve: d.cloudInventory.TargetPve, ApiPath: data.ApiPath.ValueString(), GetArgs: getArgs})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable make get api request, got error: %s", err))
		return
	}

	data.JsonResp = types.StringValue(cresp.JsonResp)

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
