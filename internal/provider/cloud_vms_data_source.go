package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &CloudVmsDataSource{}

func NewCloudVmsDataSource() datasource.DataSource {
	return &CloudVmsDataSource{}
}

// CloudVmsDataSource defines the data source implementation.
type CloudVmsDataSource struct {
	cloudInventory CloudInventory
}

// CloudVmsDataSourceModel describes the data source data model.
type CloudVmsDataSourceModel struct {
	CloudVmsJson types.String `tfsdk:"vms_json"`
}

func (d *CloudVmsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cloud_vms"
}

func (d *CloudVmsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Returns all proxmox cloud vms on the current target_pve (proxmox cluster).",

		Attributes: map[string]schema.Attribute{
			// todo: figure out terraforms absurd type system to avoid jsonencode and decode calls to pass / receive dynamic values
			"vms_json": schema.StringAttribute{
				MarkdownDescription: "Json list of cloud vm instances. Contains pvesh /cluster/resources output + merged in vm_vars based on blake ids.",
				Computed:            true,
			},
		},
	}
}

func (d *CloudVmsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *CloudVmsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data CloudVmsDataSourceModel

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

	// fetch the vms
	cresp, err := client.GetProxmoxApi(ctx, &pb.GetProxmoxApiRequest{TargetPve: d.cloudInventory.TargetPve,
		ApiPath: "/cluster/resources", GetArgs: map[string]string{"--type": "vm"}})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable make get api request, got error: %s", err))
		return
	}

	var machines []map[string]interface{}

	err = json.Unmarshal([]byte(cresp.JsonResp), &machines)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to unmarschal pve resp, got error: %s", err))
		return
	}

	// extract blake ids for fetch call
	var blakeIds []string
	for _, machine := range machines {
		if val, ok := machine["tags"]; ok {
			if tagStr, isString := val.(string); isString {

				tags := strings.Split(tagStr, ";")

				for _, tag := range tags {
					if strings.HasSuffix(tag, "-blake") {
						blakeIds = append(blakeIds, strings.TrimSuffix(tag, "-blake"))
						break
					}
				}
			}
		}
	}

	vcresp, err := client.GetVmVarsBlake(ctx, &pb.GetVmVarsBlakeRequest{BlakeIds: blakeIds, TargetPve: d.cloudInventory.TargetPve, CloudDomain: d.cloudInventory.CloudDomain})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable make request for vm vars, got error: %s", err))
		return
	}

	// iterate again and add vars
	for _, machine := range machines {
		if val, ok := machine["tags"]; ok {
			if tagStr, isString := val.(string); isString {

				tags := strings.Split(tagStr, ";")

				for _, tag := range tags {
					if strings.HasSuffix(tag, "-blake") {
						// found blake id
						if vmVars, ok := vcresp.BlakeIdVars[strings.TrimSuffix(tag, "-blake")]; ok {
							// found vm vars => decode json and inject
							decoder := json.NewDecoder(strings.NewReader(vmVars))

							var blakeVars map[string]interface{}
							decoder.Decode(&blakeVars)
							machine["blake_vars"] = blakeVars
						}
						break
					}
				}
			}
		}
	}
	mBytes, err := json.Marshal(machines)
	if err != nil {
		resp.Diagnostics.AddError("Marshal error", fmt.Sprintf("Error marshalling modified vms pve api response back into json, got error: %s", err))
		return
	}

	data.CloudVmsJson = types.StringValue(string(mBytes))

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
