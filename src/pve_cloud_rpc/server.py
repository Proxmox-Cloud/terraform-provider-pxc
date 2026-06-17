import asyncio
import json
import os
import signal
import sys
from contextlib import AsyncExitStack

import asyncssh
import grpc
import yaml
from pve_cloud.cli.pvclu import (get_ssh_master_kubeconfig,
                                 get_ssh_remote_master_kubeconfig)
from pve_cloud.cli.pxrpc import PxrpcService, launch_pxrpc_async
from pve_cloud.lib.inventory import (get_cloud_domain, get_cluster_vars,
                                     get_online_pve_host_from_target_pve,
                                     get_pve_inventory)
from pve_cloud.lib.ssh import cleanup_jumphosts_async, get_jump_host_async


import pve_cloud_rpc.protos.cloud_pb2 as cloud_pb2
import pve_cloud_rpc.protos.cloud_pb2_grpc as cloud_pb2_grpc
import pve_cloud_rpc.protos.health_pb2 as health_pb2
import pve_cloud_rpc.protos.health_pb2_grpc as health_pb2_grpc


# make methods async callable for generic invoke
# this is so we can call pxrpc methods generically for a locally
# initialized instance
class PxrpcAsyncWrapper:

    def __init__(self, pxservice):
        self.pxservice =  pxservice

    def __getattr__(self, method_name):
        async def async_wrapper(*args, **kwargs):
            return getattr(self.pxservice, method_name)(*args, **kwargs)

        return  async_wrapper


class HealthServicer(health_pb2_grpc.HealthServicer):

    # this also performs the py-pve-cloud version check to not run against incompatible
    # installed proxmox cloud versions
    async def Check(self, request, context):
        target_pve = request.target_pve

        try:
            get_online_pve_host_from_target_pve(
                target_pve, skip_py_cloud_check=False
            )  # actually perform the check
            return health_pb2.HealthCheckResponse(
                status=health_pb2.HealthCheckResponse.SERVING
            )
        except RuntimeError as e:
            return health_pb2.HealthCheckResponse(
                status=health_pb2.HealthCheckResponse.MISSMATCH,
                error_message=f"py-pve-cloud version check failed with: {e}",
            )  # go provider process will kill


async def get_cstr_cvars(online_pve_host):
    async with asyncssh.connect(
        online_pve_host, username="root", known_hosts=None
    ) as conn:
        cmd = await conn.run("cat /etc/pve/cloud/secrets/patroni.pass", check=True)
        patroni_pass = cmd.stdout.rstrip()

        # fetch cluster vars to get internal proxy ip
        cmd = await conn.run("cat /etc/pve/cloud/cluster_vars.yaml", check=True)
        cluster_vars = yaml.safe_load(cmd.stdout)

    # build the connection string
    patroni_cstr = f"postgresql+psycopg2://postgres:{patroni_pass}@{cluster_vars['pve_haproxy_floating_ip_internal']}:5000/pve_cloud?sslmode=disable"

    return patroni_cstr, cluster_vars



