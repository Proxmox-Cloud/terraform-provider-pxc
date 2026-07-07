// Copyright IBM Corp. 2021, 2025
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"encoding/json"

	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &HelmMirrorResource{}
var _ resource.ResourceWithImportState = &HelmMirrorResource{}

func NewHelmMirrorResource() resource.Resource {
	return &HelmMirrorResource{}
}

// HelmMirrorResource defines the resource implementation.
type HelmMirrorResource struct {
	cloudInventory CloudInventory
}

// HelmMirrorResourceModel describes the resource data model.
type HelmMirrorResourceModel struct {
	SourceRepository types.String `tfsdk:"source_repository"`
	SourceName types.String `tfsdk:"source_name"`
	Chart types.String `tfsdk:"chart"`
	Version types.String `tfsdk:"version"`
	RepositoryOut types.String `tfsdk:"repository_out"`
}

func (r *HelmMirrorResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_helm_mirror"
}

func (r *HelmMirrorResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Helm mirror via harbor oci if configured (auto discovered).",

		Attributes: map[string]schema.Attribute{
			"source_repository": schema.StringAttribute{
				MarkdownDescription: "Repository of the helm chart to mirror.",
				Required:            true,
				// lazy replace
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"source_name": schema.StringAttribute{
				MarkdownDescription: "Name for the repository to add (passed to helm repo add NAME URL).",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"chart": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Helm chart name within that repository.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"version": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Version of the helm chart to mirror.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"repository_out": schema.StringAttribute{
				MarkdownDescription: "Resulting repository url, either oci of the harbor registry or the original if no mirroring was discovered.",
				Computed:            true,
			},
		},
	}
}


func (r *HelmMirrorResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

type HarborMirrorCreds struct {
	FullName     string `json:"full_name"`
	Secret       string `json:"secret"`
	AuthB64      string `json:"auth_b64"`
	HarborHost   string `json:"harbor_host"`
	DockerConfig string `json:"dockerconfig"`
}

// helm add / update is not thread safe so we implement a mutex
var helmCliMutex sync.Mutex

func (r *HelmMirrorResource) getMirroredIfExists(ctx context.Context, client pb.CloudServiceClient, data *HelmMirrorResourceModel) (types.String, HarborMirrorCreds, error){

	cresp, err := client.GetCloudSecrets(ctx, &pb.GetCloudSecretsRequest{CloudDomain: r.cloudInventory.CloudDomain, TargetPve: r.cloudInventory.TargetPve, SecretType: "harbor-admin-auth"})
	if err != nil {
		return types.StringNull(), HarborMirrorCreds{}, fmt.Errorf("Unable to get mirror secrets, got error: %s", err)
	}

	// decode the secrets if any
	var mirrorCredsMap map[string]HarborMirrorCreds

	err = json.Unmarshal([]byte(cresp.Secrets), &mirrorCredsMap)
	if err != nil {
		return types.StringNull(), HarborMirrorCreds{}, fmt.Errorf("Unable to unmarshal secrets response, got error: %s", err)
	}

	if len(mirrorCredsMap) == 0 {
		tflog.Info(ctx, "No mirror credentials discovered, returning origin as repository_out")
		return data.SourceRepository, HarborMirrorCreds{}, nil
	}

	e2eHarborHost := os.Getenv("E2E_HARBOR_MIRROR_HOST")
	if e2eHarborHost == "" && len(mirrorCredsMap) > 1 {
		tflog.Warn(ctx, "More than one harbor mirror discovery secret found!")
	}

	// get the first mirror secret in the list
	// todo: perform some check to warn if multiple mirrors are defined
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
		return types.StringNull(), HarborMirrorCreds{}, errors.New("Could not find harbor mirror credentials in returned map, even though it has items!")
	}

	// we found secrets and skopeo is installed, now we perform the mirror / return if its already present
	tflog.Info(ctx, fmt.Sprintf("Skopeo presence check on: docker://%s/cloud-helm-mirror/%s/%s:%s", adminCreds.HarborHost, data.SourceName.ValueString(), data.Chart.ValueString(), data.Version.ValueString()))
	checkExists := exec.Command(
		"skopeo", "inspect", "--raw", 
		"--creds", fmt.Sprintf("%s:%s", adminCreds.FullName, adminCreds.Secret), 
		fmt.Sprintf("docker://%s/cloud-helm-mirror/%s/%s:%s", adminCreds.HarborHost, data.SourceName.ValueString(), data.Chart.ValueString(), data.Version.ValueString()))

	_, err = checkExists.Output()

	if err != nil {
		// doesnt exist return the source repository
		return data.SourceRepository, adminCreds, nil
	}else {
		// exists, return oci repository
		return types.StringValue(fmt.Sprintf("oci://%s/cloud-helm-mirror/%s", adminCreds.HarborHost, data.SourceName.ValueString())), adminCreds, nil
	}
}

// abuse the plan step to already check for a presently mirrored artifact
func (r *HelmMirrorResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return // do nothing on destroy
	}

	if !req.State.Raw.IsNull() {
		return // when the resource already exists we just return as is
	}	

	// lookahead if mirror already exists, then we return oci mirror url, otherwise we set repository_out to source_repository 
	// so helm_release resources can do their Read() call properly
	var data HelmMirrorResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// todo: this is duplicated with the create method and can be kept DRY by someone who knows go
	client, err := GetCloudRpcService(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to init client, got error: %s", err))
		return
	}

	// first we perform checks, look for mirror discovery secret, check if skopeo is installed
	_, err = exec.LookPath("skopeo")
	if err != nil {
		resp.Diagnostics.AddWarning("Setup warning", fmt.Sprintf("Skopeo cli was not found installed, did you run pxc.cloud.setup_control_node? Got error: %s", err))
		
		// set input == output regsitry and return
		data.RepositoryOut = data.SourceRepository
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &data)...)

		return
	}

	repoStrVal, _, err := r.getMirroredIfExists(ctx, client, &data)

	if err != nil {
		resp.Diagnostics.AddError("Get Mirror Error", fmt.Sprintf("Unable to check existing mirror, got error: %s", err))
		return
	}

	// func either returned the mirrored oci repo uril or the original
	data.RepositoryOut = repoStrVal
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &data)...)
}

