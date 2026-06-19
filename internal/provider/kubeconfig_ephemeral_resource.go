package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"golang.org/x/sync/errgroup"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ ephemeral.EphemeralResource = &KubeconfigEphemeralResource{}

func NewKubeconfigEphemeralResource() ephemeral.EphemeralResource {
	return &KubeconfigEphemeralResource{}
}

// KubeconfigEphemeralResource defines the ephemeral resource implementation.
type KubeconfigEphemeralResource struct {
	cloudInventory CloudInventory
}

// KubeconfigEphemeralResourceModel describes the ephemeral resource data model.
type KubeconfigEphemeralResourceModel struct {
	Config types.String `tfsdk:"config"`
	Registries types.String `tfsdk:"registries"`
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
			"registries": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "Mirror registry if discovered.",
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

	cloudInv, ok := req.ProviderData.(CloudInventory)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected CloudInventory, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	if cloudInv.KubesprayInventory == nil {
		resp.Diagnostics.AddError(
			"Unsupported with provider config",
			"The use of this resource requires initialization with a pxc.cloud.kubespray_inv invetory file.",
		)
		return
	}

	r.cloudInventory = cloudInv
}

func (r *KubeconfigEphemeralResource) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	var data KubeconfigEphemeralResourceModel

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

	g, groupCtx := errgroup.WithContext(ctx)

	// launch fetch kubeconfig and get mirror registry discovery in paralell routines
	g.Go(func() error {
		// perform the request
		cresp, err := client.GetMasterKubeconfig(groupCtx, &pb.GetKubeconfigRequest{TargetPve: r.cloudInventory.TargetPve, StackName: r.cloudInventory.StackName, ExtraControlPlaneSans: r.cloudInventory.KubesprayInventory.ExtraControlPlaneSans})
		if err != nil {
			return fmt.Errorf("Unable to get kubeconfig, got error: %s", err)
		}

		data.Config = types.StringValue(cresp.Config)

		return nil
	})

	// launch get registry
	g.Go(func() error {

		csresp, err := client.GetCloudSecrets(groupCtx, &pb.GetCloudSecretsRequest{CloudDomain: r.cloudInventory.CloudDomain, TargetPve: r.cloudInventory.TargetPve, SecretType: "harbor-admin-auth"})
		if err != nil {
			return fmt.Errorf("Unable to get mirror secrets, got error: %s", err)
		}

		// decode the secrets if any
		var mirrorCredsMap map[string]HarborMirrorCreds

		err = json.Unmarshal([]byte(csresp.Secrets), &mirrorCredsMap)
		if err != nil {
			return fmt.Errorf("Unable to unmarshal secrets response, got error: %s", err)
		}

		e2eHarborHost := os.Getenv("E2E_HARBOR_MIRROR_HOST")
		if e2eHarborHost == "" && len(mirrorCredsMap) > 1 {
			tflog.Warn(groupCtx, "More than one harbor mirror discovery secret found!")
		}

		if len(mirrorCredsMap) > 0 {
			var adminCreds HarborMirrorCreds
			for _, v := range mirrorCredsMap {
				if e2eHarborHost != "" {
					// return the one that matches the env var
					if e2eHarborHost == v.HarborHost {
						adminCreds = v
						break
					}
				}else {
					// simply return the first
					adminCreds = v
					break
				}

			}
			
			if adminCreds == (HarborMirrorCreds{}) {
				return fmt.Errorf("Could not find harbor mirror credentials in returned map, even though it has items!")
			}

			// serialize as array for compat
			adminCredsJson, err := json.Marshal([]HarborMirrorCreds{adminCreds})
			if err != nil {
				return fmt.Errorf("Unable to marshal admin mirror creds back to json, got error: %s", err)
			}

			tflog.Info(groupCtx, fmt.Sprintf("Found and serialized registry: %s", adminCredsJson))

			data.Registries = types.StringValue(string(adminCredsJson))

		} else {
			// otherwise we set it to an empty array for dynamic init compat
			data.Registries = types.StringValue("[]")
		}

		return nil
	})

	if err := g.Wait(); err != nil {
		resp.Diagnostics.AddError("Execution Error", err.Error())
		return
	}

	// Save data into ephemeral result data
	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}
