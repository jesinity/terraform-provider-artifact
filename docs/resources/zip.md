# artifact_zip

Creates a ZIP archive from a directory.

This resource is deterministic when `deterministic = true`:
- stable file ordering
- stable timestamps/metadata (implementation-specific)
- produces repeatable `zip_sha256` outputs in CI pipelines

## Example

```hcl
resource "artifact_zip" "bundle" {
  input_dir   = "${path.module}/data"
  output_path = "${path.module}/dist/bundle.zip"

  include       = ["**/*"]
  exclude       = ["**/*.tmp", "bundle.zip"]
  deterministic = true

  # Optional: change triggers that force replacement.
  extra_triggers = {
    upstream_sha = artifact_download.s3_sink_zip.download_sha256
  }
}

output "zip_sha" {
  value = artifact_zip.bundle.zip_sha256
}
```

The output archive and its `.tmp` file are automatically excluded from the input
files, even when `output_path` is inside `input_dir`. You do not need an explicit
exclude pattern for the archive itself.
