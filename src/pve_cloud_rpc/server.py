# Copyright (c) HashiCorp, Inc.
# SPDX-License-Identifier: MPL-2.0

import asyncio, asyncssh
import grpc
import pve_cloud_rpc.protos.cloud_pb2 as cloud_pb2
import pve_cloud_rpc.protos.cloud_pb2_grpc as cloud_pb2_grpc
import pve_cloud_rpc.protos.health_pb2 as health_pb2
import pve_cloud_rpc.protos.health_pb2_grpc as health_pb2_grpc
from pve_cloud.lib.inventory import *
from pve_cloud.cli.pvclu import get_cluster_vars, get_ssh_master_kubeconfig
import yaml
import socket
import sys


class HealthServicer(health_pb2_grpc.HealthServicer):
    def __init__(self):
        self._statuses = {}

    async def Check(self, request, context):
        service_name = request.service or ""
        status = self._statuses.get(service_name, health_pb2.HealthCheckResponse.UNKNOWN)
        print(f"Health Check request for service '{service_name}', returning {status}")
        return health_pb2.HealthCheckResponse(status=status)

    def set_status(self, service_name: str, status: health_pb2.HealthCheckResponse.ServingStatus):
        self._statuses[service_name] = status


class CloudServiceServicer(cloud_pb2_grpc.CloudServiceServicer):
    
    async def GetMasterKubeconfig(self, request, context):
        target_pve = request.target_pve
        stack_name = request.stack_name

        online_pve_host = get_online_pve_host(target_pve, skip_py_cloud_check=True)
        cluster_vars = get_cluster_vars(online_pve_host)

        return cloud_pb2.GetKubeconfigResponse(config=get_ssh_master_kubeconfig(cluster_vars, stack_name))


    async def GetClusterVars(self, request, context):
        target_pve = request.target_pve

        online_pve_host = get_online_pve_host(target_pve, skip_py_cloud_check=True)
        cluster_vars = get_cluster_vars(online_pve_host)

        return cloud_pb2.GetClusterVarsResponse(vars=yaml.safe_dump(cluster_vars))


    async def GetCloudSecret(self, request, context):
        target_pve = request.target_pve
        secret_name = request.secret_name

        online_pve_host = get_online_pve_host(target_pve, skip_py_cloud_check=True)
        async with asyncssh.connect(online_pve_host, username='root', known_hosts=None) as conn:
            cmd = await conn.run(f"cat /etc/pve/cloud/secrets/{secret_name}", check=True)
            catted_secret = cmd.stdout
            
        return cloud_pb2.GetCloudSecretResponse(secret=catted_secret)
    

    async def GetCephAccess(self, request, context):
        target_pve = request.target_pve

        online_pve_host = get_online_pve_host(target_pve, skip_py_cloud_check=True)
        async with asyncssh.connect(online_pve_host, username='root', known_hosts=None) as conn:
            cmd = await conn.run(f"cat /etc/ceph/ceph.conf", check=True)
            catted_conf = cmd.stdout

            cmd = await conn.run(f"cat /etc/pve/priv/ceph.client.admin.keyring", check=True)
            catted_keyring = cmd.stdout   


        return cloud_pb2.GetCephAccessResponse(ceph_conf=catted_conf, admin_keyring=catted_keyring)


    async def GetSshKey(self, request, context):
        target_pve = request.target_pve

        online_pve_host = get_online_pve_host(target_pve, skip_py_cloud_check=True)
        async with asyncssh.connect(online_pve_host, username='root', known_hosts=None) as conn:
            match request.key_type:
                case cloud_pb2.GetSshKeyRequest.PVE_HOST_RSA:
                    cmd = await conn.run(f"cat /root/.ssh/id_rsa", check=True)
                    catted_key = cmd.stdout   
                case cloud_pb2.GetSshKeyRequest.AUTOMATION:
                    cmd = await conn.run(f"cat /etc/pve/cloud/automation_id_ed25519", check=True)
                    catted_key = cmd.stdout  

        return cloud_pb2.GetSshKeyResponse(key=catted_key)
    

    async def GetProxmoxApi(self, request, context):
        target_pve = request.target_pve

        online_pve_host = get_online_pve_host(target_pve, skip_py_cloud_check=True)
        async with asyncssh.connect(online_pve_host, username='root', known_hosts=None) as conn:
            args_string = None
            if request.get_args:
                args_string = " ".join(f"{k} {v}" for k, v in request.get_args.items())
            
            cmd = await conn.run(f"pvesh get {request.api_path} {args_string} --output-format json", check=True)
            resp_json = cmd.stdout   

        return cloud_pb2.GetProxmoxApiResponse(json_resp=resp_json)
    

    async def GetProxmoxHost(self, request, context):
        target_pve = request.target_pve
        online_pve_host = get_online_pve_host(target_pve, skip_py_cloud_check=True)

        return cloud_pb2.GetProxmoxHostResponse(pve_host=online_pve_host)
    

    async def GetPveInventory(self, request, context):
        target_pve = request.target_pve

        cloud_domain = get_cloud_domain(target_pve)
        pve_inventory = get_pve_inventory(cloud_domain, skip_py_cloud_check=True)

        return cloud_pb2.GetPveInventoryResponse(inventory=yaml.safe_dump(pve_inventory), cloud_domain=cloud_domain)


def is_port_bound(port, host="0.0.0.0"):
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        try:
            s.bind((host, port))
            return False  # not bound
        except OSError:
            return True   # bound
        

async def serve():
    server = grpc.aio.server()
    cloud_pb2_grpc.add_CloudServiceServicer_to_server(CloudServiceServicer(), server)

    health_servicer = HealthServicer()
    health_pb2_grpc.add_HealthServicer_to_server(health_servicer, server)

    # if is_port_bound(50052): # google quirks nice
    #     raise RuntimeError("PCRPC Already running / unclean shutdown!")
    
    socket_file = f"/tmp/pc-rpc-{sys.argv[1]}.sock"

    # listen_addr = "[::]:50052"
    server.add_insecure_port(f"unix://{socket_file}") 
    await server.start()

    health_servicer.set_status(
        "",  # empty = overall server health
        health_pb2.HealthCheckResponse.SERVING
    )
    # print(f"gRPC AsyncIO server running on {listen_addr}")
    print(f"gRPC AsyncIO server running on {socket_file}")
    try:
        await server.wait_for_termination()
    finally:
        # Ensure cleanup
        await server.stop(grace=0)
        print("gRPC server stopped and port released.")

        # delete unix socket file
        if os.path.exists(socket_file):
            os.remove(socket_file)



def main():
    asyncio.run(serve())