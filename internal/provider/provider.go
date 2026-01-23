package provider

import (
	"context"

	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
	pb "github.com/Proxmox-Cloud/terraform-provider-pxc/internal/provider/protos"
	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Ensure PxcProvider satisfies various provider interfaces.
var _ provider.Provider = &PxcProvider{}
var _ provider.ProviderWithFunctions = &PxcProvider{}
var _ provider.ProviderWithEphemeralResources = &PxcProvider{}
var _ provider.ProviderWithActions = &PxcProvider{}

// PxcProvider defines the provider implementation.
type PxcProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
	exitCh  chan bool
}

// PxcProviderModel describes the provider data model.
type PxcProviderModel struct {
	InventoryPath types.String `tfsdk:"inventory"`
	TargetCluster types.String `tfsdk:"target_cluster"`
	exitCh       chan bool
}

func (p *PxcProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "pxc"
	resp.Version = p.version
}

func (p *PxcProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"inventory": schema.StringAttribute{
				MarkdownDescription: "Path to your proxmox cloud inventory yaml file.",
				Required:            true,
			},
			"target_cluster": schema.StringAttribute{
				MarkdownDescription: "Cluster you want to target, only needed/allowed when passing an inventory of type pxc.cloud.pve_cloud_inv",
				Optional:            true,
			},
		},
	}
}


type KubesprayInventory struct {
	TargetPve          string               `yaml:"target_pve"`
	StackName          string               `yaml:"stack_name"`
	// we need these two in the controller module and will return them in cloud_self data source
	ClusterCertEntries []interface{}   `yaml:"cluster_cert_entries"`
	ExternalDomains    []interface{} `yaml:"external_domains"`
}

type PveCloudInventory struct {
	PveCloudDomain string `yaml:"pve_cloud_domain"`
}

// this gets passed down to resources and they can dynamically pick / err what they need
type CloudInventory struct {
	Plugin	string 	`yaml:"plugin"`
	TargetPve string
	StackName string

	// nullables
	KubesprayInventory *KubesprayInventory
	PveCloudInventory *PveCloudInventory
}