class CloudServiceServicer(cloud_pb2_grpc.CloudServiceServicer):

    def __init__(self):
        self._stack = AsyncExitStack()  # here we dump all our pxrpc connections
        self.pxrpcs = {}  # map to reuse pxrpc connections

    # return local / remote instance of our pxrpcservice class
    async def get_pxrpc(self, online_pve_host, jump_host):
        print("fetching pxrpc", online_pve_host, jump_host)
        if not jump_host:
            # return async wrapper of locally initted service

            cstr, cluster_vars = await get_cstr_cvars(online_pve_host)
            return PxrpcAsyncWrapper(PxrpcService(cluster_vars, cstr))
        
        # if a jump host is specified we return from pxrpc remote service pool
        pxrpc_id = f"{online_pve_host}-{jump_host}"

        if pxrpc_id not in self.pxrpcs:
            print(f"launching new pxrpc server {online_pve_host}, {jump_host}")
            self.pxrpcs[pxrpc_id] = await self._stack.enter_async_context(
                launch_pxrpc_async(jump_host, online_pve_host)
            )

        return self.pxrpcs[pxrpc_id]

    # close the pxrpc connection(s)
    async def shutdown(self):
        await self._stack.aclose()

    async def GetMasterKubeconfig(self, request, context):
        target_pve = request.target_pve
        stack_name = request.stack_name

        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )
        print("getting master k8s", online_pve_host, jump_host)

        if jump_host and not request.extra_control_plane_sans:
            await context.abort(
                grpc.StatusCode.NOT_FOUND,
                "For proxmox clouds accessed via proxy jumps, kubernetes cluster need to be exposed via extra_control_plane_sans + custom dns!",
            )

        if jump_host:
            return cloud_pb2.GetKubeconfigResponse(
                config=get_ssh_remote_master_kubeconfig(
                    stack_name,
                    request.extra_control_plane_sans[0],
                    jump_host,
                    online_pve_host,
                )
            )
        else:
            cluster_vars = get_cluster_vars(online_pve_host)

            return cloud_pb2.GetKubeconfigResponse(
                config=get_ssh_master_kubeconfig(cluster_vars, stack_name)
            )

    async def GetClusterVars(self, request, context):
        target_pve = request.target_pve

        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )
        cluster_vars = get_cluster_vars(online_pve_host, jump_host)

        return cloud_pb2.GetClusterVarsResponse(vars=yaml.safe_dump(cluster_vars))

    # file secrets are default secrets created by the collections playbook stored on the proxmox hosts
    # under /etc/pve/cloud/secrets
    async def GetCloudFileSecret(self, request, context):
        target_pve = request.target_pve
        secret_name = request.secret_name

        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )

        # go through jump host if defined
        jc = None
        if jump_host:
            jc = await get_jump_host_async(jump_host)

        async with asyncssh.connect(
            online_pve_host, username="root", known_hosts=None, tunnel=jc
        ) as conn:
            cmd = await conn.run(
                f"cat /etc/pve/cloud/secrets/{secret_name}", check=True
            )
            catted_secret = cmd.stdout

            if (
                request.rstrip
            ):  # defaults to true but in special cases user might want to keep newlines (e.g. certs)
                catted_secret = catted_secret.rstrip()

        return cloud_pb2.GetCloudFileSecretResponse(secret=catted_secret)

    # non file proxmox cloud secrets are stored in the patroni database
    async def CreateCloudSecret(self, request, context):
        target_pve = request.target_pve
        cloud_domain = request.cloud_domain
        secret_name = request.secret_name
        secret_data = json.loads(request.secret_data)
        secret_type = request.secret_type

        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )

        # need to execute via pxrpc on jumphost
        pxrpc = await self.get_pxrpc(online_pve_host, jump_host)

        success = await pxrpc.inject_cloud_secret(
            cloud_domain,
            secret_name,
            json.dumps(secret_data),
            secret_type,
        )

        if not success:
            return cloud_pb2.CreateCloudSecretResponse(
                success=False, err_message="Unknown error in pxrpc."
            )

        return cloud_pb2.CreateCloudSecretResponse(success=True)


    async def DeleteCloudSecret(self, request, context):
        target_pve = request.target_pve
        secret_name = request.secret_name
        cloud_domain = request.cloud_domain

        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )

        pxrpc = await self.get_pxrpc(online_pve_host, jump_host)

        await pxrpc.delete_cloud_secret(cloud_domain, secret_name)

        return cloud_pb2.DeleteCloudSecretResponse(success=True)


    async def GetCloudSecret(self, request, context):
        target_pve = request.target_pve
        secret_name = request.secret_name
        cloud_domain = request.cloud_domain

        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )

        pxrpc = await self.get_pxrpc(online_pve_host, jump_host)

        secret_json = await pxrpc.get_cloud_secret(cloud_domain, secret_name)

        if secret_json == "":
            return cloud_pb2.GetCloudSecretResponse()

        return cloud_pb2.GetCloudSecretResponse(secret=secret_json)


    # fetch by type
    async def GetCloudSecrets(self, request, context):
        target_pve = request.target_pve
        secret_type = request.secret_type
        cloud_domain = request.cloud_domain

        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )

        pxrpc = await self.get_pxrpc(online_pve_host, jump_host)

        secrets_json = await pxrpc.get_cloud_secrets(cloud_domain, secret_type)

        return cloud_pb2.GetCloudSecretsResponse(secrets=secrets_json)


    async def GetVmVarsBlake(self, request, context):
        blake_ids = request.blake_ids
        target_pve = request.target_pve
        cloud_domain = request.cloud_domain

        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )

        pxrpc = await self.get_pxrpc(online_pve_host, jump_host)

        blake_ids_json = json.dumps(list(blake_ids))
        return_vars = await pxrpc.get_vm_vars_blake(blake_ids_json, cloud_domain)

        return cloud_pb2.GetVmVarsBlakeResponse(
            blake_id_vars={
                blake_id: json.dumps(vm_vars)
                for blake_id, vm_vars in json.loads(return_vars).items()
            }
        )


    async def GetCephAccess(self, request, context):
        target_pve = request.target_pve

        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )

        jc = None
        if jump_host:
            jc = await get_jump_host_async(jump_host)

        async with asyncssh.connect(
            online_pve_host, username="root", known_hosts=None, tunnel=jc
        ) as conn:
            cmd = await conn.run(f"cat /etc/ceph/ceph.conf", check=True)
            catted_conf = cmd.stdout

            cmd = await conn.run(
                f"cat /etc/pve/priv/ceph.client.admin.keyring", check=True
            )
            catted_keyring = cmd.stdout

        return cloud_pb2.GetCephAccessResponse(
            ceph_conf=catted_conf, admin_keyring=catted_keyring
        )

    async def GetSshKey(self, request, context):
        target_pve = request.target_pve

        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )

        jc = None
        if jump_host:
            jc = await get_jump_host_async(jump_host)

        async with asyncssh.connect(
            online_pve_host, username="root", known_hosts=None, tunnel=jc
        ) as conn:
            match request.key_type:
                case cloud_pb2.GetSshKeyRequest.PVE_HOST_RSA:
                    cmd = await conn.run(f"cat /root/.ssh/id_rsa", check=True)
                    catted_key = cmd.stdout
                case cloud_pb2.GetSshKeyRequest.AUTOMATION:
                    cmd = await conn.run(
                        f"cat /etc/pve/cloud/automation_id_ed25519", check=True
                    )
                    catted_key = cmd.stdout

        return cloud_pb2.GetSshKeyResponse(key=catted_key)

    async def GetProxmoxApi(self, request, context):
        target_pve = request.target_pve

        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )

        jc = None
        if jump_host:
            jc = await get_jump_host_async(jump_host)

        async with asyncssh.connect(
            online_pve_host, username="root", known_hosts=None, tunnel=jc
        ) as conn:
            args_string = None
            if request.get_args:
                args_string = " ".join(
                    f"{k} '{v}'" for k, v in request.get_args.items()
                )

            cmd = await conn.run(
                f"pvesh get {request.api_path} {args_string} --output-format json",
                check=True,
            )
            resp_json = cmd.stdout

        return cloud_pb2.GetProxmoxApiResponse(json_resp=resp_json)

    async def CreateProxmoxApi(self, request, context):
        target_pve = request.target_pve

        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )

        jc = None
        if jump_host:
            jc = await get_jump_host_async(jump_host)

        async with asyncssh.connect(
            online_pve_host, username="root", known_hosts=None, tunnel=jc
        ) as conn:
            args_string = None
            if request.create_args:
                args_string = " ".join(
                    f"{k} '{v}'" for k, v in request.create_args.items()
                )
            try:
                print(f"pvesh create {request.api_path} {args_string}")
                cmd = await conn.run(
                    f"pvesh create {request.api_path} {args_string}",
                    check=True,
                )
                print(cmd.stdout)
            except asyncssh.ProcessError as e:
                return cloud_pb2.CreateProxmoxApiResponse(
                    success=False, err_message=f"Exit code {e.exit_status} - {e.stderr}"
                )

        return cloud_pb2.CreateProxmoxApiResponse(success=True)

    async def DeleteProxmoxApi(self, request, context):
        target_pve = request.target_pve

        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )

        jc = None
        if jump_host:
            jc = await get_jump_host_async(jump_host)

        async with asyncssh.connect(
            online_pve_host, username="root", known_hosts=None, tunnel=jc
        ) as conn:
            try:
                cmd = await conn.run(
                    f"pvesh delete {request.api_path}",
                    check=True,
                )
                print(cmd.stdout)
            except asyncssh.ProcessError as e:
                return cloud_pb2.DeleteProxmoxApiResponse(
                    success=False, err_message=f"Exit code {e.exit_status} - {e.stderr}"
                )

        return cloud_pb2.DeleteProxmoxApiResponse(success=True)

    async def GetProxmoxHost(self, request, context):
        target_pve = request.target_pve
        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )

        return cloud_pb2.GetProxmoxHostResponse(pve_host=online_pve_host)

    async def GetCloudDomain(self, request, context):
        target_pve = request.target_pve

        cloud_domain = get_cloud_domain(target_pve)

        return cloud_pb2.GetCloudDomainResponse(domain=cloud_domain)

    async def GetPveInventory(self, request, context):
        target_pve = request.target_pve

        cloud_domain = get_cloud_domain(target_pve)
        pve_inventory = get_pve_inventory(cloud_domain, skip_py_cloud_check=True)

        return cloud_pb2.GetPveInventoryResponse(
            inventory=yaml.safe_dump(pve_inventory), cloud_domain=cloud_domain
        )

    async def GetDnsARecordSet(self, request, context):
        target_pve = request.target_pve
        host = request.host
        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )

        pxrpc = await self.get_pxrpc(online_pve_host, jump_host)

        addresses_json = await pxrpc.get_dns_a_record(host)

        return cloud_pb2.GetDnsARecordSetResponse(addrs=json.loads(addresses_json))


    async def CreateExternalAcmeTls(self, request, context):
        target_pve = request.target_pve
        stack_fqdn = request.stack_fqdn
        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )

        pxrpc = await self.get_pxrpc(online_pve_host, jump_host)

        await pxrpc.create_external_acme_tls(
            stack_fqdn, request.cert_config_json, request.ec_csr_json
        )

        return cloud_pb2.ExternalAcmeTlsResponse(success=True)


    async def DeleteExternalAcmeTls(self, request, context):
        target_pve = request.target_pve
        stack_fqdn = request.stack_fqdn
        online_pve_host, jump_host = get_online_pve_host_from_target_pve(
            target_pve, skip_py_cloud_check=True
        )

        pxrpc = await self.get_pxrpc(online_pve_host, jump_host)

        await pxrpc.delete_external_acme_tls(stack_fqdn)

        return cloud_pb2.ExternalAcmeTlsResponse(success=True)


