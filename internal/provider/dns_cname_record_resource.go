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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int32default"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &DnsCnameRecordResource{}
var _ resource.ResourceWithImportState = &DnsCnameRecordResource{}

func NewDnsCnameRecordResource() resource.Resource {
	return &DnsCnameRecordResource{}
}

// DnsCnameRecordResource defines the resource implementation.
type DnsCnameRecordResource struct {
	cloudInventory CloudInventory
}

// DnsCnameRecordResourceModel describes the resource data model.
type DnsCnameRecordResourceModel struct {
	Zone	types.String `tfsdk:"zone"`
	Name	types.String `tfsdk:"name"`
	CName	types.String `tfsdk:"cname"`
	Ttl	types.Int32 `tfsdk:"ttl"`
}

func (r *DnsCnameRecordResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_dns_cname_record"
}

func (r *DnsCnameRecordResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Create a cname record inside the PXCs nameservers.",

		Attributes: map[string]schema.Attribute{
			"zone": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "DNS zone we want to create our cname record in.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(), // changing host forces replace
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the dns record that will be created.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(), // changes are irrelevant
				},
			},
			"cname": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "CName record value to create. Full hostname.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(), // changing host forces replace
				},
			},
			"ttl": schema.Int32Attribute{
				MarkdownDescription: "TTL for the DNS Record, defaults to 5 minutes.",
				Optional: 					 true,
				Computed: 					 true,
				Default: 					 int32default.StaticInt32(500),
			},
		},
	}
}

func (r *DnsCnameRecordResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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


func (r *DnsCnameRecordResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data DnsCnameRecordResourceModel

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

	cresp, err := client.CreateCNameRecord(ctx, &pb.CreateCNameRecordRequest{
		TargetPve: r.cloudInventory.TargetPve, Zone: data.Zone.ValueString(),
		Name: data.Name.ValueString(), Cname: data.CName.ValueString(), Ttl: data.Ttl.ValueInt32(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create cname record, got error: %s", err))
		return
	}

	if !cresp.Success {
		resp.Diagnostics.AddError("Response Error", fmt.Sprintf("Got error from rpc: %s", cresp.ErrMessage))
		return
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *DnsCnameRecordResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data DnsCnameRecordResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *DnsCnameRecordResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Update Not Supported",
		"This resource does not support in-place updates. Any change to these attributes "+
		"should have triggered a replacement. This is a provider bug.",
  )
	// var data DnsCnameRecordResourceModel

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

func (r *DnsCnameRecordResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data DnsCnameRecordResourceModel

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

	cresp, err := client.DeleteCNameRecord(ctx, &pb.DeleteCNameRecordRequest{
		TargetPve: r.cloudInventory.TargetPve, Zone: data.Zone.ValueString(),
		Name: data.Name.ValueString(),
	})

	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete cname record, got error: %s", err))
		return
	}

	if !cresp.Success {
		resp.Diagnostics.AddError("Response Error", fmt.Sprintf("Got error from rpc: %s", cresp.ErrMessage))
		return
	}
}

func (r *DnsCnameRecordResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
