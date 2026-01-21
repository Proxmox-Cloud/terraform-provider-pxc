package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"time"

	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &CloudVmsDataSource{}

func NewCloudVmsDataSource() datasource.DataSource {
	return &CloudVmsDataSource{}
}

// CloudVmsDataSource defines the data source implementation.
type CloudVmsDataSource struct {
	kubesprayInventory KubesprayInventory
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

func (d *CloudVmsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data CloudVmsDataSourceModel

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
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to init grpc client, got error: %s", err))
		return
	}
	defer conn.Close()

	client := pb.NewCloudServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	getArgs := map[string]string{
		"--type": "vm",
	}

	// fetch the vms
	cresp, err := client.GetProxmoxApi(ctx, &pb.GetProxmoxApiRequest{TargetPve: d.kubesprayInventory.TargetPve, ApiPath: "/cluster/resources", GetArgs: getArgs})
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
	vcresp, err := client.GetVmVarsBlake(ctx, &pb.GetVmVarsBlakeRequest{BlakeIds: blakeIds, TargetPve: d.kubesprayInventory.TargetPve})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable make request for vm vars, got error: %s", err))
		return
	}

	// iterate again and add vars
	for _, machine := range machines {
		machine["niggervar"] = "fag"
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
