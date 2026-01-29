// Copyright IBM Corp. 2021, 2025
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strconv"

	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &PveGraphiteExporterResource{}
var _ resource.ResourceWithImportState = &PveGraphiteExporterResource{}

func NewPveGraphiteExporterResource() resource.Resource {
	return &PveGraphiteExporterResource{}
}

// PveGraphiteExporterResource defines the resource implementation.
type PveGraphiteExporterResource struct {
	cloudInventory CloudInventory
}

// PveGraphiteExporterResourceModel describes the resource data model.
type PveGraphiteExporterResourceModel struct {
	ExporterName types.String `tfsdk:"exporter_name"`
	Server       types.String `tfsdk:"server"`
	Port         types.Int64  `tfsdk:"port"`
}

func (r *PveGraphiteExporterResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_pve_graphite_exporter"
}

func (r *PveGraphiteExporterResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates a TCP graphite exporter in your proxmox cluster.",

		Attributes: map[string]schema.Attribute{
			"exporter_name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Unique name of the exporter on your proxmox cluster.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(), // changing host forces replace
				},
			},
			"server": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Server address where metrics will be send to.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(), // lazy replace
				},
			},
			"port": schema.Int64Attribute{
				Required:            true,
				MarkdownDescription: "UDP port of the server.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(), // lazy replace
				},
			},
		},
	}
}

func (r *PveGraphiteExporterResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PveGraphiteExporterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data PveGraphiteExporterResourceModel

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
		"--server":  data.Server.ValueString(),
		"--port":    strconv.FormatInt(int64(data.Port.ValueInt64()), 10),
		"--type":    "graphite", // default is udp
	}

	// perform the request
	cresp, err := client.CreateProxmoxApi(ctx, &pb.CreateProxmoxApiRequest{TargetPve: r.cloudInventory.TargetPve, ApiPath: fmt.Sprintf("/cluster/metrics/server/graphite-%s", data.ExporterName.ValueString()), CreateArgs: createArgs})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable make create exporter api request, got error: %s", err))
		return
	}

	if !cresp.Success {
		resp.Diagnostics.AddError("Create Call Error", fmt.Sprintf("Error on server side making exporter create call, got error: %s", cresp.ErrMessage))
		return
	}

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PveGraphiteExporterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data PveGraphiteExporterResourceModel

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

func (r *PveGraphiteExporterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Update Not Supported",
		"This resource does not support in-place updates. Any change to these attributes "+
			"should have triggered a replacement. This is a provider bug.",
	)

	// var data PveGraphiteExporterResourceModel

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

func (r *PveGraphiteExporterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data PveGraphiteExporterResourceModel

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
	cresp, err := client.DeleteProxmoxApi(ctx, &pb.DeleteProxmoxApiRequest{TargetPve: r.cloudInventory.TargetPve, ApiPath: fmt.Sprintf("/cluster/metrics/server/graphite-%s", data.ExporterName.ValueString())})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable make delete exporter api request, got error: %s", err))
		return
	}

	if !cresp.Success {
		resp.Diagnostics.AddError("Create Call Error", fmt.Sprintf("Error on server side making delete exporter call, got error: %s", cresp.ErrMessage))
		return
	}
}

func (r *PveGraphiteExporterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
