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
var _ resource.Resource = &ExternalAcmeTlsResource{}
var _ resource.ResourceWithImportState = &ExternalAcmeTlsResource{}

func NewExternalAcmeTlsResource() resource.Resource {
	return &ExternalAcmeTlsResource{}
}

// ExternalAcmeTlsResource defines the resource implementation.
type ExternalAcmeTlsResource struct {
	cloudInventory CloudInventory
}

type ExternalAcmeTlsResourceModel struct {
	ConfigJson     types.String    `tfsdk:"config_json"`
	EcCsrJson      types.String    `tfsdk:"ec_csr_json"`
}

func (r *ExternalAcmeTlsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_external_acme_tls"
}

func (r *ExternalAcmeTlsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Request proxmox cloud tls certificate from external cluster.",

		Attributes: map[string]schema.Attribute{
			// pxc formatted acme config input
            "config_json": schema.StringAttribute{
                Required: true,

				// lazy replace
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
            },

            // terraform generated certificate signing request
            "ec_csr_json": schema.StringAttribute{
                Required: true,

				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
            },
		},
	}
}

func (r *ExternalAcmeTlsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

	r.cloudInventory = cloudInv
}



func (r *ExternalAcmeTlsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ExternalAcmeTlsResourceModel

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

	cresp, err := client.CreateExternalAcmeTls(ctx, 
		&pb.CreateExternalAcmeTlsRequest{
			TargetPve: r.cloudInventory.TargetPve, 
			StackFqdn: fmt.Sprintf("%s.%s", r.cloudInventory.StackName, r.cloudInventory.CloudDomain),
			CertConfigJson: string(data.ConfigJson.ValueString()),
			EcCsrJson: string(data.EcCsrJson.ValueString()),
	})

	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to make external acme create request, got error: %s", err))
		return
	}

	if !cresp.Success {
		resp.Diagnostics.AddError("Create Call Error", fmt.Sprintf("Error on server side making acme create request, got error: %s", cresp.ErrMessage))
		return
	}
	
	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ExternalAcmeTlsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ExternalAcmeTlsResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ExternalAcmeTlsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Update Not Supported",
		"This resource does not support in-place updates. Any change to these attributes "+
		"should have triggered a replacement. This is a provider bug.",
  )
	// var data ExternalAcmeTlsResourceModel

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

func (r *ExternalAcmeTlsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ExternalAcmeTlsResourceModel

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

	cresp, err := client.DeleteExternalAcmeTls(ctx, 
		&pb.DeleteExternalAcmeTlsRequest{
			TargetPve: r.cloudInventory.TargetPve, 
			StackFqdn: fmt.Sprintf("%s.%s", r.cloudInventory.StackName, r.cloudInventory.CloudDomain),
	})

	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to make external acme create request, got error: %s", err))
		return
	}

	if !cresp.Success {
		resp.Diagnostics.AddError("Create Call Error", fmt.Sprintf("Error on server side making acme create request, got error: %s", cresp.ErrMessage))
		return
	}

}

func (r *ExternalAcmeTlsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}