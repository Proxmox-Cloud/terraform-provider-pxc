import asyncio
import json
import sys
from contextlib import AsyncExitStack

import asyncssh
import grpc
import yaml
from pve_cloud.cli.pvclu import (get_cluster_vars, get_ssh_master_kubeconfig,
                                 get_ssh_remote_master_kubeconfig)
from pve_cloud.cli.pxrpc import launch_pxrpc_async
from pve_cloud.lib.inventory import *
from pve_cloud.orm.alchemy import ProxmoxCloudSecrets, VirtualMachineVars
from sqlalchemy import create_engine, delete, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

import pve_cloud_rpc.protos.cloud_pb2 as cloud_pb2
import pve_cloud_rpc.protos.cloud_pb2_grpc as cloud_pb2_grpc
import pve_cloud_rpc.protos.health_pb2 as health_pb2
import pve_cloud_rpc.protos.health_pb2_grpc as health_pb2_grpc


class HealthServicer(health_pb2_grpc.HealthServicer):

    # this also performs the py-pve-cloud version check to not run against incompatible
    # installed proxmox cloud versions
    async def Check(self, request, context):
        target_pve = request.target_pve

        try:
            get_online_pve_host(
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


async def get_engine(online_pve_host):
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

    # insert the secret
    engine = create_engine(patroni_cstr)

    return engine


# fabric host
def get_pg_conn_str(pve_host):
    result = pve_host.run("cat /etc/pve/cloud/secrets/patroni.pass")
    patroni_pass = result.stdout.rstrip()

    result = pve_host.run("cat /etc/pve/cloud/cluster_vars.yaml")
    cluster_vars = yaml.safe_load(result.stdout)

    patroni_cstr = f"postgresql+psycopg2://postgres:{patroni_pass}@{cluster_vars['pve_haproxy_floating_ip_internal']}:5000/pve_cloud?sslmode=disable"

    return patroni_cstr


class CloudServiceServicer(cloud_pb2_grpc.CloudServiceServicer):

    def __init__(self):
        self._stack = AsyncExitStack()  # here we dump all our pxrpc connections
        self.pxrpcs = {}  # map to reuse pxrpc connections

    # with this we reuse our pxrpc instances
    async def get_pxrpc(self, online_pve_host, jump_host):
        pxrpc_id = f"{online_pve_host}-{jump_host}"

        if pxrpc_id not in self.pxrpcs:
            print(f"launching new pxrpc server {online_pve_host}, {jump_host}")
            self.pxrpcs[pxrpc_id] = await self._stack.enter_async_context(
                launch_pxrpc_async(jump_host, online_pve_host)
            )

        pxrpc, pve_host = self.pxrpcs[pxrpc_id]

        return pxrpc, pve_host

    # close the pxrpc connection(s)
    async def shutdown(self):
        await self._stack.aclose()

    async def GetMasterKubeconfig(self, request, context):
        target_pve = request.target_pve
        stack_name = request.stack_name

        online_pve_host, jump_host = get_online_pve_host(
            target_pve, skip_py_cloud_check=True
        )
        if jump_host and not request.extra_control_plane_sans:
            await context.abort(
                grpc.StatusCode.NOT_FOUND,
                "For proxmox clouds accessed via proxy jumps, kubernetes cluster need to be exposed via extra_control_plane_sans + custom dns!",
            )

        cluster_vars = get_cluster_vars(online_pve_host)

        if jump_host:
            return cloud_pb2.GetKubeconfigResponse(
                config=get_ssh_remote_master_kubeconfig(
                    cluster_vars,
                    stack_name,
                    request.extra_control_plane_sans[0],
                    jump_host,
                    online_pve_host,
                )
            )
        else:
            return cloud_pb2.GetKubeconfigResponse(
                config=get_ssh_master_kubeconfig(cluster_vars, stack_name)
            )

    async def GetClusterVars(self, request, context):
        target_pve = request.target_pve

        online_pve_host, jump_host = get_online_pve_host(
            target_pve, skip_py_cloud_check=True
        )
        cluster_vars = get_cluster_vars(online_pve_host, jump_host)

        return cloud_pb2.GetClusterVarsResponse(vars=yaml.safe_dump(cluster_vars))

    # file secrets are default secrets created by the collections playbook stored on the proxmox hosts
    # under /etc/pve/cloud/secrets
    async def GetCloudFileSecret(self, request, context):
        target_pve = request.target_pve
        secret_name = request.secret_name

        online_pve_host, jump_host = get_online_pve_host(
            target_pve, skip_py_cloud_check=True
        )

        # go through jump host if defined
        jc = None
        if jump_host:
            jc = await asyncssh.connect(jump_host, username="root", known_hosts=None)

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

        # close the jumphost if it was defined
        if jc:
            jc.close()
            await jc.wait_closed()

        return cloud_pb2.GetCloudFileSecretResponse(secret=catted_secret)

    # non file proxmox cloud secrets are stored in the patroni database
    async def CreateCloudSecret(self, request, context):
        target_pve = request.target_pve
        cloud_domain = request.cloud_domain
        secret_name = request.secret_name
        secret_data = json.loads(request.secret_data)
        secret_type = request.secret_type

        online_pve_host, jump_host = get_online_pve_host(
            target_pve, skip_py_cloud_check=True
        )
        if jump_host:
            # need to execute via pxrpc on jumphost
            pxrpc, pve_host = await self.get_pxrpc(online_pve_host, jump_host)

            pg_conn_str = get_pg_conn_str(pve_host)
            success = await pxrpc.inject_cloud_secret(
                pg_conn_str,
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

        else:
            engine = await get_engine(online_pve_host)

            with Session(engine) as session:
                try:
                    session.add(
                        ProxmoxCloudSecrets(
                            cloud_domain=cloud_domain,
                            secret_name=secret_name,
                            secret_data=secret_data,
                            secret_type=secret_type,
                        )
                    )
                    session.commit()

                except IntegrityError as e:
                    session.rollback()
                    return cloud_pb2.CreateCloudSecretResponse(
                        success=False, err_message=str(e)
                    )

            return cloud_pb2.CreateCloudSecretResponse(success=True)

    async def DeleteCloudSecret(self, request, context):
        target_pve = request.target_pve
        secret_name = request.secret_name
        cloud_domain = request.cloud_domain

        online_pve_host, jump_host = get_online_pve_host(
            target_pve, skip_py_cloud_check=True
        )

        if jump_host:
            pxrpc, pve_host = await self.get_pxrpc(online_pve_host, jump_host)

            pg_conn_str = get_pg_conn_str(pve_host)
            await pxrpc.delete_cloud_secret(pg_conn_str, cloud_domain, secret_name)

            return cloud_pb2.DeleteCloudSecretResponse(success=True)

        else:
            engine = await get_engine(online_pve_host)

            with Session(engine) as session:
                stmt = delete(ProxmoxCloudSecrets).where(
                    ProxmoxCloudSecrets.cloud_domain == cloud_domain,
                    ProxmoxCloudSecrets.secret_name == secret_name,
                )

                result = session.execute(stmt)
                session.commit()

            return cloud_pb2.DeleteCloudSecretResponse(success=True)

    async def GetCloudSecret(self, request, context):
        target_pve = request.target_pve
        secret_name = request.secret_name
        cloud_domain = request.cloud_domain

        online_pve_host, jump_host = get_online_pve_host(
            target_pve, skip_py_cloud_check=True
        )

        if jump_host:
            pxrpc, pve_host = await self.get_pxrpc(online_pve_host, jump_host)

            pg_conn_str = get_pg_conn_str(pve_host)

            secret_json = await pxrpc.get_cloud_secret(
                pg_conn_str, cloud_domain, secret_name
            )

            if secret_json == "":
                return cloud_pb2.GetCloudSecretResponse()

            return cloud_pb2.GetCloudSecretResponse(secret=secret_json)

        else:
            engine = await get_engine(online_pve_host)

            with Session(engine) as session:
                stmt = select(ProxmoxCloudSecrets).where(
                    ProxmoxCloudSecrets.cloud_domain == cloud_domain,
                    ProxmoxCloudSecrets.secret_name == secret_name,
                )
                record = session.scalars(stmt).first()

            if not record:
                return cloud_pb2.GetCloudSecretResponse()

            return cloud_pb2.GetCloudSecretResponse(
                secret=json.dumps(record.secret_data)
            )

    # fetch by type
    async def GetCloudSecrets(self, request, context):
        target_pve = request.target_pve
        secret_type = request.secret_type
        cloud_domain = request.cloud_domain

        online_pve_host, jump_host = get_online_pve_host(
            target_pve, skip_py_cloud_check=True
        )

        if jump_host:
            pxrpc, pve_host = await self.get_pxrpc(online_pve_host, jump_host)

            pg_conn_str = get_pg_conn_str(pve_host)

            secrets_json = await pxrpc.get_cloud_secrets(
                pg_conn_str, cloud_domain, secret_type
            )

            return cloud_pb2.GetCloudSecretsResponse(secrets=secrets_json)

        else:
            engine = await get_engine(online_pve_host)

            with Session(engine) as session:
                stmt = select(ProxmoxCloudSecrets).where(
                    ProxmoxCloudSecrets.cloud_domain == cloud_domain,
                    ProxmoxCloudSecrets.secret_type == secret_type,
                )
                records = session.scalars(stmt).all()

            return cloud_pb2.GetCloudSecretsResponse(
                secrets=json.dumps(
                    {record.secret_name: record.secret_data for record in records}
                )
            )

    async def GetVmVarsBlake(self, request, context):
        blake_ids = request.blake_ids
        target_pve = request.target_pve
        cloud_domain = request.cloud_domain

        online_pve_host, jump_host = get_online_pve_host(
            target_pve, skip_py_cloud_check=True
        )

        if jump_host:
            pxrpc, pve_host = await self.get_pxrpc(online_pve_host, jump_host)

            pg_conn_str = get_pg_conn_str(pve_host)

            blake_ids_json = json.dumps(list(blake_ids))
            return_vars = await pxrpc.get_vm_vars_blake(
                pg_conn_str, blake_ids_json, cloud_domain
            )

            return cloud_pb2.GetVmVarsBlakeResponse(
                blake_id_vars={
                    blake_id: json.dumps(vm_vars)
                    for blake_id, vm_vars in json.loads(return_vars).items()
                }
            )

        else:
            engine = await get_engine(online_pve_host)

            with Session(engine) as session:
                stmt = select(VirtualMachineVars).where(
                    VirtualMachineVars.blake_id.in_(blake_ids),
                    VirtualMachineVars.cloud_domain == cloud_domain,
                )
                records = session.scalars(stmt).all()

            return cloud_pb2.GetVmVarsBlakeResponse(
                blake_id_vars={
                    entry.blake_id: json.dumps(entry.vm_vars) for entry in records
                }
            )

    async def GetCephAccess(self, request, context):
        target_pve = request.target_pve

        online_pve_host, jump_host = get_online_pve_host(
            target_pve, skip_py_cloud_check=True
        )

        jc = None
        if jump_host:
            jc = await asyncssh.connect(jump_host, username="root", known_hosts=None)

        async with asyncssh.connect(
            online_pve_host, username="root", known_hosts=None, tunnel=jc
        ) as conn:
            cmd = await conn.run(f"cat /etc/ceph/ceph.conf", check=True)
            catted_conf = cmd.stdout

            cmd = await conn.run(
                f"cat /etc/pve/priv/ceph.client.admin.keyring", check=True
            )
            catted_keyring = cmd.stdout

        if jc:
            jc.close()
            await jc.wait_closed()

        return cloud_pb2.GetCephAccessResponse(
            ceph_conf=catted_conf, admin_keyring=catted_keyring
        )

    async def GetSshKey(self, request, context):
        target_pve = request.target_pve

        online_pve_host, jump_host = get_online_pve_host(
            target_pve, skip_py_cloud_check=True
        )

        jc = None
        if jump_host:
            jc = await asyncssh.connect(jump_host, username="root", known_hosts=None)

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

        if jc:
            jc.close()
            await jc.wait_closed()

        return cloud_pb2.GetSshKeyResponse(key=catted_key)

    async def GetProxmoxApi(self, request, context):
        target_pve = request.target_pve

        online_pve_host, jump_host = get_online_pve_host(
            target_pve, skip_py_cloud_check=True
        )

        jc = None
        if jump_host:
            jc = await asyncssh.connect(jump_host, username="root", known_hosts=None)

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

        if jc:
            jc.close()
            await jc.wait_closed()

        return cloud_pb2.GetProxmoxApiResponse(json_resp=resp_json)

    async def CreateProxmoxApi(self, request, context):
        target_pve = request.target_pve

        online_pve_host, jump_host = get_online_pve_host(
            target_pve, skip_py_cloud_check=True
        )

        jc = None
        if jump_host:
            jc = await asyncssh.connect(jump_host, username="root", known_hosts=None)

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

        if jc:
            jc.close()
            await jc.wait_closed()

        return cloud_pb2.CreateProxmoxApiResponse(success=True)

    async def DeleteProxmoxApi(self, request, context):
        target_pve = request.target_pve

        online_pve_host, jump_host = get_online_pve_host(
            target_pve, skip_py_cloud_check=True
        )

        jc = None
        if jump_host:
            jc = await asyncssh.connect(jump_host, username="root", known_hosts=None)

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

        if jc:
            jc.close()
            await jc.wait_closed()

        return cloud_pb2.DeleteProxmoxApiResponse(success=True)

    async def GetProxmoxHost(self, request, context):
        target_pve = request.target_pve
        online_pve_host, jump_host = get_online_pve_host(
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


async def serve():
    server = grpc.aio.server()
    servicer = CloudServiceServicer()
    cloud_pb2_grpc.add_CloudServiceServicer_to_server(servicer, server)

    health_servicer = HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)

    socket_file = f"/tmp/pc-rpc-{sys.argv[1]}.sock"

    server.add_insecure_port(f"unix://{socket_file}")
    try:
        await server.start()
        print(f"gRPC AsyncIO server running on {socket_file}")

        await server.wait_for_termination()
    finally:
        # Ensure cleanup
        await servicer.shutdown()
        await server.stop(grace=0)
        print("gRPC server stopped and port released.")

        # delete unix socket file
        if os.path.exists(socket_file):
            os.remove(socket_file)


def main():
    asyncio.run(serve())
