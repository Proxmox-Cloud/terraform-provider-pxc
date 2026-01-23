// Copyright IBM Corp. 2021, 2025
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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

	"filippo.io/age"
	"filippo.io/age/agessh"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &CloudSecretAgeResource{}
var _ resource.ResourceWithImportState = &CloudSecretAgeResource{}

func NewCloudSecretAgeResource() resource.Resource {
	return &CloudSecretAgeResource{}
}

// CloudSecretAgeResource defines the resource implementation.
type CloudSecretAgeResource struct {
	cloudInventory CloudInventory
}

// CloudSecretAgeResourceModel describes the resource data model.
type CloudSecretAgeResourceModel struct {
	SecretName types.String `tfsdk:"secret_name"`
	B64AgeData types.String `tfsdk:"b64_age_data"`
	PlainData  types.String `tfsdk:"plain_data"`
}

func (r *CloudSecretAgeResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_cloud_age_secret"
}

func (r *CloudSecretAgeResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates age encrypted secret in proxmox cloud. This is useful for storing hard coded secrets safely in git repositories. This resource will try to use keys from the ~/.ssh directory for decryption during resource creation.",
		Attributes: map[string]schema.Attribute{
			"secret_name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the secret, has to be unique for the target_pve.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(), // lazy replace
				},
			},
			"b64_age_data": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Insert your b64 encoded age encrypted secret here, use `age -R ~/.ssh/id_ed25519.pub -R ~/.ssh/id_rsa.pub secret.file | base64 -w0` to generate the value. Currently only supports string files.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(), // lazy replace
				},
			},
			"plain_data": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "During resource creation the provider looks at the env var CLOUD_AGE_SSH_KEY_FILE to load file for initial decryption. Once the resource is created you can here access the unencrypted secret, this is for convenience sake. You can also use the pxc_cloud_secret datasource to access it.",
			},
		},
	}
}

func (r *CloudSecretAgeResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *CloudSecretAgeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data CloudSecretAgeResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// try decode the secret value with keyfiles from ~/.ssh
	identities := []age.Identity{}
	home, _ := os.UserHomeDir()
	sshDir := filepath.Join(home, ".ssh")
	
	files, _ := os.ReadDir(sshDir)
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "id_") && !strings.HasSuffix(file.Name(), ".pub") {
			keyPath := filepath.Join(sshDir, file.Name())
			
			pemBytes, err := os.ReadFile(keyPath)
			if err != nil {
				continue
			}

			identity, err := agessh.ParseIdentity(pemBytes)
			if err == nil {
				identities = append(identities, identity)
			}
		}
	}
	
	// additionally a env var can be passed to specific custom location (e.g. e2e usecase)
	ageSshKey := os.Getenv("CLOUD_AGE_SSH_KEY_FILE")
	if ageSshKey != "" {
		pemBytes, err := os.ReadFile(ageSshKey)
		if err != nil {
			resp.Diagnostics.AddError("Read err", fmt.Sprintf("Error reading ssh key %s", err))
			return
		}

		identity, err := agessh.ParseIdentity(pemBytes)
		if err != nil {
			resp.Diagnostics.AddError("Parse err", fmt.Sprintf("Error parsing age id %s", err))
			return
		}
		identities = append(identities, identity)
	}

	b64Reader := base64.NewDecoder(base64.StdEncoding, strings.NewReader(data.B64AgeData.ValueString()))
	re, err := age.Decrypt(b64Reader, identities...)
	if err != nil {
		resp.Diagnostics.AddError("Decrypt err", fmt.Sprintf("Failed to decrypt: %v (Ensure your SSH key matches one of the recipients)", err))
		return
	}

	var out bytes.Buffer
	if _, err := io.Copy(&out, re); err != nil {
		resp.Diagnostics.AddError("Read err", fmt.Sprintf("Error reading decrypted data: %v", err))
		return
	}

	data.PlainData = types.StringValue(out.String())

	client, err := GetCloudRpcService(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to init client, got error: %s", err))
		return
	}

	// perform the request
	cresp, err := client.CreateCloudSecret(ctx, &pb.CreateCloudSecretRequest{TargetPve:r.cloudInventory.TargetPve, CloudDomain: r.cloudInventory.CloudDomain, SecretName: data.SecretName.ValueString(), SecretData: data.PlainData.String()})
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

func (r *CloudSecretAgeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data CloudSecretAgeResourceModel

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

func (r *CloudSecretAgeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Update Not Supported",
		"This resource does not support in-place updates. Any change to these attributes "+
			"should have triggered a replacement. This is a provider bug.",
	)
	// var data CloudSecretAgeResourceModel

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

func (r *CloudSecretAgeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data CloudSecretAgeResourceModel

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

func (r *CloudSecretAgeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
