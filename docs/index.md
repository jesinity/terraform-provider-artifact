---
page_title: "artifact Provider"
description: "Provider for simple artifact download and packaging tasks."
---

# artifact Provider

The `artifact` provider offers small, focused resources that help automate common “artifact pipeline” tasks during Terraform runs:

- Download a remote artifact to a local file (`artifact_download`)
- Package a directory into a ZIP file (`artifact_zip`)

## Example

```hcl
terraform {
  required_providers {
    artifact = {
      source  = "jesinity/artifact"
      version = ">= 0.0.1"
    }
  }
}

resource "artifact_download" "example" {
  url              = "https://example.com/file.zip"
  output_path      = "${path.module}/data/file.zip"
  follow_redirects = true
  timeout_seconds  = 120
}

output "sha" {
  value = artifact_download.example.download_sha256
}
```
