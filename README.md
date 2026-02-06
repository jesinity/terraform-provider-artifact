# Terraform Provider: artifact

A Terraform provider for **controlled, repeatable artifact handling** during infrastructure provisioning.

This provider is intentionally **not a package manager** and **not a runtime downloader**.
Instead, it helps Terraform workflows **reference external artifacts declaratively** while keeping control over *when* downloads happen.

Typical use cases:
- Assemble deployment bundles (java/python plugins, Lambda layers, sidecar assets)
- Fetch third‑party artifacts during provisioning *only when inputs change*
- Avoid imperative `null_resource + local-exec` patterns
- Keep artifact logic explicit and reviewable in Terraform plans

---

## Philosophy (important ⚠️)

Terraform is declarative. Files on disk are not.

This provider embraces that reality:

- **Artifacts are treated as ephemeral build inputs**
- Terraform tracks *intent* (URL, coordinates, version), **not file existence**
- Downloads are triggered **only when inputs change**, not because files disappeared
- Users explicitly choose stricter behavior when needed

Why on Earth use it then? Because the local ephemeral artifact can be transferred to S3, Azure storage account or other cloud/non cloud persistent resources.
You can see this plugin as a better replacement for the imperative `null_resource + local-exec` patterns with a :

1. declarative intent (which artifact I want?)
2. declarative retrigger (when should I retrigger a download?)
3. portability (avoids wgetting data on the wire or other wizardries, reducing the need for other CLI tools to exists)
4. reduce some boilerplates of knowing Maven and PyPI internals.
 

This avoids endless re-downloads in CI while keeping plans stable.

---

## Requirements

- Terraform >= 1.5
- No external tools required at runtime

---

## Installation (Terraform Registry)

```hcl
terraform {
  required_providers {
    artifact = {
      source  = "jesinity/artifact"
      version = ">= 0.2.0"
    }
  }
}
```

---

## Artifact Refresh Modes

Artifact resources (`artifact_download`, `artifact_maven_download`,
`artifact_pypi_download`) support **three refresh modes** that control
when Terraform re-downloads the artifact.

This is intentionally explicit, because artifact downloads are
inherently *imperative* while Terraform is *declarative*.

You choose the trade-off.

------------------------------------------------------------------------

### 1️⃣ `refresh_mode = "none"` (default)

#### Behavior

-   The artifact is downloaded **only once**, when the resource is first
    created.
-   Terraform will **not** re-download the artifact on subsequent
    applies.
-   The file is treated as ephemeral local state.

#### When to use

-   CI/CD pipelines
-   Temporary build artifacts
-   When the artifact is copied elsewhere (e.g., uploaded to S3) and the
    local file is not important

#### Implications

-   If the local file disappears, Terraform will **not notice**.
-   This is a calculated risk by design.

``` hcl
resource "artifact_download" "plugin" {
  url          = "https://example.com/plugin.zip"
  output_path  = "${path.module}/.terraform/plugin.zip"
  refresh_mode = "none"
}
```

------------------------------------------------------------------------

### 2️⃣ `refresh_mode = "sha256"`

#### Behavior

-   Terraform re-downloads the artifact only if the content changes.
-   The provider computes the SHA256 from the downloaded bytes.
-   Downstream resources (e.g., `aws_s3_object`) can react using
    `source_hash`.

#### When to use

-   When artifact content changes but URL stays the same
-   When the artifact is uploaded to S3, Lambda, MSK Connect, etc.
-   When you want idempotent behavior across applies

#### Important

-   Terraform does not proactively check the remote file.
-   The SHA is updated only when Terraform decides to re-run the
    download.

Typical pattern:

``` hcl
resource "artifact_download" "plugin" {
  url          = "https://example.com/plugin.zip"
  output_path  = "${path.module}/.terraform/plugin.zip"
  refresh_mode = "sha256"
}

resource "aws_s3_object" "plugin" {
  bucket      = "my-bucket"
  key         = "plugins/plugin.zip"
  source      = artifact_download.plugin.output_path
  source_hash = artifact_download.plugin.sha256
}
```

This guarantees: - No unnecessary uploads - Automatic updates when
content changes

------------------------------------------------------------------------

## 3️⃣ `refresh_mode = "missing"`

### Behavior

