package provider

import (
	"context"
	"fmt"

	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"gopkg.in/yaml.v3"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &CloudSelfDataSource{}

func NewCloudSelfDataSource() datasource.DataSource {
	return &CloudSelfDataSource{}
}

// CloudSelfDataSource defines the data source implementation.
type CloudSelfDataSource struct {
	cloudInventory CloudInventory
}

// CloudSelfDataSourceModel describes the data source data model.
type CloudSelfDataSourceModel struct {
	ClusterVars        types.String `tfsdk:"cluster_vars"`
	TargetPve          types.String `tfsdk:"target_pve"`
	StackName          types.String `tfsdk:"stack_name"`
	ClusterCertEntries types.String `tfsdk:"cluster_cert_entries"`
	ExternalDomains    types.String `tfsdk:"external_domains"`
}

func (d *CloudSelfDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cloud_self"
}

// todo: turn into self resource with target_pve and stack name as fields for even more implicit models
func (d *CloudSelfDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Gives information about the cloud / provider instance in a reflective manner. Fetches the cluster vars, set by ansible, of the associated target_pve. Check out the [cloud inventory schema](https://proxmox-cloud.github.io/pve_cloud/schemas/pve_cloud_inv_schema/) for available variables.",

		Attributes: map[string]schema.Attribute{
			"cluster_vars": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Cluster vars as yaml string, use `yamldecode()` to parse",
			},
			"target_pve": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Target pve that was initially passed to the provider via kubespray inv.",
			},
			"stack_name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Stack name that was initially passed to the provider via kubespray inv.",
			},
			"cluster_cert_entries": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Cluster cert entries as yaml string as defined in the kubespray inv, use tf yamldecode() to parse.",
			},
			"external_domains": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Externally exposed domains as yaml string as defined in the kubespray inv, use tf yamldecode() to parse.",
			},
		},
	}
}

func (d *CloudSelfDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *CloudSelfDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data CloudSelfDataSourceModel

	// Read Terraform configuration data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// first check if the provider was initialized with a kubespray inventory
	if d.cloudInventory.KubesprayInventory == nil {
		resp.Diagnostics.AddError("Init Error", fmt.Sprintf("Currently this datasource only supports pxc.cloud.kubespray_inv inventories. Provider was initialized with %s", d.cloudInventory.Plugin))
		return
	}

	client, err := GetCloudRpcService(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to init client, got error: %s", err))
		return
	}

	// perform the request
	cresp, err := client.GetClusterVars(ctx, &pb.GetClusterVarsRequest{TargetPve: d.cloudInventory.TargetPve})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get cluster vars, got error: %s", err))
		return
	}

	data.ClusterVars = types.StringValue(cresp.Vars)

	// pass down
	data.StackName = types.StringValue(d.cloudInventory.StackName)
	data.TargetPve = types.StringValue(d.cloudInventory.TargetPve)

	// convert cluster cert entries and external domains to yaml string
	ceYamlBytes, err := yaml.Marshal(d.cloudInventory.KubesprayInventory.ClusterCertEntries)
	if err != nil {
		resp.Diagnostics.AddError(
			"YAML Marshalling Error",
			"Could not convert inventory struct to YAML: "+err.Error(),
		)
		return
	}

	data.ClusterCertEntries = types.StringValue(string(ceYamlBytes))

	edYamlBytes, err := yaml.Marshal(d.cloudInventory.KubesprayInventory.ExternalDomains)
	if err != nil {
		resp.Diagnostics.AddError(
			"YAML Marshalling Error",
			"Could not convert inventory struct to YAML: "+err.Error(),
		)
		return
	}

	data.ExternalDomains = types.StringValue(string(edYamlBytes))

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
