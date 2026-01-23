// Copyright IBM Corp. 2021, 2025
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &PveGotifyTargetResource{}
var _ resource.ResourceWithImportState = &PveGotifyTargetResource{}

func NewPveGotifyTargetResource() resource.Resource {
	return &PveGotifyTargetResource{}
}

// PveGotifyTargetResource defines the resource implementation.
type PveGotifyTargetResource struct {
	cloudInventory CloudInventory
}

// PveGotifyTargetResourceModel describes the resource data model.
type PveGotifyTargetResourceModel struct {
	GotifyHost  types.String `tfsdk:"gotify_host"`
	GotifyToken types.String `tfsdk:"gotify_token"`
}

func (r *PveGotifyTargetResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_pve_gotify_target"
}

func (r *PveGotifyTargetResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates a gotify notification target in your proxmox cluster.",

		Attributes: map[string]schema.Attribute{
			"gotify_host": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Gotify host to connect to (e.g. gotify.example.com).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(), // changing host forces replace
				},
			},
			"gotify_token": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Gotify app token that proxmox uses when publishing notifications.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(), // lazy replace
				},
			},
		},
	}
}

func (r *PveGotifyTargetResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

	r.cloudInventory = cloudInv
}

func (r *PveGotifyTargetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data PveGotifyTargetResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client, err := GetCloudRpcService(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to init client, got error: %s", err))
		return
	}

	createArgs := map[string]string{
		"--name":    fmt.Sprintf("gotify-%s", r.cloudInventory.StackName),
		"--server":  fmt.Sprintf("https://%s", data.GotifyHost.ValueString()),
		"--token":   data.GotifyToken.ValueString(),
		"--comment": "Proxmox cloud gotify alerts.",
	}

	// perform the request
	cresp, err := client.CreateProxmoxApi(ctx, &pb.CreateProxmoxApiRequest{TargetPve: r.cloudInventory.TargetPve, ApiPath: "/cluster/notifications/endpoints/gotify", CreateArgs: createArgs})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable make create gotify api request, got error: %s", err))
		return
	}

	if !cresp.Success {
		resp.Diagnostics.AddError("Create Call Error", fmt.Sprintf("Error on server side making gotify create call, got error: %s", cresp.ErrMessage))
		return
	}

	// create error matcher
	createArgs = map[string]string{
		"--name":           fmt.Sprintf("gotify-%s-matcher", r.cloudInventory.StackName),
		"--target":         fmt.Sprintf("gotify-%s", r.cloudInventory.StackName),
		"--match-severity": "error",
	}
	cresp, err = client.CreateProxmoxApi(ctx, &pb.CreateProxmoxApiRequest{TargetPve: r.cloudInventory.TargetPve, ApiPath: "/cluster/notifications/matchers", CreateArgs: createArgs})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable make create matcher api request, got error: %s", err))
		return
	}

	if !cresp.Success {
		resp.Diagnostics.AddError("Create Call Error", fmt.Sprintf("Error on server side making matcher create call, got error: %s", cresp.ErrMessage))
		return
	}
	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PveGotifyTargetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data PveGotifyTargetResourceModel

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

func (r *PveGotifyTargetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Update Not Supported",
		"This resource does not support in-place updates. Any change to these attributes "+
			"should have triggered a replacement. This is a provider bug.",
	)

	// var data PveGotifyTargetResourceModel

	// // Read Terraform plan data into the model
	// resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	// if resp.Diagnostics.HasError() {
	// 	return
	// }

	// If applicable, this is a great opportunity to initialize any necessary
	// provider client data and make a call using it.
	// httpResp, err := r.client.Do(httpReq)
	// if err != nil {
	//     resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update example, got error: %s", err))
	//     return
	// }

	// Save updated data into Terraform state
	// resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PveGotifyTargetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data PveGotifyTargetResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	client, err := GetCloudRpcService(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to init client, got error: %s", err))
		return
	}

	// delete the matcher first
	cresp, err := client.DeleteProxmoxApi(ctx, &pb.DeleteProxmoxApiRequest{TargetPve: r.cloudInventory.TargetPve, ApiPath: fmt.Sprintf("/cluster/notifications/matchers/%s", fmt.Sprintf("gotify-%s-matcher", r.cloudInventory.StackName))})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable make delete matcher api request, got error: %s", err))
		return
	}

	if !cresp.Success {
		resp.Diagnostics.AddError("Create Call Error", fmt.Sprintf("Error on server side making delete matcher call, got error: %s", cresp.ErrMessage))
		return
	}

	// perform the request to delete gotify notification target
	cresp, err = client.DeleteProxmoxApi(ctx, &pb.DeleteProxmoxApiRequest{TargetPve: r.cloudInventory.TargetPve, ApiPath: fmt.Sprintf("/cluster/notifications/endpoints/gotify/%s", fmt.Sprintf("gotify-%s", r.cloudInventory.StackName))})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable make delete gotify api request, got error: %s", err))
		return
	}

	if !cresp.Success {
		resp.Diagnostics.AddError("Create Call Error", fmt.Sprintf("Error on server side making delete gotify call, got error: %s", cresp.ErrMessage))
		return
	}
}

func (r *PveGotifyTargetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
