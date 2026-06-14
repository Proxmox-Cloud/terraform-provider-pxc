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
var _ datasource.DataSource = &GotifyMasterDataSource{}

func NewGotifyMasterDataSource() datasource.DataSource {
	return &GotifyMasterDataSource{}
}

// GotifyMasterDataSource defines the data source implementation.
type GotifyMasterDataSource struct {
	cloudInventory CloudInventory
}

// GotifyMasterDataSourceModel describes the data source data model.
type GotifyMasterDataSourceModel struct {
	MCPeers	types.Set `tfsdk:"mc_peers"`
	MCToken	types.String `tfsdk:"mc_token"`

	GotifyHost types.String `tfsdk:"gotify_host"`
	GotifyPassword types.String `tfsdk:"gotify_password"`
	GotifyCloudDomain types.String `tfsdk:"gotify_cloud_domain"`
}

func (d *GotifyMasterDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gotify_master"
}

func (d *GotifyMasterDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a proxmox cloud secret, scoped by target_pve, from the postgres px_cloud_secret table.",

		Attributes: map[string]schema.Attribute{
			"mc_peers": schema.SetAttribute{
				ElementType: types.StringType,
				MarkdownDescription: "List of peers to look for gotify master via their multi cloud gateway.",
				Optional:            true,
			},
			"mc_token": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Multi cloud token to query the other peers.",
			},
			"gotify_host": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "DNS Hostname of master gotify",
			},
			"gotify_password": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Password for the default admin user.",
				Sensitive: true,
			},
			"gotify_cloud_domain": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Cloud domain of the master, this is only different if fetched from a multi cloud peer.",
			},
		},
	}
}

func (d *GotifyMasterDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

type GotifyDiscoverySecret struct {
	Host  string   `json:"host"`
	Password   string     `json:"password"`
	CloudDomain string `json:"cloud_domain"`
}

type MCGatewayGotifyResponse struct {
	GotifyPresent bool `json:"gotify_present"`
	GotifyAccess *GotifyDiscoverySecret `json:"gotify_access"`
}

func (d *GotifyMasterDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data GotifyMasterDataSourceModel

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

	// first we look for the cloud internals gotify discovery secret => if a gotify is deployed in the cloud that takes precedence
	cresp, err := client.GetCloudSecret(ctx, &pb.GetCloudSecretRequest{CloudDomain: d.cloudInventory.CloudDomain, TargetPve: d.cloudInventory.TargetPve, SecretName: "gotify_admin_pw"})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get cloud secret, got error: %s", err))
		return
	}

	if cresp.Secret != "" {
		// secret is defined fill and return
		var ds GotifyDiscoverySecret
		err := json.Unmarshal([]byte(cresp.Secret), &ds)
		if err != nil {
			resp.Diagnostics.AddError("Json error", fmt.Sprintf("Unable unmarshal discovery secret, got error: %s", err))
			return
		}

		data.GotifyHost = types.StringValue(ds.Host)
		data.GotifyCloudDomain = types.StringValue(ds.CloudDomain)
		data.GotifyPassword = types.StringValue(ds.Password)

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
		
		var gotifyMasterSecrets []GotifyDiscoverySecret

		// now we query all the peers for a gotify master
		for _, peer := range peers {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/get-gotify-master", peer), nil)
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

			var gotifyResult MCGatewayGotifyResponse
			if err := json.Unmarshal(body, &gotifyResult); err != nil {
				resp.Diagnostics.AddError(
					"Failed to unmarshal JSON",
					fmt.Sprintf("Error parsing JSON from peer %s: %s", peer, err.Error()),
				)
				return
			}

			if gotifyResult.GotifyPresent && gotifyResult.GotifyAccess != nil {
				gotifyMasterSecrets = append(gotifyMasterSecrets, *gotifyResult.GotifyAccess)
			}
		}
		
		if len(gotifyMasterSecrets) == 1 {
			// found exactly one master as it should be
			data.GotifyHost = types.StringValue(gotifyMasterSecrets[0].Host)
			data.GotifyCloudDomain = types.StringValue(gotifyMasterSecrets[0].CloudDomain)
			data.GotifyPassword = types.StringValue(gotifyMasterSecrets[0].Password)

			// Save data into Terraform state
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			return
		}else if len(gotifyMasterSecrets) == 0 {
			resp.Diagnostics.AddError("Config Error", "Multi cloud peers and token defined but no gotify master found among them!")
			return
		}else if len(gotifyMasterSecrets) > 1 {
			resp.Diagnostics.AddError("Config Error", "Multi cloud peers and token defined but more than one master found among them!")
			return
		}
	}

	resp.Diagnostics.AddError("Config Error", "No master stack gotify discovery secret found and no multi cloud peers / tokens defined in params!")
	
}