func (r *HelmMirrorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data HelmMirrorResourceModel

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

	// first we perform checks, look for mirror discovery secret, check if skopeo is installed
	_, err = exec.LookPath("skopeo")
	if err != nil {
		resp.Diagnostics.AddWarning("Setup warning", fmt.Sprintf("Skopeo cli was not found installed, did you run pxc.cloud.setup_control_node? Got error: %s", err))
		
		// set input == output regsitry and return
		data.RepositoryOut = data.SourceRepository
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)

		return
	}

	repoStrVal, adminCreds, err := r.getMirroredIfExists(ctx, client, &data)

	if err != nil {
		resp.Diagnostics.AddError("Get Mirror Error", fmt.Sprintf("Unable to check existing mirror, got error: %s", err))
		return
	}

	// not yet mirrored
	if repoStrVal.Equal(data.SourceRepository) {
		// chart could not be found, we assume its because not mirrored
		// todo: more detailed error checking
		tflog.Info(ctx, "Repository not yet present, mirroring")

		// create tmp dir for helm download
		tempDir, err := os.MkdirTemp("", "helm_skopeo_*")
		if err != nil {
			resp.Diagnostics.AddError("Directory Error", fmt.Sprintf("Unable to create tmp dir, got error: %s", err))
			return
		}

		defer os.RemoveAll(tempDir)

		if !strings.HasPrefix(data.SourceRepository.ValueString(), "oci://"){
			// helm repo add and update only works on non oci https repositories 
			helmCliMutex.Lock()

			// add original helm repo and download chart
			repoAdd := exec.Command("helm", "repo", "add", data.SourceName.ValueString(), data.SourceRepository.ValueString())
			_, err = repoAdd.CombinedOutput()
			if err != nil {
				resp.Diagnostics.AddError("Helm Error", fmt.Sprintf("Error adding helm repo, got error: %s", err))
				helmCliMutex.Unlock()
				return
			}

			repoUpdate := exec.Command("helm", "repo", "update", data.SourceName.ValueString())
			_, err = repoUpdate.CombinedOutput()
			if err != nil {
				resp.Diagnostics.AddError("Helm Error", fmt.Sprintf("Error updating helm repo, got error: %s", err))
				helmCliMutex.Unlock()
				return
			}
			helmCliMutex.Unlock() // finished with helm operations that may only run synchronous

			pullChart := exec.Command("helm", "pull", fmt.Sprintf("%s/%s", data.SourceName.ValueString(), data.Chart.ValueString()), "--version", data.Version.ValueString(), "--destination", tempDir)
			_, err = pullChart.CombinedOutput()
			if err != nil {
				resp.Diagnostics.AddError("Helm Error", fmt.Sprintf("Error downloading chart, got error: %s", err))
				return
			}

		}else {
			// on oci repos we pull directly from the repo
			pullChart := exec.Command("helm", "pull", fmt.Sprintf("%s/%s", data.SourceRepository.ValueString(), data.Chart.ValueString()), "--version", data.Version.ValueString(), "--destination", tempDir)
			_, err = pullChart.CombinedOutput()
			if err != nil {
				resp.Diagnostics.AddError("Helm Error", fmt.Sprintf("Error downloading chart, got error: %s", err))
				return
			}
		}

		pushChart := exec.Command(
			"helm", "push", filepath.Join(tempDir, fmt.Sprintf("%s-%s.tgz", data.Chart.ValueString(), data.Version.ValueString())), 
			fmt.Sprintf("oci://%s/cloud-helm-mirror/%s", adminCreds.HarborHost, data.SourceName.ValueString()),
			"--username", adminCreds.FullName, "--password", adminCreds.Secret,
		)
		_, err = pushChart.CombinedOutput()
		if err != nil {
			resp.Diagnostics.AddError("Helm Error", fmt.Sprintf("Error pushing chart, got error: %s", err))
			return
		}
	}

	// mirror complete / already mirrored, we return the oci mirror repo from our harbor
	// authentication to this happens via kubeconfig resource registry discovery
	tflog.Info(ctx, fmt.Sprintf("Setting repository out: oci://%s/cloud-helm-mirror/%s", adminCreds.HarborHost, data.SourceName.ValueString()))
	data.RepositoryOut = types.StringValue(fmt.Sprintf("oci://%s/cloud-helm-mirror/%s", adminCreds.HarborHost, data.SourceName.ValueString()))

	// Save data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)

}

func (r *HelmMirrorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data HelmMirrorResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *HelmMirrorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Update Not Supported",
		"This resource does not support in-place updates. Any change to these attributes "+
		"should have triggered a replacement. This is a provider bug.",
  	)
	// var data HelmMirrorResourceModel

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

// no delete call, mirrored artificats will / should be cleaned up by harbor policies
func (r *HelmMirrorResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data HelmMirrorResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

}

func (r *HelmMirrorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
