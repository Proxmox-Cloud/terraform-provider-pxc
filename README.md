# Terraform Provider Scaffolding (Terraform Plugin Framework)

_This template repository is built on the [Terraform Plugin Framework](https://github.com/hashicorp/terraform-plugin-framework). The template repository built on the [Terraform Plugin SDK](https://github.com/hashicorp/terraform-plugin-sdk) can be found at [terraform-provider-scaffolding](https://github.com/hashicorp/terraform-provider-scaffolding). See [Which SDK Should I Use?](https://developer.hashicorp.com/terraform/plugin/framework-benefits) in the Terraform documentation for additional information._

This repository is a *template* for a [Terraform](https://www.terraform.io) provider. It is intended as a starting point for creating Terraform providers, containing:

- A resource and a data source (`internal/provider/`),
- Examples (`examples/`) and generated documentation (`docs/`),
- Miscellaneous meta files.

These files contain boilerplate code that you will need to edit to create your own Terraform provider. Tutorials for creating Terraform providers can be found on the [HashiCorp Developer](https://developer.hashicorp.com/terraform/tutorials/providers-plugin-framework) platform. _Terraform Plugin Framework specific guides are titled accordingly._

Please see the [GitHub template repository documentation](https://help.github.com/en/github/creating-cloning-and-archiving-repositories/creating-a-repository-from-a-template) for how to create a new repository from this template on GitHub.

Once you've written your provider, you'll want to [publish it on the Terraform Registry](https://developer.hashicorp.com/terraform/registry/providers/publishing) so that others can use it.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0
- [Go](https://golang.org/doc/install) >= 1.24

## Building The Provider

1. Clone the repository
1. Enter the repository directory
1. Build the provider using the Go `install` command:

```shell
go install
```

## Adding Dependencies

This provider uses [Go modules](https://github.com/golang/go/wiki/Modules).
Please see the Go documentation for the most up to date information about using Go modules.

To add a new dependency `github.com/author/dependency` to your Terraform provider:

```shell
go get github.com/author/dependency
go mod tidy
```

Then commit the changes to `go.mod` and `go.sum`.

## Using the provider

Fill this in for each provider

## Developing the Provider

If you wish to work on the provider, you'll first need [Go](http://www.golang.org) installed on your machine (see [Requirements](#requirements) above).

To compile the provider, run `go install`. This will build the provider and put the provider binary in the `$GOPATH/bin` directory.

To generate or update documentation, run `make generate`.

In order to run the full suite of Acceptance tests, run `make testacc`.

*Note:* Acceptance tests create real resources, and often cost money to run.

```shell
make testacc
```


## TDD Dev


```bash

docker run -d \
  --name ephemeral-s3 \
  -p 9000:9000 \
  -e MINIO_ROOT_USER=minioadmin \
  -e MINIO_ROOT_PASSWORD=minioadmin \
  quay.io/minio/minio server /data

# create the bucket
docker exec ephemeral-s3 mc alias set local http://localhost:9000 minioadmin minioadmin
docker exec ephemeral-s3 mc mb local/tdd-providers


docker run -d -p 5601:5601 -e AWS_ACCESS_KEY_ID=minioadmin -e AWS_SECRET_ACCESS_KEY=minioadmin \
  ghcr.io/boring-registry/boring-registry:latest server \
  --storage-s3-endpoint http://localhost:9000 \
  --storage-s3-region minio \
  --storage-s3-bucket tdd-providers \
  --auth-static-token very-secure-token


export GNUPGHOME="$(pwd)/.gnupg"
mkdir -p "$GNUPGHOME"
chmod 700 "$GNUPGHOME"

gpg --batch --generate-key <<'EOF'
Key-Type: RSA
Key-Length: 2048
Subkey-Type: RSA
Subkey-Length: 2048
Name-Real: Cloud TDD Signing
Name-Email: noreply@example.invalid
Expire-Date: 0
%no-protection
%commit
EOF


# from python
VERSION="0.0.1"

export GOOS=linux

ARCH=$(uname -m)
case "$ARCH" in
  x86_64) ARCH=amd64 ;;
  aarch64) ARCH=arm64 ;;
  armv7l) ARCH=arm ;;
esac

export GOARCH=$ARCH


go build -o terraform-provider-proxmox-cloud_v$VERSION

mkdir -p ~/.tdd-providers/registry.terraform.io/pxc/proxmox-cloud/$VERSION/linux_$ARCH/

mv terraform-provider-proxmox-cloud_v$VERSION ~/.tdd-providers/registry.terraform.io/pxc/proxmox-cloud/$VERSION/linux_$ARCH/




terraform init -plugin-dir='~/.tdd-providers' -upgrade

zip terraform-provider-proxmox-cloud_${VERSION}_linux_$ARCH.zip terraform-provider-proxmox-cloud_v$VERSION README.md

sha256sum terraform-provider-proxmox-cloud_${VERSION}_linux_$ARCH.zip > terraform-provider-proxmox-cloud_${VERSION}_SHA256SUMS

gpg --detach-sign terraform-provider-proxmox-cloud_${VERSION}_SHA256SUMS

# todo: publish to bucket/providers/pxc/proxmox-cloud
# upload signing key

export AWS_ACCESS_KEY_ID=minioadmin
export AWS_SECRET_ACCESS_KEY=minioadmin
export AWS_DEFAULT_REGION=minio

aws s3 cp terraform-provider-proxmox-cloud_0.0.1218143636_linux_amd64.zip s3://tdd-providers/providers/pxc/proxmox-cloud \
    --endpoint-url http://localhost:9000 \
    --no-verify-ssl


aws s3 cp terraform-provider-proxmox-cloud_0.0.1218143636_SHA256SUMS s3://tdd-providers/providers/pxc/proxmox-cloud \
    --endpoint-url http://localhost:9000 \
    --no-verify-ssl

aws s3 cp terraform-provider-proxmox-cloud_0.0.1218143636_SHA256SUMS.sig s3://tdd-providers/providers/pxc/proxmox-cloud \
    --endpoint-url http://localhost:9000 \
    --no-verify-ssl


GPG_FINGERPRINT=$(gpg --list-keys --with-colons | awk -F: '/^fpr:/ {print $10; exit}')
gpg --armor --export $GPG_FINGERPRINT | jq -Rs "{\"gpg_public_keys\": [{\"key_id\": \"$GPG_FINGERPRINT$\", \"ascii_armor\": .}]}" > signing-keys.json

aws s3 cp signing-keys.json s3://tdd-providers/providers/pxc/ \
    --endpoint-url http://localhost:9000 \
    --no-verify-ssl



The token can be passed to Terraform inside the ~/.terraformrc configuration file:

credentials "localhost:5601" {
  token = "very-secure-token"
}



provider_installation {
  filesystem_mirror {
    path    = "/home/cloud/.tdd-providers"
    include = ["pxc/*"]
  }
  direct {
    exclude = ["pxc/*"]	
  }
}


```