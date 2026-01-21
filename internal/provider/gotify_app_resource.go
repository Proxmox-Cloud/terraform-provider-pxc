// Copyright IBM Corp. 2021, 2025
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"

	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
  "github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &GotifyAppResource{}
var _ resource.ResourceWithImportState = &GotifyAppResource{}

func NewGotifyAppResource() resource.Resource {
	return &GotifyAppResource{}
}

// GotifyAppResource defines the resource implementation.
type GotifyAppResource struct {
}

// GotifyAppResourceModel describes the resource data model.
type GotifyAppResourceModel struct {
	GotifyHost						types.String `tfsdk:"gotify_host"`
	GotifyAdminPw					types.String `tfsdk:"gotify_admin_pw"`
	AppName							types.String `tfsdk:"app_name"`
	AllowInsecure					types.Bool 	 `tfsdk:"allow_insecure"`
	AppToken						types.String `tfsdk:"app_token"`
	AppId							types.Int64	 `tfsdk:"app_id"`
}

func (r *GotifyAppResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gotify_app"
}

func (r *GotifyAppResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Create a gotify application",

		Attributes: map[string]schema.Attribute{
			"gotify_host": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Gotify host to connect to (e.g. gotify.example.com).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(), // changing host forces replace
				},
			},
			"gotify_admin_pw": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Password for the root gotify admin user needed to make api calls.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(), // changes are irrelevant
				},
			},
			"app_name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the gotify app that will be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(), // changing host forces replace
				},
			},
			"allow_insecure": schema.BoolAttribute{
				MarkdownDescription: "Allows connection to an insecure gotify serving a self signed certificate via https. Needed for e2e tests.",
				Optional: 					 true,
				Default: 						 booldefault.StaticBool(false),
				Computed: 					 true,
			},
			"app_token": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Application token for the created gotify app.",
			},
			"app_id": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Application id for later deletion.",
			},
		},
	}
}

func (r *GotifyAppResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}
}

type GotifyAppResponse struct {
    AppToken string `json:"token"`
    Id       int64  `json:"id"`
}

func (r *GotifyAppResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data GotifyAppResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: data.AllowInsecure.ValueBool()},
		},
	}

	postUrl := fmt.Sprintf("https://%s/application", data.GotifyHost.ValueString())

	body, _ := json.Marshal(map[string]string{"name": data.AppName.ValueString()})

	httpReq, err := http.NewRequestWithContext(ctx, "POST", postUrl, bytes.NewBuffer(body))
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create request: %s", err))
		return
	}

	httpReq.Header.Set("Content-Type", "application/json")
  	httpReq.SetBasicAuth("admin", data.GotifyAdminPw.ValueString())

	httpResp, err := client.Do(httpReq)
	if err != nil {
		resp.Diagnostics.AddError("Request error", fmt.Sprintf("Error calling gotify: %s", err))
		return
	}
	defer httpResp.Body.Close()

	// read the response
	bodyBytes, err := io.ReadAll(httpResp.Body)
	if err != nil {
		resp.Diagnostics.AddError("Response error", fmt.Sprintf("Failed to read body: %s", err))
		return
	}

	var response GotifyAppResponse
	err = json.Unmarshal(bodyBytes, &response)
	if err != nil {
			resp.Diagnostics.AddError("JSON Error", fmt.Sprintf("Error unmarshalling: %s", err))
	}

	// save token and id for later delete
	data.AppToken = types.StringValue(response.AppToken)
	data.AppId = types.Int64Value(response.Id)
	
	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GotifyAppResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data GotifyAppResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// If applicable, this is a great opportunity to initialize any necessary
	// provider client data and make a call using it.
	// httpResp, err := r.client.Do(httpReq)
	// if err != nil {
	//     resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read example, got error: %s", err))
	//     return
	// }

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GotifyAppResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Update Not Supported",
		"This resource does not support in-place updates. Any change to these attributes "+
		"should have triggered a replacement. This is a provider bug.",
  )
	var data GotifyAppResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// If applicable, this is a great opportunity to initialize any necessary
	// provider client data and make a call using it.
	// httpResp, err := r.client.Do(httpReq)
	// if err != nil {
	//     resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update example, got error: %s", err))
	//     return
	// }

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GotifyAppResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data GotifyAppResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: data.AllowInsecure.ValueBool()},
		},
	}

	postUrl := fmt.Sprintf("https://%s/application/%d", data.GotifyHost.ValueString(), data.AppId.ValueInt64())

	httpReq, err := http.NewRequestWithContext(ctx, "DELETE", postUrl, nil)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create delete request: %s", err))
		return
	}

 	httpReq.SetBasicAuth("admin", data.GotifyAdminPw.ValueString())

	httpResp, err := client.Do(httpReq)
	if err != nil {
		resp.Diagnostics.AddError("Request error", fmt.Sprintf("Error calling gotify: %s", err))
		return
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK && httpResp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(httpResp.Body)
		resp.Diagnostics.AddError(
			"Delete Failed",
			fmt.Sprintf("Delete failed with code %d, message: %s", httpResp.StatusCode, string(body)),
		)
		return
	}

}

func (r *GotifyAppResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}