-   The artifact is re-downloaded if the file does not exist on local disk.


### When to use

-   Bring artifacts into source code  
-   Shared folders with artifacts "mounted" in the terraform source code.



``` hcl
resource "artifact_download" "debug" {
  url          = "https://example.com/latest.zip"
  output_path  = "${path.module}/.terraform/latest.zip"
  refresh_mode = "missing"
}
```

------------------------------------------------------------------------

### Summary

| Mode     | Re-downloads                                  |  Terraform state changes                | Recommended                  |
|----------|-----------------------------------------------|-----------------------------------------|------------------------------|
| `none`   | Only when configuration changes               |  Only when configuration changes        | Default                      |
| `sha256` | When content hash differs (implies download)  |  When SHA changes (content changes)     | Best for mutable URLs        |
| `missing`| Only if output file is missing                |  When file does not exist anymore       | Artifact in/visible in code  |

### Design Rationale

Artifact downloads are side effects.

Terraform cannot guarantee that: - Local files persist - Remote
artifacts are immutable - External systems don't mutate state

This provider makes the trade-off explicit and leaves control to the
user.


---

## Resources Overview

| Resource | Purpose |
|--------|--------|
| `artifact_download` | Generic HTTP(S) artifact download |
| `artifact_maven_download` | Download from Maven repositories |
| `artifact_pypi_download` | Download Python packages from PyPI |
| `artifact_zip` | Create deterministic ZIP archives |

---

## Resource: `artifact_download`

Downloads an artifact over HTTP(S).

### Key behavior

- **Default**: download only when URL or inputs change
- Does **not** fail if the file disappears between applies
- Optional stricter checks via `refresh_strategy`

### Example

```hcl
resource "artifact_download" "plugin" {
  url         = "https://example.com/plugin.zip"
  output_path = "${path.module}/.terraform/plugin.zip"
}

output "sha" {
  value = artifact_download.plugin.sha256
}
```

### Refresh strategies

```hcl

refresh_strategy = "none"    # default, ephemeral
refresh_strategy = "missing" # recreate if file missing
refresh_strategy = "sha256"  # recreate if sha mismatch
```

---

## Resource: `artifact_maven_download`

Downloads artifacts from Maven repositories.

### Example

```hcl
resource "artifact_maven_download" "kafka_connect" {
  group_id    = "io.confluent"
  artifact_id = "kafka-connect-s3"
  version     = "11.0.8"

  output_path = "${path.module}/.terraform/s3.zip"
}
```

Supports:
- classifiers (`sources`, `javadoc`, custom)
- private Maven repos
- basic auth or bearer token
- same refresh semantics as `artifact_download`

---

## Resource: `artifact_pypi_download`

Downloads Python packages **without running pip**.

### Example

```hcl
resource "artifact_pypi_download" "requests" {
  package     = "requests"
  version     = "2.31.0"
  output_path = "${path.module}/.terraform/requests.whl"
}
```

Supports:
- public PyPI by default
- private PyPI (basic auth)
- wheels, source dists, or custom artifacts

---

## Resource: `artifact_zip`

Creates deterministic ZIP archives.

### Example

```hcl
resource "artifact_zip" "bundle" {
  input_dir   = "${path.module}/data"
  output_path = "${path.module}/bundle.zip"
  include     = ["**/*"]
  deterministic = true
}
```

Terraform will recreate the zip **only when inputs change**.

---

## Recommended Pattern

```hcl
artifact_download → artifact_zip → cloud resource
```

Example:

```hcl
resource "aws_s3_object" "plugin" {
  bucket = "my-bucket"
  key    = "plugin.zip"
  source = artifact_zip.bundle.output_path
}
```

This keeps artifact logic declarative and avoids shell scripts.

---

## What this provider is NOT

- ❌ Not a package manager
- ❌ Not a cache
- ❌ Not a replacement for build pipelines
- ❌ Not guaranteed persistence between applies

If you need immutability, store artifacts in S3, Artifactory, Nexus, etc.

---

## Development

### Local build

```bash
go build -o terraform-provider-artifact
```

### Terraform CLI override

```hcl
provider_installation {
  dev_overrides {
    "jesinity/artifact" = "/path/to/build"
  }
  direct {}
}
```

---

## License

MIT
