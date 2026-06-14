package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &VlSelectAuthDataSource{}

func NewVlSelectAuthDataSource() datasource.DataSource {
	return &VlSelectAuthDataSource{}
}

// VlSelectAuthDataSource defines the data source implementation.
type VlSelectAuthDataSource struct {
	cloudInventory CloudInventory
}

// VlSelectAuthDataSourceModel describes the data source data model.
type VlSelectAuthDataSourceModel struct {
	MCPeers	types.Set `tfsdk:"mc_peers"`
	MCToken	types.String `tfsdk:"mc_token"`

	VlSelectAuthPassword types.String `tfsdk:"vlselect_auth_password"`
}

func (d *VlSelectAuthDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vlselect_auth"
}

func (d *VlSelectAuthDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a proxmox cloud secret, scoped by target_pve, from the postgres px_cloud_secret table.",

		Attributes: map[string]schema.Attribute{
			"mc_peers": schema.SetAttribute{
				ElementType: types.StringType,
				MarkdownDescription: "List of peers to look for vlselect auth via their multi cloud gateway.",
				Optional:            true,
			},
			"mc_token": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Multi cloud token to query the other peers.",
			},
			"vlselect_auth_password": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The fetched password, either from local or from multi cloud peers.",
			},
		},
	}
}

func (d *VlSelectAuthDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
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

	d.cloudInventory = cloudInv
}

type VlSelectAuthSecret struct {
	Password   string     `json:"password"`
}

type MCGatewayVlSelectResponse struct {
	AuthPresent bool `json:"auth_present"`
	VlSelectAccess *VlSelectAuthSecret `json:"vlselect_auth"`
}

func (d *VlSelectAuthDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data VlSelectAuthDataSourceModel

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

	// first we look for the cloud internals vlauth secret => a vlselect that is deployed in the cloud takes precendence
	cresp, err := client.GetCloudSecret(ctx, &pb.GetCloudSecretRequest{
		CloudDomain: d.cloudInventory.CloudDomain, 
		TargetPve: d.cloudInventory.TargetPve, 
		SecretName: fmt.Sprintf("%s-vlogs-storage-node", d.cloudInventory.CloudDomain),
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get cloud secret, got error: %s", err))
		return
	}

	if cresp.Secret != "" {
		// secret is defined fill and return
		var ds VlSelectAuthSecret
		err := json.Unmarshal([]byte(cresp.Secret), &ds)
		if err != nil {
			resp.Diagnostics.AddError("Json error", fmt.Sprintf("Unable unmarshal auth secret, got error: %s", err))
			return
		}
		
		data.VlSelectAuthPassword = types.StringValue(ds.Password)

		// Save data into Terraform state
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}

	// no cloud secret defined, if peers are set we query them else we error
	if !data.MCPeers.IsNull() && !data.MCToken.IsNull() {
		client := &http.Client{
			Timeout: 10 * time.Second,
		}

		var peers []string
		d := data.MCPeers.ElementsAs(ctx, &peers, false)
		resp.Diagnostics.Append(d...)
		if resp.Diagnostics.HasError() {
			return
		}
		
		var VlSelectAuthSecrets []VlSelectAuthSecret

		// now we query all the peers for a vlselect auth
		for _, peer := range peers {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/get-vlselect-auth", peer), nil)
			if err != nil {
				resp.Diagnostics.AddWarning(
					"Failed to create HTTP Request",
					fmt.Sprintf("Error creating request for peer %s: %s", peer, err.Error()),
				)
				continue
			}

			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", data.MCToken.ValueString()))
			req.Header.Set("Accept", "application/json")

			response, err := client.Do(req)
			if err != nil {
				resp.Diagnostics.AddWarning(
					"HTTP Request Failed",
					fmt.Sprintf("Failed to fetch data from peer %s: %s", peer, err.Error()),
				)
				continue
			}
			defer response.Body.Close()

			body, err := io.ReadAll(response.Body)
			if err != nil {
				resp.Diagnostics.AddError(
					"Failed to read response body",
					fmt.Sprintf("Error reading from peer %s: %s", peer, err.Error()),
				)
				return
			}

			var authResult MCGatewayVlSelectResponse
			if err := json.Unmarshal(body, &authResult); err != nil {
				resp.Diagnostics.AddError(
					"Failed to unmarshal JSON",
					fmt.Sprintf("Error parsing JSON from peer %s: %s", peer, err.Error()),
				)
				return
			}

			if authResult.AuthPresent && authResult.VlSelectAccess != nil {
				VlSelectAuthSecrets = append(VlSelectAuthSecrets, *authResult.VlSelectAccess)
			}
		}
		
		if len(VlSelectAuthSecrets) == 1 {
			// found exactly one master as it should be
			data.VlSelectAuthPassword = types.StringValue(VlSelectAuthSecrets[0].Password)

			// Save data into Terraform state
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			return
		}else if len(VlSelectAuthSecrets) == 0 {
			resp.Diagnostics.AddError("Config Error", "Multi cloud peers and token defined but no vlselect auth found among them!")
			return
		}else if len(VlSelectAuthSecrets) > 1 {
			resp.Diagnostics.AddError("Config Error", "Multi cloud peers and token defined but more than one vlselect auth found among them!")
			return
		}
	}

	resp.Diagnostics.AddError("Config Error", "No vlselect auth secret found and no multi cloud peers / tokens defined in params!")
	
}
