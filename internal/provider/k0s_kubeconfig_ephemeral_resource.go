package provider

import (
	"context"
	"fmt"

	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ ephemeral.EphemeralResource = &K0sKubeconfigEphemeralResource{}

func NewK0sKubeconfigEphemeralResource() ephemeral.EphemeralResource {
	return &K0sKubeconfigEphemeralResource{}
}

// K0sKubeconfigEphemeralResource defines the ephemeral resource implementation.
type K0sKubeconfigEphemeralResource struct {
	cloudInventory CloudInventory
}

// K0sKubeconfigEphemeralResourceModel describes the ephemeral resource data model.
type K0sKubeconfigEphemeralResourceModel struct {
	Config types.String `tfsdk:"config"`
}

func (r *K0sKubeconfigEphemeralResource) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_k0s_kubeconfig"
}

func (r *K0sKubeconfigEphemeralResource) Schema(ctx context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get the admin kubeconfig for authenticating k8s related providers. Target kubernetes cluster is automatically inferred from the provider initialization.",

		Attributes: map[string]schema.Attribute{
			"config": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Kubeconfig",
			},
		},
	}
}

func (r *K0sKubeconfigEphemeralResource) Configure(ctx context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	// Always perform a nil check when handling ProviderData because Terraform
	// sets that data after it calls the ConfigureProvider RPC.
	if req.ProviderData == nil {
		return
	}

	cloudInv, ok := req.ProviderData.(CloudInventory)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected CloudInventory, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	if cloudInv.ExternalHostsInventory == nil {
		resp.Diagnostics.AddError(
			"Unsupported with provider config",
			"The use of this resource requires initialization with a pxc.cloud.ext_hosts_inv invetory file.",
		)
		return
	}

	// here we check if the typed k0s edge hostgroup is present
	_, ok = cloudInv.ExternalHostsInventory.TypedHostGroups["k0s_edge"]
	if !ok {
		resp.Diagnostics.AddError(
			"Unsupported with provider config",
			"The use of this resource requires external hosts inventory to have the typed k0s_edge hostgroup set.",
		)
		return
	}

	r.cloudInventory = cloudInv
}

func (r *K0sKubeconfigEphemeralResource) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var data K0sKubeconfigEphemeralResourceModel

	// Read Terraform config data into the model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	client, err := GetCloudRpcService(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to init client, got error: %s", err))
		return
	}

	// first we validate if the custom ext hosts inventory file actually has a k0s_single host defined
	ansibleHost, ok := r.cloudInventory.ExternalHostsInventory.TypedHostGroups["k0s_edge"]["k0s_single"]["ansible_host"].(string)
	if !ok {
		resp.Diagnostics.AddError("Inventory Error", fmt.Sprintf("Unable to cast ansible_host for k0s_single, got error: %s", err))
		return
	}

	ansibleUser, ok := r.cloudInventory.ExternalHostsInventory.TypedHostGroups["k0s_edge"]["k0s_single"]["ansible_user"].(string)
	if !ok {
		resp.Diagnostics.AddError("Inventory Error", fmt.Sprintf("Unable to cast ansible_user for k0s_single, got error: %s", err))
		return
	}

	// perform the request
	cresp, err := client.GetK0SKubeconfig(ctx, &pb.GetK0SKubeconfigRequest{K0SHost: ansibleHost, K0SUser: ansibleUser})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get rpc k0s kubeconfig, got error: %s", err))
		return
	}

	data.Config = types.StringValue(cresp.Config)

	// Save data into ephemeral result data
	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}
