# Terraform Proxmox Cloud Provider

This repository contains the terraform provider connecting to proxmox cloud instances.

## Building documentation

`go install` and `make generate`

## Building Proto files

```bash
# python proto files
pip install grpcio-tools-1.76.0

python -m grpc_tools.protoc -I./protos --python_out=./src/pve_cloud_rpc/protos --grpc_python_out=./src/pve_cloud_rpc/protos ./protos/*.proto
sed -i 's|import cloud_pb2|import pve_cloud_rpc.protos.cloud_pb2|g' src/pve_cloud_rpc/protos/cloud_pb2_grpc.py
sed -i 's|import health_pb2|import pve_cloud_rpc.protos.health_pb2|g' src/pve_cloud_rpc/protos/health_pb2_grpc.py

# golang proto files 
# need protocompiler 3 (installed via apt)
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

export PATH="$PATH:$(go env GOPATH)/bin"
protoc --go_out=./internal/provider --go_opt=paths=source_relative \
    --go-grpc_out=./internal/provider --go-grpc_opt=paths=source_relative \
    ./protos/*.proto

```

## TDD Dev

Supports proxmox cloud tddog development.

## Adding Dependencies

This provider uses [Go modules](https://github.com/golang/go/wiki/Modules).
Please see the Go documentation for the most up to date information about using Go modules.

To add a new dependency `github.com/author/dependency` to your Terraform provider:

```shell
go get github.com/author/dependency
go mod tidy
```

Then commit the changes to `go.mod` and `go.sum`.
