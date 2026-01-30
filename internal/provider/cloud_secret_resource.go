// Copyright IBM Corp. 2021, 2025
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"time"

	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &CloudSecretResource{}
var _ resource.ResourceWithImportState = &CloudSecretResource{}

func NewCloudSecretResource() resource.Resource {
	return &CloudSecretResource{}
}

// CloudSecretResource defines the resource implementation.
type CloudSecretResource struct {
	cloudInventory CloudInventory
}

// CloudSecretResourceModel describes the resource data model.
type CloudSecretResourceModel struct {
	SecretName types.String `tfsdk:"secret_name"`
	SecretData types.String `tfsdk:"secret_data"`
	SecretType types.String `tfsdk:"secret_type"`
}

func (r *CloudSecretResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cloud_secret"
}

func (r *CloudSecretResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates a proxmox cloud secret that is saved in the clouds patroni postgres.",

		Attributes: map[string]schema.Attribute{
			"secret_name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the secret, has to be unique for the target_pve.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(), // lazy replace
				},
			},
			// todo: figure out terraforms absurd type system to avoid jsonencode and decode calls to pass / receive dynamic values
			"secret_data": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Secret data as json string, use jsonencode to pass your terraform object (will be converted to json on storage).",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(), // lazy replace
				},
			},
			"secret_type": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Type of the secret, can be used to store configuration secrets and for discovery.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(), // lazy replace
				},
			},
		},
	}
}

func (r *CloudSecretResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *CloudSecretResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data CloudSecretResourceModel

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

	

	// perform the request
	cresp, err := client.CreateCloudSecret(ctx, &pb.CreateCloudSecretRequest{CloudDomain: r.cloudInventory.CloudDomain, TargetPve: r.cloudInventory.TargetPve, SecretName: data.SecretName.ValueString(), SecretType: data.SecretType.ValueString(), SecretData: data.SecretData.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable make grp create cloud secret request, got error: %s", err))
		return
	}

	if !cresp.Success {
		resp.Diagnostics.AddError("Create Call Error", fmt.Sprintf("Error on server side creating cloud secret, got error: %s", cresp.ErrMessage))
		return
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *CloudSecretResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data CloudSecretResourceModel

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

func (r *CloudSecretResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Update Not Supported",
		"This resource does not support in-place updates. Any change to these attributes "+
			"should have triggered a replacement. This is a provider bug.",
	)

	// var data CloudSecretResourceModel

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

func (r *CloudSecretResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data CloudSecretResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

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
	cresp, err := client.DeleteCloudSecret(ctx, &pb.DeleteCloudSecretRequest{CloudDomain: r.cloudInventory.CloudDomain, TargetPve: r.cloudInventory.TargetPve, SecretName: data.SecretName.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable make grp delete cloud secret request, got error: %s", err))
		return
	}

	if !cresp.Success {
		resp.Diagnostics.AddError("Create Call Error", fmt.Sprintf("Error on server side deleting cloud secret, got error: %s", cresp.ErrMessage))
		return
	}

}

func (r *CloudSecretResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
