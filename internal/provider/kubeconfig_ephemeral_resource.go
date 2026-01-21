package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"fmt"

	"time"

	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ ephemeral.EphemeralResource = &KubeconfigEphemeralResource{}

func NewKubeconfigEphemeralResource() ephemeral.EphemeralResource {
	return &KubeconfigEphemeralResource{}
}

// KubeconfigEphemeralResource defines the ephemeral resource implementation.
type KubeconfigEphemeralResource struct {
	kubesprayInventory KubesprayInventory
}

// KubeconfigEphemeralResourceModel describes the ephemeral resource data model.
type KubeconfigEphemeralResourceModel struct {
	Config types.String `tfsdk:"config"`
}

func (r *KubeconfigEphemeralResource) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_kubeconfig"
}

func (r *KubeconfigEphemeralResource) Schema(ctx context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
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

func (r *KubeconfigEphemeralResource) Configure(ctx context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	// Always perform a nil check when handling ProviderData because Terraform
	// sets that data after it calls the ConfigureProvider RPC.
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

	r.kubesprayInventory = kubesprayInv
}

func (r *KubeconfigEphemeralResource) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var data KubeconfigEphemeralResourceModel

	// Read Terraform config data into the model
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

	// perform the request
	cresp, err := client.GetMasterKubeconfig(ctx, &pb.GetKubeconfigRequest{TargetPve: r.kubesprayInventory.TargetPve, StackName: r.kubesprayInventory.StackName})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get kubeconfig, got error: %s", err))
		return
	}

	data.Config = types.StringValue(cresp.Config)

	// Save data into ephemeral result data
	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}
