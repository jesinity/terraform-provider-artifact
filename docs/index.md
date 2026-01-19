---
page_title: "artifact Provider"
description: "Provider for downloading, packaging, and managing build artifacts such as HTTP files, ZIPs, Maven artifacts, and PyPI packages."
---

# artifact Provider

The **artifact** provider offers small, focused resources that help automate common
*artifact pipeline* tasks during Terraform runs.

It is intentionally **simple, deterministic, and side-effect–oriented**, designed for:
- CI/CD pipelines
- build systems
- integration environments
- controlled artifact fetching

This provider **does not attempt to replace package managers** (like Maven, pip, npm).
Instead, it focuses on **retrieving immutable artifacts over HTTP** and making them
available as Terraform-managed build inputs.

## Available Resources

- **`artifact_download`** – Download a generic HTTP/HTTPS artifact
- **`artifact_zip`** – Package a directory into a ZIP archive
- **`artifact_maven_download`** – Download artifacts from Maven repositories
- **`artifact_pypi_download`** – Download Python packages from PyPI-compatible indexes

## Common Use Cases

- Fetch Kafka Connect plugins
- Download vendor SDKs or CLIs
- Materialize build-time dependencies
- Package files for cloud services (Lambda, MSK, Glue, etc.)

## Provider Configuration

```hcl
terraform {
  required_providers {
    artifact = {
      source  = "jesinity/artifact"
      version = ">= 0.1.0"
    }
  }
}
```