async def serve():
    # patch the current asyncio loop to allow pxc async ssh calls
    asyncio.get_running_loop()._pxc_ssh_managed = True

    server = grpc.aio.server()
    servicer = CloudServiceServicer()
    cloud_pb2_grpc.add_CloudServiceServicer_to_server(servicer, server)

    health_servicer = HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)

    socket_file = f"/tmp/pc-rpc-{sys.argv[1]}.sock"

    server.add_insecure_port(f"unix://{socket_file}")

    # shudown logic
    shutdown_event = asyncio.Event()

    def receive_shutdown():
        shutdown_event.set()

    asyncio.get_running_loop().add_signal_handler(signal.SIGTERM, receive_shutdown)
    asyncio.get_running_loop().add_signal_handler(signal.SIGINT, receive_shutdown)

    try:
        await server.start()
        print(f"gRPC AsyncIO server running on {socket_file}")

        await asyncio.wait(
            [
                asyncio.create_task(server.wait_for_termination()),
                asyncio.create_task(shutdown_event.wait()),
            ],
            return_when=asyncio.FIRST_COMPLETED,
        )
    finally:
        # Ensure cleanup
        await servicer.shutdown()
        await server.stop(grace=0)
        print("gRPC server stopped and port released.")

        # call pxc cleanup functions
        await cleanup_jumphosts_async()

        # delete unix socket file
        if os.path.exists(socket_file):
            os.remove(socket_file)


def main():
    asyncio.run(serve())
