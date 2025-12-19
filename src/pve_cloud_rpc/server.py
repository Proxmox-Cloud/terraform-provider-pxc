import asyncio
import grpc
import pve_cloud_rpc.protos.cloud_pb2 as cloud_pb2
import pve_cloud_rpc.protos.cloud_pb2_grpc as cloud_pb2_grpc
from pve_cloud.lib.inventory import *
from pve_cloud.cli.pvclu import get_cloud_env, get_ssh_master_kubeconfig


class CloudServiceServicer(cloud_pb2_grpc.CloudServiceServicer):
    async def GetMasterKubeconfig(self, request, context):
        target_pve = request.target_pve
        stack_name = request.stack_name

        online_pve_host = get_online_pve_host(target_pve)
        cluster_vars, patroni_pass, bind_internal_key = get_cloud_env(online_pve_host)

        return cloud_pb2.GetKubeconfigResponse(config=get_ssh_master_kubeconfig(cluster_vars, stack_name))


async def serve():
    server = grpc.aio.server()
    cloud_pb2_grpc.add_CloudServiceServicer_to_server(CloudServiceServicer(), server)
    listen_addr = "[::]:50051"
    server.add_insecure_port(listen_addr)
    await server.start()
    print(f"gRPC AsyncIO server running on {listen_addr}")
    await server.wait_for_termination()


def main():
    asyncio.run(serve())