# Terraform Provider: artifact

A small Terraform provider that helps with common “artifact pipeline” tasks during infra provisioning:
- Download a remote artifact to a local file (`artifact_download`)
- Create a ZIP from a directory (`artifact_zip`)

This is useful when you need to assemble deployment bundles as part of Terraform (e.g., MSK Connect plugins, Lambda layers, etc.).

## Requirements
- Terraform >= 1.5 (recommended)
- Go is only required for building the provider from source

## Installation (Terraform Registry)

```hcl
terraform {
  required_providers {
    artifact = {
      source  = "jesinity/artifact"
      version = ">= 0.0.1"
    }
  }
}
```

## Resources

### `artifact_download`
Downloads a URL to a local file (supports redirects, optional expected SHA256).

Example:

```hcl
resource "artifact_download" "s3_sink" {
  url              = "https://api.hub.confluent.io/api/plugins/confluentinc/kafka-connect-s3/versions/11.0.8/archive"
  output_path      = "${path.module}/data/s3.zip"
  follow_redirects = true
  timeout_seconds  = 120
}

output "download_sha" {
  value = artifact_download.s3_sink.download_sha256
}
```

### `artifact_zip`
Creates a zip from a local directory (optionally include patterns).

Example:

```hcl
resource "artifact_zip" "bundle" {
  input_dir   = "${path.module}/data/some-folder"
  output_path = "${path.module}/data/bundle.zip"
  include     = ["**/*"]
}

output "zip_sha" {
  value = artifact_zip.bundle.zip_sha256
}
```

## Development

### Local testing with Terraform dev override
Build the provider:

```bash
go build -o terraform-provider-artifact
```

Configure Terraform CLI dev override (e.g. `~/.terraformrc`) to point to your build output folder, then run:

```bash
terraform init
terraform plan
terraform apply
```

## License
MIT