func (p *PxcProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data PxcProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// first we parse the inventory file
	yamlFile, err := os.ReadFile(data.InventoryPath.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Inventory File",
			"Could not read file at "+data.InventoryPath.ValueString()+": "+err.Error(),
		)
		return
	}

	// first we need to check what type of inventory was passed
	var cloudInv CloudInventory
	err = yaml.Unmarshal(yamlFile, &cloudInv)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Parsing Inventory YAML",
			"Could not unmarshal YAML: "+err.Error(),
		)
		return
	}

	switch cloudInv.Plugin {
		case "pxc.cloud.pve_cloud_inv":
			// core cloud inventory
			if data.TargetCluster.IsNull() {
				resp.Diagnostics.AddError(
					"Bad configuration",
					"When passing a pxc.cloud.pve_cloud_inv inventory you need to set target_cluster in the provider configuration!",
				)
				return
			}
			// parse the pve_cloud_inv file
			var pveCloudInventory PveCloudInventory
			err = yaml.Unmarshal(yamlFile, &pveCloudInventory)
			if err != nil {
				resp.Diagnostics.AddError(
					"Error Parsing Inventory YAML",
					"Could not unmarshal YAML: "+err.Error(),
				)
				return
			}

			cloudInv.StackName = "master" // only one cloud inv per cloud
			cloudInv.TargetPve = fmt.Sprintf("%s.%s", data.TargetCluster.ValueString(), pveCloudInventory.PveCloudDomain)

			cloudInv.PveCloudInventory = &pveCloudInventory

		case "pxc.cloud.kubespray_inv":
			// kubernetes
			if !data.TargetCluster.IsNull() {
				resp.Diagnostics.AddError(
					"Bad configuration",
					"When passing a pxc.cloud.kubespray inventory you are not allowed to set target_cluster! It is sourced from the inventory file.",
				)
				return
			}

			var kubeInv KubesprayInventory
			err = yaml.Unmarshal(yamlFile, &kubeInv)
			if err != nil {
				resp.Diagnostics.AddError(
					"Error Parsing Inventory YAML",
					"Could not unmarshal YAML: "+err.Error(),
				)
				return
			}

			cloudInv.TargetPve = kubeInv.TargetPve
			cloudInv.StackName = kubeInv.StackName

			cloudInv.KubesprayInventory = &kubeInv


		default:
			resp.Diagnostics.AddError(
				"Unknown type",
				"Unknown plugin type: "+ cloudInv.Plugin,
			)
			return
	}

	// simply pass the inventory as data
	resp.DataSourceData = cloudInv
	resp.ResourceData = cloudInv
	resp.EphemeralResourceData = cloudInv

	// next launch our python grpc server

	// todo: implement option to specify pythonpath in provider and pass that up here somehow
	// or find a better solution
	virtualEnv := os.Getenv("VIRTUAL_ENV")
	if virtualEnv == "" {
		resp.Diagnostics.AddError("Client Error", "VIRTUAL_ENV not defined, cant launch gprc")
		return
	}

	// with this env var we can determine if we are running in a pytest context
	pytestCurrent := os.Getenv("PYTEST_CURRENT_TEST")

	// only install the pypi package if not in e2e scenario (in this case its installed via pip -e .)
	if pytestCurrent == "" && p.version != "dev" {
		// package will be published to pypi with same version tag as provider
		// todo: check against installed version and prevent from removing / missmatching
		pipCmd := exec.Command(fmt.Sprintf("%s/bin/pip", virtualEnv), "install", fmt.Sprintf("rpyc-pve-cloud==%s", p.version))

		output, err := pipCmd.CombinedOutput()
		if err != nil {
			resp.Diagnostics.AddError("Could not launch rpc server", fmt.Sprintf("Command failed with error: %v - %s", err, string(output)))
			return
		}
	}

	// start pyhon grpc server as daemon
	tflog.Info(ctx, fmt.Sprintf("Launching python rpc server on unix:///tmp/pc-rpc-%d.sock", os.Getpid()))
	cmd := exec.Command(fmt.Sprintf("%s/bin/pcrpc", virtualEnv), strconv.Itoa(os.Getpid()))
	if err := cmd.Start(); err != nil {
		resp.Diagnostics.AddError("Failed to start Python backend", err.Error())
		return
	}

	// launch routine to kill the server
	go func() {
		<-p.exitCh // wait for exit signal

		cmd.Process.Kill() // kill

		p.exitCh <- true // call finished
	}()

	// wait for rpc to come up and healthcheck to succeed
	deadline := time.Now().Add(10 * time.Second)

	for {
		if time.Now().After(deadline) {
			resp.Diagnostics.AddError("Failed to start python grpc server", "Deadline exceeded")
			return
		}

		// try connect via grpc and health check
		conn, err := grpc.NewClient(
			fmt.Sprintf("unix:///tmp/pc-rpc-%d.sock", os.Getpid()),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}
		defer conn.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		healthClient := pb.NewHealthClient(conn)
		hresp, err := healthClient.Check(ctx, &pb.HealthCheckRequest{TargetPve: cloudInv.TargetPve})

		if err != nil {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if hresp.Status == pb.HealthCheckResponse_MISSMATCH {
			resp.Diagnostics.AddError("Failed to start python grpc server", hresp.ErrorMessage)
			return
		}

		// this case should never hit.
		// todo: refactor
		if hresp.Status != pb.HealthCheckResponse_SERVING {
			time.Sleep(200 * time.Millisecond)
			continue
		}

		break // its up and running
	}

}

func (p *PxcProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewGotifyAppResource,
		NewCloudSecretResource,
		NewCloudSecretAgeResource,
		NewPveGotifyTargetResource,
		NewPveGraphiteExporterResource,
	}
}

func (p *PxcProvider) EphemeralResources(ctx context.Context) []func() ephemeral.EphemeralResource {
	return []func() ephemeral.EphemeralResource{
		NewKubeconfigEphemeralResource,
	}
}

func (p *PxcProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewCloudSelfDataSource,
		NewCloudFileSecretDataSource,
		NewCephAccessDataSource,
		NewSshKeyDataSource,
		NewPveApiGetDataSource,
		NewProxmoxHostDataSource,
		NewPveInventoryDataSource,
		NewCloudSecretDataSource,
		NewCloudSecretsDataSource,
		NewCloudVmsDataSource,
	}
}

func (p *PxcProvider) Functions(ctx context.Context) []func() function.Function {
	return []func() function.Function{}
}

func (p *PxcProvider) Actions(ctx context.Context) []func() action.Action {
	return []func() action.Action{}
}

func New(version string, exitCh chan bool) func() provider.Provider {
	return func() provider.Provider {
		return &PxcProvider{
			version: version,
			exitCh:  exitCh,
		}
	}
}


func GetCloudRpcService(ctx context.Context)(pb.CloudServiceClient, error){
	// init rpc client
	socketPath := fmt.Sprintf("unix:///tmp/pc-rpc-%d.sock", os.Getpid())

	// if this env var is set we connect to a manually launched pve cloud rpc server
	// for easier debugging
	manualPid := os.Getenv("PXC_RPC_MANUAL_PID")
	if manualPid != "" {
		socketPath = fmt.Sprintf("unix:///tmp/pc-rpc-%s.sock", manualPid)
	}

	tflog.Info(ctx, socketPath)
	conn, err := grpc.NewClient(
		socketPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, err
	}

	client := pb.NewCloudServiceClient(conn)

	return client, nil
}